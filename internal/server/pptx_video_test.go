package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/vessica-labs/vessica-studio/internal/library"
	"github.com/vessica-labs/vessica-studio/internal/studio"
)

func TestPowerPointVideoHydrationChecksManifestAndBytes(t *testing.T) {
	st := testStudio(t)
	s := New(st, ModeStudio)
	content := []byte("test-mp4-content")
	hash := sha256.Sum256(content)
	asset := library.VideoAsset{ID: "video-test", File: "video/test.mp4", Hash: hex.EncodeToString(hash[:]), Bytes: int64(len(content))}
	manifest := library.Manifest{Videos: []library.VideoAsset{asset}}
	os.MkdirAll(filepath.Join(st.Root, "library/video"), 0700)
	writeManifest := func() {
		t.Helper()
		data, _ := json.Marshal(manifest)
		if err := os.WriteFile(filepath.Join(st.Root, "library/manifest.json"), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	writeManifest()
	if err := os.WriteFile(filepath.Join(st.Root, "library", asset.File), content, 0600); err != nil {
		t.Fatal(err)
	}
	model := func() studio.PPTXDeck {
		return studio.PPTXDeck{Slides: []studio.PPTXSlide{{Elements: []studio.PPTXElement{{Kind: "video", VideoID: asset.ID}, {Kind: "video", VideoID: asset.ID}}}}}
	}
	deck := model()
	if err := s.hydratePPTXVideos(httptest.NewRequest("GET", "/", nil), &deck); err != nil {
		t.Fatal(err)
	}
	for _, e := range deck.Slides[0].Elements {
		if string(e.VideoData) != string(content) {
			t.Fatal("video was not embedded")
		}
	}
	// Missing source bytes must fail rather than returning an image-only deck.
	os.Remove(filepath.Join(st.Root, "library", asset.File))
	deck = model()
	if err := s.hydratePPTXVideos(httptest.NewRequest("GET", "/", nil), &deck); err == nil {
		t.Fatal("missing video accepted")
	}
	if err := os.WriteFile(filepath.Join(st.Root, "library", asset.File), []byte("corrupt-mp4-data!"), 0600); err != nil {
		t.Fatal(err)
	}
	deck = model()
	if err := s.hydratePPTXVideos(httptest.NewRequest("GET", "/", nil), &deck); err == nil {
		t.Fatal("corrupt video accepted")
	}
	manifest.Videos[0].File = "../private.mp4"
	writeManifest()
	deck = model()
	if err := s.hydratePPTXVideos(httptest.NewRequest("GET", "/", nil), &deck); err == nil {
		t.Fatal("traversal accepted")
	}
	manifest.Videos[0] = asset
	writeManifest()
	os.Remove(filepath.Join(st.Root, "library", asset.File))
	outside := filepath.Join(t.TempDir(), "private.mp4")
	os.WriteFile(outside, content, 0600)
	os.Symlink(outside, filepath.Join(st.Root, "library", asset.File))
	deck = model()
	if err := s.hydratePPTXVideos(httptest.NewRequest("GET", "/", nil), &deck); err == nil {
		t.Fatal("symlink accepted")
	}
}

type plannedVideoRenderer struct{ recordingPowerPointRenderer }

func (f *plannedVideoRenderer) Editable(_ *http.Request, _ string, ids []string) (studio.PPTXDeck, error) {
	deck := studio.PPTXDeck{}
	for _, id := range ids {
		deck.Slides = append(deck.Slides, studio.PPTXSlide{ID: id, Elements: []studio.PPTXElement{{Kind: "video", VideoID: "video-" + id}}})
	}
	return deck, nil
}

func TestPowerPointMediaPlanRequiresPresenterAndUsesSelection(t *testing.T) {
	s := New(testStudio(t), ModeStudio)
	addCacheTestSlide(t, s.St)
	s.PowerPointRenderer = &plannedVideoRenderer{}
	url := "/api/deck/demo/export.pptx?mode=editable&media=plan&slide=0020-b"
	w := httptest.NewRecorder()
	s.Routes().ServeHTTP(w, httptest.NewRequest(http.MethodGet, url, nil))
	if w.Code != http.StatusOK || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("plan response: %d %s", w.Code, w.Body.String())
	}
	var plan struct {
		Videos []string `json:"videos"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &plan); err != nil || len(plan.Videos) != 1 || plan.Videos[0] != "video-0020-b" {
		t.Fatalf("wrong selection: %+v %v", plan, err)
	}
	s.Mode = ModePublic
	w = httptest.NewRecorder()
	s.Routes().ServeHTTP(w, httptest.NewRequest(http.MethodGet, url, nil))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated media plan: %d", w.Code)
	}
}

func TestPowerPointMediaPlanUsesMeasuredSelection(t *testing.T) {
	deck := studio.PPTXDeck{Slides: []studio.PPTXSlide{{Elements: []studio.PPTXElement{{Kind: "video", VideoID: "selected"}, {Kind: "video", VideoID: "selected"}, {Kind: "image", VideoID: "poster-only"}, {Kind: "video"}}}}}
	ids := pptxVideoIDs(deck)
	if len(ids) != 1 || ids[0] != "selected" {
		t.Fatalf("incorrect plan: %v", ids)
	}
	if len(pptxVideoIDs(studio.PPTXDeck{})) != 0 {
		t.Fatal("empty export requested media")
	}
}
