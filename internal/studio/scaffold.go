package studio

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

//go:embed templates
var templates embed.FS

// Init scaffolds a new studio root: studio.yaml, themes/default (embedded),
// empty decks/, library/, requests/. Existing files are never overwritten.
func Init(root string) error {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}
	dirs := []string{"decks", "library/img", "requests", "themes/default"}
	for _, d := range dirs {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			return err
		}
	}
	writeIfAbsent(filepath.Join(root, "studio.yaml"), []byte(defaultStudioYAML))
	writeIfAbsent(filepath.Join(root, "library", "manifest.json"), []byte(defaultManifest))
	writeIfAbsent(filepath.Join(root, ".gitignore"), []byte("decks/*/build/\nrequests/done/\n.DS_Store\n"))

	// embedded default theme
	return fs.WalkDir(templates, "templates/default-theme", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel := strings.TrimPrefix(p, "templates/default-theme/")
		b, err := templates.ReadFile(p)
		if err != nil {
			return err
		}
		return writeIfAbsent(filepath.Join(root, "themes", "default", rel), b)
	})
}

// NewDeck scaffolds decks/<name> with a starter slide + companion.
func (s *Studio) NewDeck(name, title string) error {
	if !ValidDeckName(name) {
		return fmt.Errorf("invalid deck name %q (lowercase, digits, hyphens)", name)
	}
	dir := s.DeckDir(name)
	if _, err := os.Stat(dir); err == nil {
		return fmt.Errorf("deck %q already exists", name)
	}
	if err := os.MkdirAll(filepath.Join(dir, "slides"), 0o755); err != nil {
		return err
	}
	if title == "" {
		title = name
	}
	meta := &DeckMeta{
		Title:      title,
		Theme:      s.Config.ThemeDefault,
		Visibility: "private",
		Created:    time.Now().Format("2006-01-02"),
	}
	if err := s.SaveDeckMeta(name, meta); err != nil {
		return err
	}
	os.WriteFile(filepath.Join(dir, "deck.css"), []byte("/* deck-specific overrides */\n"), 0o644)

	cover, _ := templates.ReadFile("templates/starter/0010-cover.html")
	coverMD, _ := templates.ReadFile("templates/starter/0010-cover.md")
	coverS := strings.ReplaceAll(string(cover), "{{TITLE}}", title)
	os.WriteFile(filepath.Join(dir, "slides", "0010-cover.html"), []byte(coverS), 0o644)
	os.WriteFile(filepath.Join(dir, "slides", "0010-cover.md"),
		[]byte(strings.ReplaceAll(string(coverMD), "{{TITLE}}", title)), 0o644)
	return nil
}

// NewSlide creates an empty slide + companion after the given position.
func (s *Studio) NewSlide(deck, id, title, layoutHTML string) error {
	if !ValidDeckName(deck) || !ValidSlideID(id) {
		return fmt.Errorf("invalid deck/slide id")
	}
	hp := s.SlidePath(deck, id, ".html")
	if _, err := os.Stat(hp); err == nil {
		return fmt.Errorf("slide %q already exists", id)
	}
	if layoutHTML == "" {
		layoutHTML = fmt.Sprintf(`<section class="slide" data-sec=%q>
  <div class="s-title">%s</div>
  <div class="s-lead">Lead-in line</div>
  <div class="content"></div>
  <div class="pgpill"></div>
  <aside class="notes">Talk track for this slide.</aside>
</section>`, title, title)
	}
	if err := os.WriteFile(hp, []byte(layoutHTML+"\n"), 0o644); err != nil {
		return err
	}
	md := fmt.Sprintf(`---
slide: %s
status: draft
visuals: []
layout: light-content
---
## Intent
%s

## Key ideas
-

## Evidence & sources
-

## Talk track
-

## Visual direction
-

## Log
- %s created via edit API
`, id, title, time.Now().Format("2006-01-02"))
	return os.WriteFile(s.SlidePath(deck, id, ".md"), []byte(md), 0o644)
}

func writeIfAbsent(p string, b []byte) error {
	if _, err := os.Stat(p); err == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, b, 0o644)
}

const defaultStudioYAML = `theme_default: default
port: 4400
openai:
  image_model: gpt-image-2
  realtime_model: gpt-realtime-2
  base_url: https://api.openai.com/v1
`

const defaultManifest = `{
  "version": 1,
  "styleFamilies": {},
  "assets": []
}
`
