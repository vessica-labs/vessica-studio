package server

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"

	"github.com/vessica-labs/vessica-studio/internal/studio"
)

type recordingPowerPointRenderer struct {
	mu       sync.Mutex
	visual   [][]string
	editable [][]string
}

func (f *recordingPowerPointRenderer) Visual(_ context.Context, _ *http.Request, _ string, ids []string) ([][]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.visual = append(f.visual, append([]string(nil), ids...))
	out := make([][]byte, len(ids))
	pixel, _ := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=")
	for i := range ids {
		out[i] = append([]byte(nil), pixel...)
	}
	return out, nil
}

func (f *recordingPowerPointRenderer) Editable(_ *http.Request, _ string, ids []string) (studio.PPTXDeck, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.editable = append(f.editable, append([]string(nil), ids...))
	out := studio.PPTXDeck{Title: "test"}
	for _, id := range ids {
		out.Slides = append(out.Slides, studio.PPTXSlide{ID: id})
	}
	return out, nil
}

func addCacheTestSlide(t *testing.T, st *studio.Studio) {
	t.Helper()
	if err := os.WriteFile(st.SlidePath("demo", "0020-b", ".html"), []byte(`<section class="slide"><div class="s-title">Second</div></section>`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(st.SlidePath("demo", "0020-b", ".md"), []byte("# Second\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestPowerPointVisualCacheRendersOnlyMisses(t *testing.T) {
	st := testStudio(t)
	addCacheTestSlide(t, st)
	fake := &recordingPowerPointRenderer{}
	s := New(st, ModeStudio)
	s.PowerPointRenderer = fake
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	ids := []string{"0010-a", "0020-b"}

	_, stats, err := s.cachedVisualPowerPointSlides(context.Background(), req, "demo", ids)
	if err != nil || stats.Misses != 2 || !reflect.DeepEqual(fake.visual, [][]string{ids}) {
		t.Fatalf("first export stats=%+v calls=%v err=%v", stats, fake.visual, err)
	}
	_, stats, err = s.cachedVisualPowerPointSlides(context.Background(), req, "demo", ids)
	if err != nil || stats.Hits != 2 || len(fake.visual) != 1 {
		t.Fatalf("repeat stats=%+v calls=%v err=%v", stats, fake.visual, err)
	}
	if err := os.WriteFile(st.SlidePath("demo", "0020-b", ".html"), []byte(`<section class="slide">Changed</section>`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, stats, err = s.cachedVisualPowerPointSlides(context.Background(), req, "demo", ids)
	if err != nil || stats.Hits != 1 || stats.Misses != 1 || !reflect.DeepEqual(fake.visual[1], []string{"0020-b"}) {
		t.Fatalf("fragment change stats=%+v calls=%v err=%v", stats, fake.visual, err)
	}
	if err := os.WriteFile(filepath.Join(st.ThemeDir("default"), "theme.css"), []byte(".slide{color:red}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, stats, err = s.cachedVisualPowerPointSlides(context.Background(), req, "demo", ids)
	if err != nil || stats.Misses != 2 || !reflect.DeepEqual(fake.visual[2], ids) {
		t.Fatalf("theme change stats=%+v calls=%v err=%v", stats, fake.visual, err)
	}
	manifest := readPowerPointManifest(s.powerpointCacheDir("demo"))
	entry := manifest.Entries[cacheKey("visual", "0010-a")]
	if err := os.WriteFile(filepath.Join(s.powerpointCacheDir("demo"), filepath.FromSlash(entry.File)), []byte("corrupt"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, stats, err = s.cachedVisualPowerPointSlides(context.Background(), req, "demo", ids)
	if err != nil || stats.Misses != 1 || !reflect.DeepEqual(fake.visual[3], []string{"0010-a"}) {
		t.Fatalf("corrupt visual recovery stats=%+v calls=%v err=%v", stats, fake.visual, err)
	}
}

func TestPowerPointEditableCacheCurrentSlideAndCorruptionRecovery(t *testing.T) {
	st := testStudio(t)
	addCacheTestSlide(t, st)
	fake := &recordingPowerPointRenderer{}
	s := New(st, ModeStudio)
	s.PowerPointRenderer = fake
	req := httptest.NewRequest(http.MethodGet, "/", nil)

	slides, stats, err := s.cachedEditablePowerPointSlides(req, "demo", []string{"0020-b"})
	if err != nil || len(slides) != 1 || stats.Misses != 1 || !reflect.DeepEqual(fake.editable, [][]string{{"0020-b"}}) {
		t.Fatalf("current slide stats=%+v calls=%v slides=%v err=%v", stats, fake.editable, slides, err)
	}
	manifest := readPowerPointManifest(s.powerpointCacheDir("demo"))
	entry := manifest.Entries[cacheKey("editable", "0020-b")]
	if err := os.WriteFile(filepath.Join(s.powerpointCacheDir("demo"), filepath.FromSlash(entry.File)), []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, stats, err = s.cachedEditablePowerPointSlides(req, "demo", []string{"0020-b"})
	if err != nil || stats.Misses != 1 || len(fake.editable) != 2 {
		t.Fatalf("corrupt recovery stats=%+v calls=%v err=%v", stats, fake.editable, err)
	}
}

func TestPowerPointCacheSerializesConcurrentCreation(t *testing.T) {
	st := testStudio(t)
	fake := &recordingPowerPointRenderer{}
	s := New(st, ModeStudio)
	s.PowerPointRenderer = fake
	start := make(chan struct{})
	errs := make(chan error, 2)
	for range 2 {
		go func() {
			<-start
			_, _, err := s.cachedVisualPowerPointSlides(context.Background(), httptest.NewRequest(http.MethodGet, "/", nil), "demo", []string{"0010-a"})
			errs <- err
		}()
	}
	close(start)
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	if len(fake.visual) != 1 {
		t.Fatalf("concurrent creation rendered %d times: %v", len(fake.visual), fake.visual)
	}
}

func TestCatalogThumbnailGeneratesOnceThenUsesCache(t *testing.T) {
	t.Setenv("VSTD_USER_CONFIG_DIR", t.TempDir())
	st := testStudio(t)
	fake := &recordingPowerPointRenderer{}
	s := New(st, ModeStudio)
	s.PowerPointRenderer = fake
	h := s.Routes()

	for i := range 2 {
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/app/decks/demo/thumbnail.png", nil))
		if rr.Code != http.StatusOK || rr.Header().Get("Content-Type") != "image/png" {
			t.Fatalf("request %d status=%d content-type=%q body=%q", i+1, rr.Code, rr.Header().Get("Content-Type"), rr.Body.String())
		}
		if got := rr.Header().Get("Cache-Control"); got != "private, max-age=300" {
			t.Fatalf("request %d cache-control=%q", i+1, got)
		}
	}
	if !reflect.DeepEqual(fake.visual, [][]string{{"0010-a"}}) {
		t.Fatalf("thumbnail renders = %v, want one first-slide render", fake.visual)
	}
}

func TestCatalogThumbnailFailuresAreNotCached(t *testing.T) {
	t.Setenv("VSTD_USER_CONFIG_DIR", t.TempDir())
	s := New(testStudio(t), ModeStudio)
	rr := httptest.NewRecorder()
	s.Routes().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/app/decks/missing/thumbnail.png", nil))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%q", rr.Code, rr.Body.String())
	}
	if got := rr.Header().Get("Cache-Control"); got != "private, no-store" {
		t.Fatalf("cache-control=%q", got)
	}
}
