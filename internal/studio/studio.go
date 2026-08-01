// Package studio implements the Vessica Studio content model: a root folder
// containing themes/, library/, requests/ and decks/, where each deck is a
// directory of slide fragments (NNNN-slug.html) paired with companion
// markdown files (NNNN-slug.md) carrying the research, evidence and talk
// track behind the slide.
package studio

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is studio.yaml at the root of a content repo.
type Config struct {
	ThemeDefault string `yaml:"theme_default"`
	Port         int    `yaml:"port"`
	OpenAI       struct {
		BaseURL           string `yaml:"base_url"`
		APIKeyCmd         string `yaml:"api_key_cmd"`
		ImageModel        string `yaml:"image_model"`
		RealtimeModel     string `yaml:"realtime_model"`
		RealtimeTokenPath string `yaml:"realtime_token_path"`
	} `yaml:"openai"`
}

// DeckMeta is deck.yaml inside a deck directory.
type DeckMeta struct {
	Title        string            `yaml:"title"`
	Theme        string            `yaml:"theme"`
	Visibility   string            `yaml:"visibility"`
	Created      string            `yaml:"created"`
	Description  string            `yaml:"description,omitempty"`
	ForkedFrom   string            `yaml:"forked_from,omitempty"`
	ForkDate     string            `yaml:"fork_date,omitempty"`
	ParentHashes map[string]string `yaml:"parent_hashes,omitempty"`
}

// Studio is an opened content root.
type Studio struct {
	Root   string
	Config Config
}

func Open(root string) (*Studio, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	s := &Studio{Root: abs}
	s.Config.ThemeDefault = "default"
	s.Config.Port = 4400
	s.Config.OpenAI.BaseURL = "https://api.openai.com/v1"
	s.Config.OpenAI.ImageModel = "gpt-image-2"
	s.Config.OpenAI.RealtimeModel = "gpt-realtime-2"
	s.Config.OpenAI.RealtimeTokenPath = "/realtime/client_secrets"
	b, err := os.ReadFile(filepath.Join(abs, "studio.yaml"))
	if err != nil {
		return nil, fmt.Errorf("not a studio root (missing studio.yaml): %s", abs)
	}
	if err := yaml.Unmarshal(b, &s.Config); err != nil {
		return nil, fmt.Errorf("studio.yaml: %w", err)
	}
	if v := os.Getenv("PORT"); v != "" {
		fmt.Sscanf(v, "%d", &s.Config.Port)
	}
	return s, nil
}

func (s *Studio) DecksDir() string { return filepath.Join(s.Root, "decks") }
func (s *Studio) ThemeDir(name string) string {
	return filepath.Join(s.Root, "themes", name)
}
func (s *Studio) DeckDir(deck string) string { return filepath.Join(s.DecksDir(), deck) }

var deckNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// ValidDeckName guards against path traversal in API-supplied names.
func ValidDeckName(n string) bool { return deckNameRe.MatchString(n) }

var slideIDRe = regexp.MustCompile(`^[0-9]{2,5}-[a-z0-9-]+$`)

func ValidSlideID(id string) bool { return slideIDRe.MatchString(id) }

// ListDecks returns deck directory names, sorted.
func (s *Studio) ListDecks() ([]string, error) {
	ents, err := os.ReadDir(s.DecksDir())
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range ents {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
			if _, err := os.Stat(filepath.Join(s.DecksDir(), e.Name(), "deck.yaml")); err == nil {
				out = append(out, e.Name())
			}
		}
	}
	sort.Strings(out)
	return out, nil
}

func (s *Studio) LoadDeckMeta(deck string) (*DeckMeta, error) {
	if !ValidDeckName(deck) {
		return nil, fmt.Errorf("invalid deck name %q", deck)
	}
	b, err := os.ReadFile(filepath.Join(s.DeckDir(deck), "deck.yaml"))
	if err != nil {
		return nil, err
	}
	m := &DeckMeta{}
	if err := yaml.Unmarshal(b, m); err != nil {
		return nil, err
	}
	if m.Theme == "" {
		m.Theme = s.Config.ThemeDefault
	}
	if m.Visibility == "" {
		m.Visibility = "private"
	}
	return m, nil
}

func (s *Studio) SaveDeckMeta(deck string, m *DeckMeta) error {
	b, err := yaml.Marshal(m)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(s.DeckDir(deck), "deck.yaml"), b, 0o644)
}

// SlideIDs lists slide ids (filename without .html) in order.
func (s *Studio) SlideIDs(deck string) ([]string, error) {
	ents, err := os.ReadDir(filepath.Join(s.DeckDir(deck), "slides"))
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range ents {
		n := e.Name()
		if strings.HasSuffix(n, ".html") {
			out = append(out, strings.TrimSuffix(n, ".html"))
		}
	}
	sort.Strings(out)
	return out, nil
}

func (s *Studio) SlidePath(deck, id, ext string) string {
	return filepath.Join(s.DeckDir(deck), "slides", id+ext)
}

func (s *Studio) ReadSlide(deck, id string) (fragment, companion string, err error) {
	if !ValidDeckName(deck) || !ValidSlideID(id) {
		return "", "", fmt.Errorf("invalid deck/slide id")
	}
	fb, err := os.ReadFile(s.SlidePath(deck, id, ".html"))
	if err != nil {
		return "", "", err
	}
	cb, _ := os.ReadFile(s.SlidePath(deck, id, ".md")) // companion optional
	return string(fb), string(cb), nil
}

func (s *Studio) WriteFragment(deck, id, html string) error {
	if !ValidDeckName(deck) || !ValidSlideID(id) {
		return fmt.Errorf("invalid deck/slide id")
	}
	if !strings.Contains(html, "<section") {
		return fmt.Errorf("fragment must contain a <section> element")
	}
	return os.WriteFile(s.SlidePath(deck, id, ".html"), []byte(strings.TrimSpace(html)+"\n"), 0o644)
}

// AppendLog appends a line to the companion's "## Log" section (creating the
// section if absent). Companion is created if missing.
func (s *Studio) AppendLog(deck, id, line string) error {
	p := s.SlidePath(deck, id, ".md")
	b, err := os.ReadFile(p)
	content := ""
	if err == nil {
		content = string(b)
	}
	entry := fmt.Sprintf("- %s %s", time.Now().Format("2006-01-02"), line)
	if strings.Contains(content, "## Log") {
		content = strings.TrimRight(content, "\n") + "\n" + entry + "\n"
	} else {
		content = strings.TrimRight(content, "\n") + "\n\n## Log\n" + entry + "\n"
	}
	return os.WriteFile(p, []byte(content), 0o644)
}

// UpdateCompanionSection replaces the body of one "## Section" in the
// companion markdown, preserving everything else.
func (s *Studio) UpdateCompanionSection(deck, id, section, body string) error {
	if !ValidDeckName(deck) || !ValidSlideID(id) {
		return fmt.Errorf("invalid deck/slide id")
	}
	p := s.SlidePath(deck, id, ".md")
	b, err := os.ReadFile(p)
	if err != nil {
		return err
	}
	content := string(b)
	header := "## " + section
	idx := strings.Index(content, header)
	if idx < 0 {
		// append new section before Log if present, else at end
		block := "\n" + header + "\n" + strings.TrimSpace(body) + "\n"
		if li := strings.Index(content, "## Log"); li >= 0 {
			content = content[:li] + block + "\n" + content[li:]
		} else {
			content = strings.TrimRight(content, "\n") + "\n" + block
		}
		return os.WriteFile(p, []byte(content), 0o644)
	}
	rest := content[idx+len(header):]
	next := strings.Index(rest, "\n## ")
	var tail string
	if next >= 0 {
		tail = rest[next+1:]
	} else {
		tail = ""
	}
	newContent := content[:idx] + header + "\n" + strings.TrimSpace(body) + "\n\n" + tail
	return os.WriteFile(p, []byte(newContent), 0o644)
}

var titleRe = regexp.MustCompile(`(<div class="s-title"[^>]*>)([\s\S]*?)(</div>)`)

// SetTitle updates the slide's s-title text in the fragment.
func (s *Studio) SetTitle(deck, id, title string) error {
	frag, _, err := s.ReadSlide(deck, id)
	if err != nil {
		return err
	}
	if !titleRe.MatchString(frag) {
		return fmt.Errorf("slide %s has no .s-title element", id)
	}
	frag = titleRe.ReplaceAllString(frag, "${1}"+title+"${3}")
	if err := s.WriteFragment(deck, id, frag); err != nil {
		return err
	}
	return s.AppendLog(deck, id, fmt.Sprintf("title set to %q via edit API", title))
}

// HashSlides returns content hashes for every slide fragment in a deck.
func (s *Studio) HashSlides(deck string) (map[string]string, error) {
	ids, err := s.SlideIDs(deck)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, id := range ids {
		b, err := os.ReadFile(s.SlidePath(deck, id, ".html"))
		if err != nil {
			return nil, err
		}
		h := sha256.Sum256(b)
		out[id] = hex.EncodeToString(h[:8])
	}
	return out, nil
}
