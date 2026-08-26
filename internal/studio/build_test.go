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
	writeFile(t, filepath.Join(root, "studio.yaml"), "theme_default: default\npublic_host: https://talk.example\nfollow_deck: demo\n")
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
		`id="hud"`,                                // HUD bar
		`id="hudmore"`,                            // ⋯ overflow popover
		`id="homebtn"`,                            // presenter return to the deck index
		`app+'/presentations'`,                    // Home control returns to the isolated authenticated catalog
		`window.VSTDEventStream`,                  // authenticated fetch stream replaces cookie-only EventSource
		`id="downloadbtn"`,                        // PDF/PPTX download menu
		`id="sharebtn"`,                           // presenter-only deck sharing
		`data-share="generate"`,                   // expiring share-link dialog
		`/api/events?deck=`,                       // deck-scoped live-follow stream
		`me.presenter===true`,                     // only the presenter publishes positions
		`if(window.__lastPresenterIdx!=null)show`, // late audience joins catch up immediately
		`data-download="pptx"`,                    // visual-exact PowerPoint export
		`data-download="pptx-editable"`,           // explicit best-effort editable fallback
		`document.querySelectorAll('#downloadMenu [data-download^="pptx"]')`, // audience never sees PowerPoint
		`b.title='Download PDF'`,                                 // audience HUD exposes PDF directly
		`data-presenter-control`,                                 // presenter controls fail closed before identity resolves
		`window.VSTDPresenterControl`,                            // all client control paths share the same presenter gate
		`new MutationObserver(lockAudienceHUD)`,                  // dynamically injected HUD controls are also hidden
		`chip.setAttribute('role','status')`,                     // follow state is an indicator, not an audience control
		`document.dispatchEvent(new CustomEvent('vstd:identity'`, // late-created controls sync after auth resolves
		`"follow_url":"https://talk.example/follow"`,             // stable laptop entry is available to the QR overlay
		`id="editRibbon"`,                                        // fixed, shared object-editing ribbon
		`role="toolbar"`,                                         // accessible PowerPoint-style control surface
		`id="videoRibbonTools"`,                                  // media options share the same ribbon
		`id="imageRibbonTools"`,                                  // pictures and CSS background images expose crop controls
		`data-img="crop"`,                                        // crop mode keeps the frame fixed while the image pans
		`backgroundPosition`,                                     // CSS background pictures can be repositioned in their frame
		`backgroundSize`,                                         // CSS background pictures can be zoomed to change the crop
		`id="marquee"`,                                           // dragging blank canvas creates a multi-object selection box
		`selectionBounds`,                                        // multi-selection uses one combined bounding outline
		`deleteSelection`,                                        // Delete and the ribbon remove every selected object together
		`shapeKind`,                                              // CSS fills, gradients, borders, and circles are selectable shapes
		`body.editmode #stage{top:62px}`,                         // ribbon reserves canvas space instead of covering it
		`data-act="sticky"`,                                      // sticky notes
		`data-act="companion"`,                                   // companion drawer
		`data-act="vessica"`,                                     // vessica toggle
		`data-parked`,                                            // hide/park handling in the runtime
		`--vstd-green`,                                           // engine-owned chrome tokens
		`<h1>Hi</h1>`,                                            // slides injected
		`"deck":"demo"`,                                          // runtime meta injected
		`.slide{background:#fff}`,                                // theme.css injected
		`c.removeAttribute('data-vstd')`,                         // engine-only slide id is not persisted
		`name:'open_companion'`,                                  // Vessica can open the narrative editor
		`addEventListener('paste'`,                               // clipboard images can be placed on slides
		`keyTargetIsTextEntry`,                                   // typing surfaces suppress deck hotkeys
		`pad.addEventListener('keydown'`,                         // Sticky keystrokes cannot bubble to the player
		`interactionSurfaceOpen()`,                               // background reloads cannot dismiss Sticky or Companion
		`vstd:interactionend`,                                    // deferred reload resumes only after editing ends
		`[data-chart-group]>.chart-art`,                          // chart geometry yields selection to its movable group
		`const HIGHLIGHT_TITLE`,                                  // Vessica has one explicit title-exclusion boundary
		`chartHighlightTargets()`,                                // accessible chart descriptions and labels become targets
		`img[alt]`,                                               // legacy image charts can contribute alternate text
		`Never highlight the slide title`,                        // the realtime agent receives the same hard boundary
		`highlightables:()=>highlightables()`,                    // browser-level regression tests can inspect target phrases
		`function applyCurrentMonthYear`,                         // declarative cover dates resolve at runtime
		`[data-current-month-year]`,                              // slide-authored dynamic month/year field
		`chipTimer=setTimeout(()=>syncFollowChip(false),3200)`,   // follow intro collapses automatically
		`chip.textContent=announce?'● Following live':'● LIVE'`, // compact persistent live state
		`className='vsound'`,     // video sound control is a durable toggle
		`.vsound{position:fixed`, // control remains usable when the slide is scaled on mobile
		`s.querySelectorAll('video[data-vstd-video]').forEach(soundChip)`, // fullscreen preserves/rebuilds the toggle
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
	if strings.Contains(html, `.vunmute`) {
		t.Error("one-shot unmute control must not replace the persistent sound toggle")
	}
	if strings.Contains(html, `#followchip{position:fixed;bottom:`) {
		t.Error("follow indicator must not overlap the bottom mobile HUD")
	}
	if !strings.Contains(html, `data-act="home" id="homebtn" data-presenter-control style="display:none"`) {
		t.Error("Home control must remain hidden until presenter identity resolves")
	}
	if !strings.Contains(html, `const allowed=['prev','next','download'].includes(control.dataset.act)`) {
		t.Error("audience HUD allowlist must exclude the presenter Home control")
	}
	for _, forbidden := range []string{`tap to browse freely`, `Browsing freely`} {
		if strings.Contains(html, forbidden) {
			t.Errorf("audience follow indicator unexpectedly exposes a control: %q", forbidden)
		}
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
	frag := `<section class="slide"><h1>Hi</h1><div class="footer pgpill dark">7</div></section>`
	if got := ensurePagePill(frag); got != frag {
		t.Fatalf("existing page pill changed:\n%s", got)
	}
}

func TestEnsurePagePillAddsMissingPill(t *testing.T) {
	frag := `<section class="slide"><h1>Hi</h1></section>`
	got := ensurePagePill(frag)
	if !strings.Contains(got, `<div class="pgpill" data-vstd-generated="page-number" aria-label="Slide number"></div>`) || strings.Count(got, `pgpill`) != 1 {
		t.Fatalf("generated page pill missing or duplicated:\n%s", got)
	}
}

var varRe = regexp.MustCompile(`var\(--[a-z-]+`)
