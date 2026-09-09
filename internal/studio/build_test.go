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
		`data-download-scope="deck"`,              // existing full-deck behavior remains the default
		`data-download-scope="slide"`,             // current-slide downloads use the same menu
		`params.set('slide',slide)`,               // selected slide reaches every export route
		`/transfer-intent`,                        // player-grid selections cross via a short-lived handoff
		`id="ovTransfer"`,                         // selected slides can be sent from the grid
		`class="linkbar"`,                         // presenters can inspect linked snapshots
		`data-link-action="detach"`,               // linked snapshots can be made editable
		`document.querySelectorAll('#downloadMenu [data-download^="pptx"]')`, // audience never sees PowerPoint
		`b.title='Download PDF'`,                                 // audience HUD exposes PDF directly
		`data-presenter-control`,                                 // presenter controls fail closed before identity resolves
		`window.VSTDPresenterControl`,                            // all client control paths share the same presenter gate
		`/api/observability/view`,                                // audience and team slide views feed the owner dashboard asynchronously
		`/api/observability/openai-usage`,                        // authenticated Realtime usage is reported without exposing the API key
		`/api/realtime/end`,                                      // hosted billing can settle a bounded voice reservation promptly
		`Promise.allSettled([...openAIUsageReports])`,            // final usage receipts drain before hosted settlement
		`problem.message`,                                        // hosted entitlement/provider errors replace misleading local-key advice
		`keepalive:true`,                                         // telemetry never blocks navigation or unload
		`new MutationObserver(lockAudienceHUD)`,                  // dynamically injected HUD controls are also hidden
		`chip.setAttribute('role','status')`,                     // follow state is an indicator, not an audience control
		`document.dispatchEvent(new CustomEvent('vstd:identity'`, // late-created controls sync after auth resolves
		`"follow_url":"https://talk.example/follow"`,             // stable laptop entry is available to the QR overlay
		`id="editRibbon"`,                                        // fixed, shared editing ribbon (primary controls while editing)
		`role="toolbar"`,                                         // accessible PowerPoint-style control surface
		`id="objectBar"`,                                         // object-specific controls float in under the ribbon on selection
		`body.editmode.hassel #objectBar`,                        // the object bar only shows while something is selected
		`id="filmstrip"`,                                         // slide thumbnails dock left while editing
		`id="saveStatus"`,                                        // save state is an indicator in the ribbon, not a button label
		`data-act="addtext"`,                                     // the ribbon can insert a new editable text box
		`data-shape="rect"`,                                      // the ribbon exposes new shape choices
		`data-shape="circle"`,                                    // circle insertion is available without authored HTML
		`data-shape="line"`,                                      // line insertion is available without authored HTML
		`id="vtip"`,                                              // tooltips carry each control's keyboard shortcut
		`data-key="G"`,                                           // shortcut hints are data, rendered by the tooltip
		`function hudAction`,                                     // HUD, ribbon, and filmstrip share one action router
		`id="videoRibbonTools"`,                                  // media options share the same object bar
		`id="imageRibbonTools"`,                                  // pictures and CSS background images expose crop controls
		`data-img="crop"`,                                        // crop mode keeps the frame fixed while the image pans
		`backgroundPosition`,                                     // CSS background pictures can be repositioned in their frame
		`backgroundSize`,                                         // CSS background pictures can be zoomed to change the crop
		`id="marquee"`,                                           // dragging blank canvas creates a multi-object selection box
		`selectionBounds`,                                        // multi-selection uses one combined bounding outline
		`deleteSelection`,                                        // Delete and the ribbon remove every selected object together
		`duplicateSelection`,                                     // ⌘D duplicates the selection in place
		`function doRedo`,                                        // ⌘⇧Z redoes the last undone change
		`shapeKind`,                                              // CSS fills, gradients, borders, and circles are selectable shapes
		`body.editmode #stage{top:48px;left:200px`,               // ribbon and filmstrip reserve canvas space instead of covering it
		`body.editmode #hud,body.editmode #progress{display:none}`, // the bottom HUD yields to the ribbon while editing
		`--vstd-rail:`,                                                    // chrome tokens mirror the Studio Cloud shell
		`data-act="sticky"`,                                               // sticky notes
		`data-act="companion"`,                                            // companion drawer
		`data-act="vessica"`,                                              // vessica toggle
		`data-parked`,                                                     // hide/park handling in the runtime
		`--vstd-green`,                                                    // engine-owned chrome tokens
		`<h1>Hi</h1>`,                                                     // slides injected
		`"deck":"demo"`,                                                   // runtime meta injected
		`.slide{background:#fff}`,                                         // theme.css injected
		`id="vstd-presentation-styles"`,                                   // live refresh can replace theme.css + deck.css atomically with slide markup
		`syncPresentationStyles(doc)`,                                     // external revisions update presentation layout without a page reload
		`c.removeAttribute('data-vstd')`,                                  // engine-only slide id is not persisted
		`name:'open_companion'`,                                           // Vessica can open the narrative editor
		`addEventListener('paste'`,                                        // clipboard images can be placed on slides
		`keyTargetIsTextEntry`,                                            // typing surfaces suppress deck hotkeys
		`pad.addEventListener('keydown'`,                                  // Sticky keystrokes cannot bubble to the player
		`interactionSurfaceOpen()`,                                        // background reloads cannot dismiss Sticky or Companion
		`scheduleAutosave()`,                                              // dirty presentation edits save automatically
		`addEventListener('pagehide',flushAutosave`,                       // navigating away flushes pending edits with keepalive
		`if(selfMutations||Date.now()-(window.__selfSave`,                 // in-flight editor writes cannot trigger their own reload
		`body.editmode #vstatus{top:60px}`,                                // Vessica status stays below the editing ribbon
		`body.gridmode #filmstrip{display:none!important}`,                // Grid fully hides the slide filmstrip
		`const label=s.dataset.menu||s.dataset.sec||heading`,              // Agenda falls back to every slide's visible title
		`vtip.classList.toggle('above',above)`,                            // bottom controls place tooltips above the viewport edge
		`vstd:interactionend`,                                             // deferred reload resumes only after editing ends
		`[data-chart-group]>.chart-art`,                                   // chart geometry yields selection to its movable group
		`const HIGHLIGHT_TITLE`,                                           // Vessica has one explicit title-exclusion boundary
		`chartHighlightTargets()`,                                         // accessible chart descriptions and labels become targets
		`img[alt]`,                                                        // legacy image charts can contribute alternate text
		`Never highlight the slide title`,                                 // the realtime agent receives the same hard boundary
		`highlightables:()=>highlightables()`,                             // browser-level regression tests can inspect target phrases
		`function applyCurrentMonthYear`,                                  // declarative cover dates resolve at runtime
		`[data-current-month-year]`,                                       // slide-authored dynamic month/year field
		`chipTimer=setTimeout(()=>syncFollowChip(false),3200)`,            // follow intro collapses automatically
		`chip.textContent=announce?'● Following live':'● LIVE'`,           // compact persistent live state
		`className='vsound'`,                                              // video sound control is a durable toggle
		`.vsound{position:fixed`,                                          // control remains usable when the slide is scaled on mobile
		`s.querySelectorAll('video[data-vstd-video]').forEach(soundChip)`, // fullscreen preserves/rebuilds the toggle
		`if(isAudience()&&!v.hasAttribute('data-autoplay'))`,              // manual audience videos remain explicit tap-to-stream
		`clearOverlays();
      activate();`, // async audience identity re-applies autoplay to the active slide
		`window.__vhydrateAssets=hydrate`,                                      // slide images are admitted in navigation order
		`.slide{display:none;font-family:var(--sans,var(--vstd-sans))}`,        // slide content inherits the theme typeface, not UI chrome
		`Object.prototype.hasOwnProperty.call(window.VSTD||{},'audience_url')`, // hosted release mode is available even when its static CSP blocks /api/me
	} {
		if !strings.Contains(html, want) {
			t.Errorf("built deck missing %q", want)
		}
	}
	for _, removed := range []string{`id="detailsbtn"`, `vstd:details`} {
		if strings.Contains(html, removed) {
			t.Errorf("built player retains removed Details control %q", removed)
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
	if strings.Contains(html, "Deck changed on disk") {
		t.Error("ordinary editing must not instruct people to reload after a save")
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
	// Chrome must not depend on theme-overridable tokens. The slide canvas itself
	// intentionally inherits --sans; all other player tokens are engine-owned.
	chrome := html[:strings.Index(html, "/* deck overrides */")]
	themeStart := strings.Index(chrome, ":root{--sans")
	if themeStart >= 0 {
		chrome = chrome[:themeStart]
	}
	for _, m := range varRe.FindAllString(chrome, -1) {
		if m != "var(--ts" && m != "var(--sans" && !strings.HasPrefix(m, "var(--vstd-") {
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

func TestPrioritizeSlideAssets(t *testing.T) {
	first := prioritizeSlideAssets(`<section class="slide"><img src="/library/cover.png"><picture><source srcset="/library/cover.webp"><img src="/library/fallback.png"></picture></section>`, true)
	if strings.Contains(first, "data-vstd-src") || strings.Count(first, `loading="eager"`) != 2 || strings.Count(first, `fetchpriority="high"`) != 2 {
		t.Fatalf("first slide assets were not prioritized:\n%s", first)
	}
	later := prioritizeSlideAssets(`<section class="slide"><img src="/library/later.png" srcset="/library/later-2x.png 2x"><source srcset="/library/later.webp"></section>`, false)
	for _, want := range []string{`data-vstd-src="/library/later.png"`, `data-vstd-srcset="/library/later-2x.png 2x"`, `data-vstd-srcset="/library/later.webp"`, `loading="lazy"`, `fetchpriority="low"`} {
		if !strings.Contains(later, want) {
			t.Fatalf("later slide missing %q:\n%s", want, later)
		}
	}
}

func TestEnsureAudienceShareAddsOnlyMissingTitleControl(t *testing.T) {
	frag := `<section class="slide"><h1>Title</h1></section>`
	got := ensureAudienceShare(frag)
	if !strings.Contains(got, `data-vstd-generated="audience-share"`) || !strings.Contains(got, `data-vstd-audience-qr`) {
		t.Fatalf("generated audience share block missing:\n%s", got)
	}
	existing := `<section class="slide"><img data-vstd-audience-qr></section>`
	if ensureAudienceShare(existing) != existing {
		t.Fatal("existing audience share element was duplicated")
	}
}

func TestHostedBuildAddsAndRemovesGeneratedTitleShare(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "studio.yaml"), "theme_default: default\n")
	writeFile(t, filepath.Join(root, "themes", "default", "theme.css"), ".slide{}")
	writeFile(t, filepath.Join(root, "decks", "demo", "deck.yaml"), "title: Demo\ntheme: default\n")
	writeFile(t, filepath.Join(root, "decks", "demo", "slides", "0010-a.html"), `<section class="slide"><h1>Title</h1></section>`)
	studio, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	url := "https://studio.example/s/123-456"
	withQR, err := studio.BuildWithAudienceURL("demo", &url)
	if err != nil {
		t.Fatal(err)
	}
	content, _ := os.ReadFile(withQR)
	if !strings.Contains(string(content), `data-vstd-generated="audience-share"`) {
		t.Fatal("checked audience sharing did not add the title control")
	}
	empty := ""
	withoutQR, err := studio.BuildWithAudienceURL("demo", &empty)
	if err != nil {
		t.Fatal(err)
	}
	content, _ = os.ReadFile(withoutQR)
	if strings.Contains(string(content), `data-vstd-generated="audience-share"`) {
		t.Fatal("unchecked audience sharing retained the generated title control")
	}
}

var varRe = regexp.MustCompile(`var\(--[a-z-]+`)
