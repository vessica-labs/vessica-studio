package chart

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vessica-labs/vessica-studio/internal/studio"
)

func writeTestFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func testStudio(t *testing.T) (*studio.Studio, string) {
	t.Helper()
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "studio.yaml"), "theme_default: default\n")
	writeTestFile(t, filepath.Join(root, "themes/default/theme.css"), ".slide{width:1280px;height:720px}")
	writeTestFile(t, filepath.Join(root, "decks/demo/deck.yaml"), "title: Demo\ntheme: default\n")
	writeTestFile(t, filepath.Join(root, "decks/demo/slides/0010-chart.html"), `<section class="slide"><svg viewBox="0 0 100 100"><text x="10" y="20">Old</text></svg></section>`)
	writeTestFile(t, filepath.Join(root, "decks/demo/slides/0010-chart.md"), "## Log\n")
	st, err := studio.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	return st, root
}

func stubBrowserResult(t *testing.T, result Result) {
	t.Helper()
	b, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	previous := browserEvaluate
	browserEvaluate = func(context.Context, string, string, string) (string, error) { return string(b), nil }
	t.Cleanup(func() { browserEvaluate = previous })
}

func TestPromoteSVGTextDryRunThenWrite(t *testing.T) {
	st, _ := testStudio(t)
	fragment := `<section class="slide"><div data-chart-group data-edit><svg class="chart-art"></svg><div class="chart-label" data-chart-label data-edit>Old</div></div></section>`
	stubBrowserResult(t, Result{Promoted: 1, Charts: 1, Fragment: fragment})

	got, err := PromoteSVGText(context.Background(), st, "demo", "0010-chart", Options{Browser: "test-browser", DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if got.Promoted != 1 || got.Charts != 1 {
		t.Fatalf("result = %#v", got)
	}
	original, _, _ := st.ReadSlide("demo", "0010-chart")
	if !strings.Contains(original, "<text") {
		t.Fatal("dry run changed the fragment")
	}
	if _, err := PromoteSVGText(context.Background(), st, "demo", "0010-chart", Options{Browser: "test-browser"}); err != nil {
		t.Fatal(err)
	}
	written, _, _ := st.ReadSlide("demo", "0010-chart")
	if strings.Contains(written, "<text") || !strings.Contains(written, `data-chart-label`) {
		t.Fatalf("fragment was not promoted:\n%s", written)
	}
}

func TestPromoteSVGTextRefusesPartialMigration(t *testing.T) {
	st, _ := testStudio(t)
	stubBrowserResult(t, Result{Promoted: 1, Charts: 1, Skipped: []string{"textPath: curved label"}, Fragment: `<section class="slide"></section>`})
	_, err := PromoteSVGText(context.Background(), st, "demo", "0010-chart", Options{Browser: "test-browser"})
	if err == nil || !strings.Contains(err.Error(), "skipped 1 unsupported") {
		t.Fatalf("error = %v", err)
	}
	original, _, _ := st.ReadSlide("demo", "0010-chart")
	if !strings.Contains(original, "<text") {
		t.Fatal("partial migration changed the fragment")
	}
}
