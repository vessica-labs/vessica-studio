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
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is studio.yaml at the root of a content repo.
type Config struct {
	ThemeDefault   string `yaml:"theme_default"`
	Port           int    `yaml:"port"`
	AppHost        string `yaml:"app_host,omitempty"`
	PublicHost     string `yaml:"public_host,omitempty"`
	FollowDeck     string `yaml:"follow_deck,omitempty"`
	ShareSecretCmd string `yaml:"share_secret_cmd,omitempty"`
	OpenAI         struct {
		BaseURL           string `yaml:"base_url"`
		APIKeyCmd         string `yaml:"api_key_cmd"`
		ImageModel        string `yaml:"image_model"`
		RealtimeModel     string `yaml:"realtime_model"`
		RealtimeTokenPath string `yaml:"realtime_token_path"`
	} `yaml:"openai"`
	// Storage configures the S3-compatible bucket for video assets (Railway
	// Storage Bucket, R2, MinIO, …). Every field can instead come from the
	// VSTD_S3_* env vars (which win); the *_cmd fields resolve credentials
	// via a shell command (macOS Keychain pattern) so no secret sits in git.
	Storage struct {
		Endpoint     string `yaml:"endpoint,omitempty"`
		Bucket       string `yaml:"bucket,omitempty"`
		Region       string `yaml:"region,omitempty"`
		AccessKeyCmd string `yaml:"access_key_cmd,omitempty"`
		SecretKeyCmd string `yaml:"secret_key_cmd,omitempty"`
	} `yaml:"storage,omitempty"`
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
	if err := cloudRecoveryPending(abs); err != nil {
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
		if !strings.HasSuffix(n, ".html") {
			continue
		}
		id := strings.TrimSuffix(n, ".html")
		// scratch/temp files (e.g. a worker's _work_slide.html) must never
		// take the whole deck down — skip anything that isn't a valid id
		if !ValidSlideID(id) {
			continue
		}
		out = append(out, id)
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
	if idx := strings.Index(content, "## Log"); idx >= 0 {
		// insert at the END of the Log section — never spill into a later section
		rest := content[idx:]
		if next := strings.Index(rest, "\n## "); next >= 0 {
			at := idx + next
			content = strings.TrimRight(content[:at], "\n") + "\n" + entry + "\n" + content[at:]
		} else {
			content = strings.TrimRight(content, "\n") + "\n" + entry + "\n"
		}
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

// MoveSlide renames a slide so it sorts directly after the slide `after`
// ("" = to the front). Sparse numbering gives room; when two neighbors are
// adjacent it falls back to renumbering the whole deck at 10-step spacing.
// Returns the slide's new id.
func (s *Studio) MoveSlide(deck, id, after string) (string, error) {
	if !ValidDeckName(deck) || !ValidSlideID(id) || (after != "" && !ValidSlideID(after)) {
		return "", fmt.Errorf("invalid deck/slide id")
	}
	if id == after {
		return id, nil
	}
	ids, err := s.SlideIDs(deck)
	if err != nil {
		return "", err
	}
	rest := make([]string, 0, len(ids))
	found := false
	for _, x := range ids {
		if x == id {
			found = true
			continue
		}
		rest = append(rest, x)
	}
	if !found {
		return "", fmt.Errorf("slide %q not found", id)
	}
	pos := 0
	if after != "" {
		pos = -1
		for i, x := range rest {
			if x == after {
				pos = i + 1
				break
			}
		}
		if pos < 0 {
			return "", fmt.Errorf("slide %q not found", after)
		}
	}
	slug := id[strings.Index(id, "-")+1:]
	prev, next := "", ""
	if pos > 0 {
		prev = rest[pos-1]
	}
	if pos < len(rest) {
		next = rest[pos]
	}
	if p := midPrefix(prev, next); p != "" {
		newID := p + "-" + slug
		if err := s.renameSlide(deck, id, newID); err != nil {
			return "", err
		}
		return newID, nil
	}
	// no room — renumber the whole deck with the moved slide in place
	order := append(append(append([]string{}, rest[:pos]...), id), rest[pos:]...)
	return s.renumber(deck, order, id)
}

// midPrefix returns a numeric prefix sorting strictly between prev and next
// slide ids ("" allowed on either side), or "" when no room exists.
func midPrefix(prev, next string) string {
	a := 0
	if prev != "" {
		a, _ = strconv.Atoi(prev[:strings.Index(prev, "-")])
	}
	b := a + 20
	if next != "" {
		b, _ = strconv.Atoi(next[:strings.Index(next, "-")])
	} else {
		b = a + 20
	}
	if prev == "" && next != "" && b <= 1 {
		return ""
	}
	if b-a > 1 {
		return fmt.Sprintf("%04d", a+(b-a)/2)
	}
	if prev != "" {
		if ap := prev[:strings.Index(prev, "-")]; len(ap) < 5 {
			return ap + "5" // lexicographic midpoint: "0125" < "01255-…" < "0126"
		}
	}
	return ""
}

func (s *Studio) renameSlide(deck, oldID, newID string) error {
	if err := os.Rename(s.SlidePath(deck, oldID, ".html"), s.SlidePath(deck, newID, ".html")); err != nil {
		return err
	}
	for _, ext := range []string{".md", ".link.yaml"} {
		if _, err := os.Stat(s.SlidePath(deck, oldID, ext)); err == nil {
			if err := os.Rename(s.SlidePath(deck, oldID, ext), s.SlidePath(deck, newID, ext)); err != nil {
				return err
			}
		}
	}
	s.rewriteSourceLinkRefs(deck, oldID, newID)
	return nil
}

// renumber renames every slide to (i+1)*10 spacing, preserving order, via a
// temp-prefix phase to avoid collisions. Returns moved's final id.
func (s *Studio) renumber(deck string, order []string, moved string) (string, error) {
	dir := filepath.Join(s.DeckDir(deck), "slides")
	// phase 1: to temp names
	for i, id := range order {
		for _, ext := range []string{".html", ".md", ".link.yaml"} {
			if _, err := os.Stat(s.SlidePath(deck, id, ext)); err == nil {
				os.Rename(s.SlidePath(deck, id, ext), filepath.Join(dir, fmt.Sprintf("zztmp%04d%s", i, ext)))
			}
		}
	}
	movedNew := ""
	// phase 2: to final names
	for i, id := range order {
		slug := id[strings.Index(id, "-")+1:]
		newID := fmt.Sprintf("%04d-%s", (i+1)*10, slug)
		for _, ext := range []string{".html", ".md", ".link.yaml"} {
			tmp := filepath.Join(dir, fmt.Sprintf("zztmp%04d%s", i, ext))
			if _, err := os.Stat(tmp); err == nil {
				os.Rename(tmp, s.SlidePath(deck, newID, ext))
			}
		}
		if id == moved {
			movedNew = newID
		}
		s.rewriteSourceLinkRefs(deck, id, newID)
	}
	return movedNew, nil
}

// HashSlide returns the content hash of one slide fragment ("" if unreadable).
func (s *Studio) HashSlide(deck, id string) string {
	b, err := os.ReadFile(s.SlidePath(deck, id, ".html"))
	if err != nil {
		return ""
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:8])
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
