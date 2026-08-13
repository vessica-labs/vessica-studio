package studio

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// A theme is theme.css (+ optional deck.css) only — the player is the
// engine's embedded control plane, identical for every theme.
func TestBuildUsesEmbeddedPlayer(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "studio.yaml"), "theme_default: default\n")
	writeFile(t, filepath.Join(root, "themes", "default", "theme.css"),
		":root{--sans:sans-serif}.slide{background:#fff}")
	writeFile(t, filepath.Join(root, "decks", "demo", "deck.yaml"),
		"title: Demo\ntheme: default\n")
	writeFile(t, filepath.Join(root, "decks", "demo", "slides", "0010-a.html"),
		`<section class="slide"><h1>Hi</h1></section>`)

	st, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	out, err := st.Build("demo")
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	html := string(b)

	// the full engine control plane must be present
	for _, want := range []string{
		`id="hud"`,                       // HUD bar
		`id="hudmore"`,                   // ⋯ overflow popover
		`id="pdfbtn"`,                    // PDF export button
		`data-act="sticky"`,              // sticky notes
		`data-act="vessica"`,             // vessica toggle
		`data-parked`,                    // hide/park handling in the runtime
		`--vstd-green`,                   // engine-owned chrome tokens
		`<h1>Hi</h1>`,                    // slides injected
		`"deck":"demo"`,                  // runtime meta injected
		`.slide{background:#fff}`,        // theme.css injected
		`c.removeAttribute('data-vstd')`, // engine-only slide id is not persisted
	} {
		if !strings.Contains(html, want) {
			t.Errorf("built deck missing %q", want)
		}
	}
	if strings.Contains(html, "<!--VSTD:") {
		t.Error("unsubstituted VSTD marker left in output")
	}
	// chrome must not depend on theme-overridable tokens: every var(--x)
	// outside the injected theme/deck CSS block is engine-owned (--vstd-*)
	// except the runtime-set --ts thumbnail scale
	chrome := html[:strings.Index(html, "/* deck overrides */")]
	themeStart := strings.Index(chrome, ":root{--sans")
	if themeStart >= 0 {
		chrome = chrome[:themeStart]
	}
	for _, m := range varRe.FindAllString(chrome, -1) {
		if m != "var(--ts" && !strings.HasPrefix(m, "var(--vstd-") {
			t.Errorf("chrome uses theme-overridable token %s", m)
		}
	}
}

var varRe = regexp.MustCompile(`var\(--[a-z-]+`)
