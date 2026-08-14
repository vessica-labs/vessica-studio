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
		`id="downloadbtn"`,               // PDF/PPTX download menu
		`id="sharebtn"`,                  // presenter-only deck sharing
		`data-share="generate"`,          // expiring share-link dialog
		`data-download="pptx"`,           // editable PowerPoint export
		`id="editRibbon"`,                // fixed, shared object-editing ribbon
		`role="toolbar"`,                 // accessible PowerPoint-style control surface
		`id="videoRibbonTools"`,          // media options share the same ribbon
		`body.editmode #stage{top:62px}`, // ribbon reserves canvas space instead of covering it
		`data-act="sticky"`,              // sticky notes
		`data-act="companion"`,           // companion drawer
		`data-act="vessica"`,             // vessica toggle
		`data-parked`,                    // hide/park handling in the runtime
		`--vstd-green`,                   // engine-owned chrome tokens
		`<h1>Hi</h1>`,                    // slides injected
		`"deck":"demo"`,                  // runtime meta injected
		`.slide{background:#fff}`,        // theme.css injected
		`c.removeAttribute('data-vstd')`, // engine-only slide id is not persisted
		`name:'open_companion'`,          // Vessica can open the narrative editor
		`addEventListener('paste'`,       // clipboard images can be placed on slides
		`keyTargetIsTextEntry`,           // typing surfaces suppress deck hotkeys
		`pad.addEventListener('keydown'`, // Sticky keystrokes cannot bubble to the player
	} {
		if !strings.Contains(html, want) {
			t.Errorf("built deck missing %q", want)
		}
	}
	if strings.Contains(html, "<!--VSTD:") {
		t.Error("unsubstituted VSTD marker left in output")
	}
	if strings.Contains(html, `#selbox .etools`) {
		t.Error("object tools must live in the top ribbon, not on the selection box")
	}
	if strings.Contains(html, `id="vinspect"`) {
		t.Error("video controls must share the top ribbon, not use a floating inspector")
	}
	if got := strings.Count(html, `aria-label="Slide number"`); got != 1 {
		t.Fatalf("built slide has %d page-number pills, want 1", got)
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

func TestEnsurePagePillPreservesExistingPill(t *testing.T) {
	frag := `<section class="slide"><div class="pgpill custom">9</div></section>`
	if got := ensurePagePill(frag); got != frag {
		t.Fatalf("existing page pill changed:\n%s", got)
	}
}

func TestEnsurePagePillAddsMissingPill(t *testing.T) {
	frag := `<section class="slide"><h1>Title</h1></section>`
	got := ensurePagePill(frag)
	if !strings.Contains(got, `data-vstd-generated="page-number"`) || strings.Count(got, `pgpill`) != 1 {
		t.Fatalf("generated page pill missing or duplicated:\n%s", got)
	}
}

var varRe = regexp.MustCompile(`var\(--[a-z-]+`)
