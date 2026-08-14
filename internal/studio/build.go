package studio

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

// Build assembles a deck: the engine's embedded player + theme.css +
// deck.css + slide fragments -> decks/<deck>/build/index.html. Returns the
// output path.
//
// The player (HUD markup, chrome styling, and all runtime JS) is the
// engine-owned control plane, identical for every theme — themes contribute
// presentation only (theme.css + deck.css). A themes/<theme>/player.html on
// disk is ignored with a warning; per-theme players are how the HUD used to
// drift.
//
// Player template markers:
//
//	<!--VSTD:TITLE-->   deck title
//	<!--VSTD:THEME-->   <style> theme.css + deck.css </style>
//	<!--VSTD:SLIDES-->  concatenated fragments (each stamped data-vstd="id")
//	/*VSTD:META*/null/*:VSTD*/  runtime metadata JSON
func (s *Studio) Build(deck string) (string, error) {
	meta, err := s.LoadDeckMeta(deck)
	if err != nil {
		return "", err
	}
	themeDir := s.ThemeDir(meta.Theme)
	player, err := templates.ReadFile("templates/player.html")
	if err != nil {
		return "", fmt.Errorf("embedded player: %w", err)
	}
	if _, err := os.Stat(filepath.Join(themeDir, "player.html")); err == nil {
		warnThemePlayerOnce(meta.Theme)
	}
	themeCSS, err := os.ReadFile(filepath.Join(themeDir, "theme.css"))
	if err != nil {
		return "", fmt.Errorf("theme %q: %w", meta.Theme, err)
	}
	deckCSS, _ := os.ReadFile(filepath.Join(s.DeckDir(deck), "deck.css"))

	ids, err := s.SlideIDs(deck)
	if err != nil {
		return "", err
	}
	if len(ids) == 0 {
		return "", fmt.Errorf("deck %q has no slides", deck)
	}
	var slides strings.Builder
	for _, id := range ids {
		frag, _, err := s.ReadSlide(deck, id)
		if err != nil {
			return "", err
		}
		slides.WriteString(stampFragment(ensurePagePill(frag), id))
		slides.WriteString("\n")
	}

	hashes, _ := s.HashSlides(deck)
	rt := map[string]any{
		"deck": deck, "title": meta.Title, "slides": ids, "theme": meta.Theme,
		// per-slide fragment hashes at build time — the player sends these
		// back as X-VSTD-Base-Hash on fragment PUTs so a stale tab can never
		// silently overwrite work that landed on disk after it loaded
		"hashes": hashes,
		"realtime": map[string]string{
			"model": s.Config.OpenAI.RealtimeModel,
			"base":  s.Config.OpenAI.BaseURL,
		},
	}
	rtJSON, _ := json.Marshal(rt)

	out := string(player)
	out = strings.ReplaceAll(out, "<!--VSTD:TITLE-->", htmlEscape(meta.Title))
	out = strings.ReplaceAll(out, "<!--VSTD:THEME-->",
		"<style>\n"+string(themeCSS)+"\n/* deck overrides */\n"+string(deckCSS)+"\n</style>")
	out = strings.Replace(out, "<!--VSTD:SLIDES-->", slides.String(), 1)
	out = strings.Replace(out, "/*VSTD:META*/null/*:VSTD*/", string(rtJSON), 1)

	buildDir := filepath.Join(s.DeckDir(deck), "build")
	if err := os.MkdirAll(buildDir, 0o755); err != nil {
		return "", err
	}
	outPath := filepath.Join(buildDir, "index.html")
	if err := os.WriteFile(outPath, []byte(out), 0o644); err != nil {
		return "", err
	}
	return outPath, nil
}

// warnThemePlayerOnce logs the ignored-player deprecation once per theme —
// Build runs on every deck GET, so an unconditional log would flood serve
// output.
var themePlayerWarned sync.Map

func warnThemePlayerOnce(theme string) {
	if _, seen := themePlayerWarned.LoadOrStore(theme, true); !seen {
		log.Printf("theme %q: player.html is ignored — the player is engine-owned now; theme customization lives in theme.css (delete the file to silence this)", theme)
	}
}

var sectionTagRe = regexp.MustCompile(`<section\s`)
var pagePillClassRe = regexp.MustCompile(`class\s*=\s*["'][^"']*\bpgpill\b[^"']*["']`)

// ensurePagePill makes slide numbering an engine guarantee instead of
// requiring every author or redesign agent to remember footer boilerplate.
// Existing pills are preserved so theme-specific markup remains authoritative.
func ensurePagePill(frag string) string {
	if pagePillClassRe.MatchString(frag) {
		return frag
	}
	i := strings.LastIndex(strings.ToLower(frag), "</section>")
	if i < 0 {
		return frag
	}
	prefix := ""
	if i > 0 && frag[i-1] != '\n' {
		prefix = "\n"
	}
	return frag[:i] + prefix + `  <div class="pgpill" data-vstd-generated="page-number" aria-label="Slide number"></div>` + "\n" + frag[i:]
}

// stampFragment injects data-vstd="<id>" into the first <section> tag so the
// player can map DOM sections back to fragment files for save-back.
func stampFragment(frag, id string) string {
	if strings.Contains(frag, `data-vstd="`) {
		return regexp.MustCompile(`data-vstd="[^"]*"`).ReplaceAllString(frag, `data-vstd="`+id+`"`)
	}
	return sectionTagRe.ReplaceAllString(frag, `<section data-vstd="`+id+`" `)
}

func htmlEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
	return r.Replace(s)
}
