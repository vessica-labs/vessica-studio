package studio

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Build assembles a deck: theme player.html + theme.css + deck.css + slide
// fragments -> decks/<deck>/build/index.html. Returns the output path.
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
	player, err := os.ReadFile(filepath.Join(themeDir, "player.html"))
	if err != nil {
		return "", fmt.Errorf("theme %q: %w (run `vstd init` to scaffold a default theme)", meta.Theme, err)
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
		slides.WriteString(stampFragment(frag, id))
		slides.WriteString("\n")
	}

	rt := map[string]any{
		"deck": deck, "title": meta.Title, "slides": ids, "theme": meta.Theme,
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

var sectionTagRe = regexp.MustCompile(`<section\s`)

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
