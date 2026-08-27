package studio

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSlideParked(t *testing.T) {
	cases := []struct {
		frag string
		want bool
	}{
		{`<section class="slide" data-sec="A">x</section>`, false},
		{`<section class="slide" data-hidden="1">x</section>`, false},
		{`<section data-vstd="0010-a" class="slide" data-parked="1">x</section>`, true},
		{`<section class="slide">body mentions data-parked only in text</section>`, false},
	}
	for _, c := range cases {
		if got := SlideParked(c.frag); got != c.want {
			t.Errorf("SlideParked(%q) = %v, want %v", c.frag, got, c.want)
		}
	}
}

func TestBuildPrintHTML(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "studio.yaml"), "theme_default: default\n")
	writeFile(t, filepath.Join(root, "themes", "default", "theme.css"),
		".slide{display:none}.slide.active{display:block}")
	writeFile(t, filepath.Join(root, "decks", "demo", "deck.yaml"),
		"title: Demo Deck\ntheme: default\n")
	writeFile(t, filepath.Join(root, "decks", "demo", "slides", "0010-active.html"),
		`<section class="slide" data-sec="A"><h1>Active</h1></section>`)
	writeFile(t, filepath.Join(root, "decks", "demo", "slides", "0020-hidden.html"),
		`<section class="slide" data-hidden="1"><h1>Hidden</h1><video data-vstd-video="clip-1"></video></section>`)
	writeFile(t, filepath.Join(root, "decks", "demo", "slides", "0030-parked.html"),
		`<section class="slide" data-parked="1"><h1>Parked</h1></section>`)

	st, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	html, pages, err := st.BuildPrintHTML("demo")
	if err != nil {
		t.Fatal(err)
	}
	if pages != 2 {
		t.Errorf("pages = %d, want 2 (active + hidden, parked excluded)", pages)
	}
	if !strings.Contains(html, "<h1>Active</h1>") || !strings.Contains(html, "<h1>Hidden</h1>") {
		t.Error("active/hidden slides missing from print HTML")
	}
	if !strings.Contains(html, `poster="/assets/video/clip-1/poster"`) {
		t.Error("print HTML did not add the runtime-equivalent video poster")
	}
	if strings.Contains(html, "<h1>Parked</h1>") {
		t.Error("parked slide leaked into print HTML")
	}
	if got := strings.Count(html, `class="vstd-page"`); got != 2 {
		t.Errorf("page wrappers = %d, want 2", got)
	}
	for _, want := range []string{`id="vstd-page-1"`, `id="vstd-page-2"`} {
		if !strings.Contains(html, want) {
			t.Errorf("print HTML missing stable page anchor %q", want)
		}
	}
	// each exported slide must carry the active class so theme visibility
	// rules keyed on .slide.active apply without player JS
	if got := strings.Count(html, `class="active slide"`); got != 2 {
		t.Errorf("active-marked slides = %d, want 2", got)
	}
	if !strings.Contains(html, "@page{size:1280px 720px;margin:0}") {
		t.Error("print page geometry missing")
	}
	if !strings.Contains(html, "<title>Demo Deck</title>") {
		t.Error("deck title missing")
	}

	subset, ids, err := st.BuildPrintHTMLForSlides("demo", []string{"0020-hidden"})
	if err != nil || len(ids) != 1 || ids[0] != "0020-hidden" || strings.Contains(subset, "<h1>Active</h1>") || !strings.Contains(subset, "<h1>Hidden</h1>") {
		t.Fatalf("single-slide export ids=%v err=%v html=%s", ids, err, subset)
	}
	if _, _, err := st.BuildPrintHTMLForSlides("demo", []string{"0030-parked"}); err == nil || !strings.Contains(err.Error(), "parked") {
		t.Fatalf("parked slide export error = %v", err)
	}
	if _, _, err := st.BuildPrintHTMLForSlides("demo", []string{"9999-missing"}); err == nil {
		t.Fatal("missing slide unexpectedly exported")
	}
}

func TestBuildPrintHTMLAllParked(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "studio.yaml"), "theme_default: default\n")
	writeFile(t, filepath.Join(root, "themes", "default", "theme.css"), ".slide{}")
	writeFile(t, filepath.Join(root, "decks", "demo", "deck.yaml"), "title: D\ntheme: default\n")
	writeFile(t, filepath.Join(root, "decks", "demo", "slides", "0010-a.html"),
		`<section class="slide" data-parked="1">x</section>`)
	st, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.BuildPrintHTML("demo"); err == nil {
		t.Error("expected error for deck with no exportable slides")
	}
}
