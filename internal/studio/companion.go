package studio

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// CompanionAttachment records a source artifact used to create or substantiate
// a slide. Path is deck-relative and always points beneath sources/.
type CompanionAttachment struct {
	Name      string `yaml:"name" json:"name"`
	Path      string `yaml:"path" json:"path"`
	MediaType string `yaml:"media_type,omitempty" json:"media_type,omitempty"`
	Page      int    `yaml:"page,omitempty" json:"page,omitempty"`
	URL       string `yaml:"-" json:"url,omitempty"`
}

func (s *Studio) ReadCompanion(deck, id string) (string, error) {
	if !ValidDeckName(deck) || !ValidSlideID(id) {
		return "", fmt.Errorf("invalid deck/slide id")
	}
	b, err := os.ReadFile(s.SlidePath(deck, id, ".md"))
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func (s *Studio) WriteCompanion(deck, id, markdown string) error {
	if !ValidDeckName(deck) || !ValidSlideID(id) {
		return fmt.Errorf("invalid deck/slide id")
	}
	if strings.IndexByte(markdown, 0) >= 0 {
		return fmt.Errorf("companion contains invalid NUL byte")
	}
	return os.WriteFile(s.SlidePath(deck, id, ".md"), []byte(strings.TrimRight(markdown, "\n")+"\n"), 0o644)
}

func (s *Studio) HashCompanion(deck, id string) string {
	b, err := os.ReadFile(s.SlidePath(deck, id, ".md"))
	if err != nil {
		return ""
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:8])
}

func companionFrontmatter(markdown string) (front, body string, ok bool) {
	if !strings.HasPrefix(markdown, "---\n") {
		return "", markdown, false
	}
	end := strings.Index(markdown[4:], "\n---")
	if end < 0 {
		return "", markdown, false
	}
	end += 4
	lineEnd := end + len("\n---")
	if lineEnd < len(markdown) && markdown[lineEnd] == '\r' {
		lineEnd++
	}
	if lineEnd < len(markdown) && markdown[lineEnd] == '\n' {
		lineEnd++
	}
	return markdown[4:end], markdown[lineEnd:], true
}

func (s *Studio) CompanionAttachments(deck, id string) ([]CompanionAttachment, error) {
	markdown, err := s.ReadCompanion(deck, id)
	if err != nil {
		return nil, err
	}
	front, _, ok := companionFrontmatter(markdown)
	if !ok {
		return nil, nil
	}
	var meta struct {
		Attachments []CompanionAttachment `yaml:"attachments"`
	}
	if err := yaml.Unmarshal([]byte(front), &meta); err != nil {
		return nil, fmt.Errorf("companion frontmatter: %w", err)
	}
	var out []CompanionAttachment
	for _, a := range meta.Attachments {
		if strings.HasPrefix(a.Path, "sources/") && !strings.Contains(a.Path, "..") {
			out = append(out, a)
		}
	}
	return out, nil
}

func (s *Studio) AddCompanionAttachment(deck, id string, attachment CompanionAttachment) error {
	if !strings.HasPrefix(attachment.Path, "sources/") || strings.Contains(attachment.Path, "..") {
		return fmt.Errorf("attachment path must be beneath sources/")
	}
	markdown, err := s.ReadCompanion(deck, id)
	if err != nil {
		return err
	}
	front, body, ok := companionFrontmatter(markdown)
	if !ok {
		front = "slide: " + id
		body = markdown
	}
	attachments, err := s.CompanionAttachments(deck, id)
	if err != nil && ok {
		return err
	}
	for _, existing := range attachments {
		if existing.Path == attachment.Path {
			return nil
		}
	}
	block := "  - name: " + strconv.Quote(attachment.Name) + "\n" +
		"    path: " + strconv.Quote(attachment.Path) + "\n"
	if attachment.MediaType != "" {
		block += "    media_type: " + strconv.Quote(attachment.MediaType) + "\n"
	}
	if attachment.Page > 0 {
		block += fmt.Sprintf("    page: %d\n", attachment.Page)
	}

	lines := strings.Split(front, "\n")
	inserted := false
	for i, line := range lines {
		if line != "attachments:" {
			continue
		}
		end := i + 1
		for end < len(lines) && (strings.HasPrefix(lines[end], " ") || strings.TrimSpace(lines[end]) == "") {
			end++
		}
		addition := strings.Split(strings.TrimRight(block, "\n"), "\n")
		lines = append(lines[:end], append(addition, lines[end:]...)...)
		inserted = true
		break
	}
	if !inserted {
		front = strings.TrimRight(front, "\n") + "\nattachments:\n" + block
	} else {
		front = strings.Join(lines, "\n")
	}
	return s.WriteCompanion(deck, id, "---\n"+strings.TrimRight(front, "\n")+"\n---\n"+body)
}
