package studio

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// SlideLink is persisted beside a linked slide as <slide>.link.yaml. The
// ordinary HTML and Markdown pair remain a durable snapshot, so a linked
// slide still builds when its source is unavailable.
type SlideLink struct {
	Version             int               `yaml:"version" json:"version"`
	SourceDeckID        string            `yaml:"source_deck_id,omitempty" json:"source_deck_id,omitempty"`
	SourceDeck          string            `yaml:"source_deck" json:"source_deck"`
	SourceDeckTitle     string            `yaml:"source_deck_title,omitempty" json:"source_deck_title,omitempty"`
	SourceSlide         string            `yaml:"source_slide" json:"source_slide"`
	SourceFragmentHash  string            `yaml:"source_fragment_hash" json:"source_fragment_hash"`
	SourceCompanionHash string            `yaml:"source_companion_hash,omitempty" json:"source_companion_hash,omitempty"`
	SourceAttachments   map[string]string `yaml:"source_attachments,omitempty" json:"source_attachments,omitempty"`
	LastRefreshedAt     string            `yaml:"last_refreshed_at" json:"last_refreshed_at"`
}

type SlideTransferRequest struct {
	SourceDeckID    string   `json:"source_deck_id,omitempty"`
	SourceDeck      string   `json:"source_deck"`
	SourceDeckTitle string   `json:"source_deck_title,omitempty"`
	TargetDeck      string   `json:"target_deck"`
	SlideIDs        []string `json:"slide_ids"`
	Mode            string   `json:"mode"`
}

type SlideTransferResult struct {
	SlideIDs []string `json:"slide_ids"`
}

func (s *Studio) LinkPath(deck, id string) string {
	return s.SlidePath(deck, id, ".link.yaml")
}

func (s *Studio) ReadSlideLink(deck, id string) (*SlideLink, error) {
	if !ValidDeckName(deck) || !ValidSlideID(id) {
		return nil, fmt.Errorf("invalid deck/slide id")
	}
	b, err := os.ReadFile(s.LinkPath(deck, id))
	if err != nil {
		return nil, err
	}
	var link SlideLink
	if err := yaml.Unmarshal(b, &link); err != nil {
		return nil, fmt.Errorf("linked slide metadata: %w", err)
	}
	if link.Version != 1 || !ValidDeckName(link.SourceDeck) || !ValidSlideID(link.SourceSlide) {
		return nil, fmt.Errorf("invalid linked slide metadata")
	}
	return &link, nil
}

func (s *Studio) IsLinkedSlide(deck, id string) bool {
	_, err := s.ReadSlideLink(deck, id)
	return err == nil
}

func shortContentHash(body []byte) string {
	h := sha256.Sum256(body)
	return hex.EncodeToString(h[:8])
}

func safeSlideSlug(id string) string {
	if i := strings.IndexByte(id, '-'); i >= 0 && i+1 < len(id) {
		return id[i+1:]
	}
	return "slide"
}

func nextTransferIDs(existing, source []string) []string {
	maxPrefix := 0
	used := map[string]bool{}
	for _, id := range existing {
		used[id] = true
		if i := strings.IndexByte(id, '-'); i > 0 {
			n, _ := strconv.Atoi(id[:i])
			if n > maxPrefix {
				maxPrefix = n
			}
		}
	}
	out := make([]string, 0, len(source))
	for _, src := range source {
		slug := safeSlideSlug(src)
		for {
			maxPrefix += 10
			id := fmt.Sprintf("%04d-%s", maxPrefix, slug)
			if !used[id] {
				used[id] = true
				out = append(out, id)
				break
			}
		}
	}
	return out
}

type stagedSlide struct {
	id                  string
	fragment            []byte
	companion           []byte
	link                []byte
	sources             map[string][]byte
	sourceFragmentHash  string
	sourceCompanionHash string
	sourceAttachments   map[string]string
}

func (s *Studio) stageTransferredSlide(req SlideTransferRequest, sourceID, targetID string) (stagedSlide, error) {
	fragment, companion, err := s.ReadSlide(req.SourceDeck, sourceID)
	if err != nil {
		return stagedSlide{}, err
	}
	if req.Mode == "link" && s.IsLinkedSlide(req.SourceDeck, sourceID) {
		return stagedSlide{}, fmt.Errorf("slide %q is already linked; copy it to flatten the snapshot", sourceID)
	}

	staged := stagedSlide{id: targetID, fragment: []byte(strings.TrimSpace(fragment) + "\n"), companion: []byte(strings.TrimRight(companion, "\n") + "\n"), sources: map[string][]byte{}, sourceAttachments: map[string]string{}}
	// Provenance hashes describe the live source bytes, before target-relative
	// attachment paths are rewritten in the durable snapshot.
	staged.sourceFragmentHash = shortContentHash(staged.fragment)
	staged.sourceCompanionHash = shortContentHash(staged.companion)
	attachments, _ := s.CompanionAttachments(req.SourceDeck, sourceID)
	for _, attachment := range attachments {
		body, err := os.ReadFile(filepath.Join(s.DeckDir(req.SourceDeck), filepath.FromSlash(attachment.Path)))
		if err != nil {
			return stagedSlide{}, fmt.Errorf("copy source attachment %q: %w", attachment.Name, err)
		}
		ext := filepath.Ext(attachment.Path)
		staged.sourceAttachments[attachment.Path] = shortContentHash(body)
		name := "attachment-" + shortContentHash(body) + ext
		newPath := "sources/" + name
		staged.sources[name] = body
		staged.fragment = []byte(strings.ReplaceAll(string(staged.fragment), attachment.Path, newPath))
		staged.companion = []byte(strings.ReplaceAll(string(staged.companion), attachment.Path, newPath))
	}

	if req.Mode == "link" {
		link := SlideLink{
			Version: 1, SourceDeckID: req.SourceDeckID, SourceDeck: req.SourceDeck,
			SourceDeckTitle: req.SourceDeckTitle, SourceSlide: sourceID,
			SourceFragmentHash:  staged.sourceFragmentHash,
			SourceCompanionHash: staged.sourceCompanionHash,
			SourceAttachments:   staged.sourceAttachments,
			LastRefreshedAt:     time.Now().UTC().Format(time.RFC3339),
		}
		staged.link, err = yaml.Marshal(&link)
		if err != nil {
			return stagedSlide{}, err
		}
	}
	return staged, nil
}

// TransferSlides appends an ordered batch to a target deck. It stages every
// file before promotion and removes anything it created if promotion fails.
func (s *Studio) TransferSlides(req SlideTransferRequest) (SlideTransferResult, error) {
	if !ValidDeckName(req.SourceDeck) || !ValidDeckName(req.TargetDeck) || req.SourceDeck == req.TargetDeck {
		return SlideTransferResult{}, fmt.Errorf("source and target presentations must be different")
	}
	if req.Mode != "copy" && req.Mode != "link" {
		return SlideTransferResult{}, fmt.Errorf("transfer mode must be copy or link")
	}
	if len(req.SlideIDs) == 0 || len(req.SlideIDs) > 200 {
		return SlideTransferResult{}, fmt.Errorf("select between 1 and 200 slides")
	}
	wanted := map[string]bool{}
	for _, id := range req.SlideIDs {
		if !ValidSlideID(id) || wanted[id] {
			return SlideTransferResult{}, fmt.Errorf("invalid or duplicate slide %q", id)
		}
		wanted[id] = true
	}
	sourceOrder, err := s.SlideIDs(req.SourceDeck)
	if err != nil {
		return SlideTransferResult{}, err
	}
	ordered := make([]string, 0, len(wanted))
	for _, id := range sourceOrder {
		if wanted[id] {
			ordered = append(ordered, id)
		}
	}
	if len(ordered) != len(wanted) {
		return SlideTransferResult{}, fmt.Errorf("one or more source slides no longer exist")
	}
	targetExisting, err := s.SlideIDs(req.TargetDeck)
	if err != nil {
		return SlideTransferResult{}, err
	}
	targetIDs := nextTransferIDs(targetExisting, ordered)
	staged := make([]stagedSlide, 0, len(ordered))
	for i, sourceID := range ordered {
		item, err := s.stageTransferredSlide(req, sourceID, targetIDs[i])
		if err != nil {
			return SlideTransferResult{}, err
		}
		staged = append(staged, item)
	}

	stageDir, err := os.MkdirTemp(s.DeckDir(req.TargetDeck), ".vstd-transfer-*")
	if err != nil {
		return SlideTransferResult{}, err
	}
	defer os.RemoveAll(stageDir)
	for _, item := range staged {
		if err := os.WriteFile(filepath.Join(stageDir, item.id+".html"), item.fragment, 0o644); err != nil {
			return SlideTransferResult{}, err
		}
		if err := os.WriteFile(filepath.Join(stageDir, item.id+".md"), item.companion, 0o644); err != nil {
			return SlideTransferResult{}, err
		}
		if len(item.link) > 0 {
			if err := os.WriteFile(filepath.Join(stageDir, item.id+".link.yaml"), item.link, 0o644); err != nil {
				return SlideTransferResult{}, err
			}
		}
	}

	created := []string{}
	rollback := func() {
		for _, path := range created {
			_ = os.Remove(path)
		}
	}
	sourcesDir := filepath.Join(s.DeckDir(req.TargetDeck), "sources")
	if err := os.MkdirAll(sourcesDir, 0o755); err != nil {
		return SlideTransferResult{}, err
	}
	for _, item := range staged {
		names := make([]string, 0, len(item.sources))
		for name := range item.sources {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			path := filepath.Join(sourcesDir, name)
			if _, err := os.Stat(path); err == nil {
				continue
			}
			if err := os.WriteFile(path, item.sources[name], 0o644); err != nil {
				rollback()
				return SlideTransferResult{}, err
			}
			created = append(created, path)
		}
		for _, ext := range []string{".html", ".md", ".link.yaml"} {
			src := filepath.Join(stageDir, item.id+ext)
			if _, err := os.Stat(src); err != nil {
				continue
			}
			dst := s.SlidePath(req.TargetDeck, item.id, ext)
			if err := os.Rename(src, dst); err != nil {
				rollback()
				return SlideTransferResult{}, err
			}
			created = append(created, dst)
		}
	}
	return SlideTransferResult{SlideIDs: targetIDs}, nil
}

// RefreshSlideLink updates a linked slide's durable snapshot. Authorization
// is deliberately handled by the server before this filesystem operation.
func (s *Studio) RefreshSlideLink(deck, id string) (bool, error) {
	link, err := s.ReadSlideLink(deck, id)
	if err != nil {
		return false, err
	}
	if s.IsLinkedSlide(link.SourceDeck, link.SourceSlide) {
		return false, fmt.Errorf("linked source chains are not supported")
	}
	if s.SlideLinkCurrent(link) {
		return false, nil
	}
	req := SlideTransferRequest{SourceDeckID: link.SourceDeckID, SourceDeck: link.SourceDeck, SourceDeckTitle: link.SourceDeckTitle, TargetDeck: deck, Mode: "link"}
	staged, err := s.stageTransferredSlide(req, link.SourceSlide, id)
	if err != nil {
		return false, err
	}
	for name, body := range staged.sources {
		dir := filepath.Join(s.DeckDir(deck), "sources")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return false, err
		}
		if err := os.WriteFile(filepath.Join(dir, name), body, 0o644); err != nil {
			return false, err
		}
	}
	for ext, body := range map[string][]byte{".html": staged.fragment, ".md": staged.companion, ".link.yaml": staged.link} {
		path := s.SlidePath(deck, id, ext)
		tmp := path + ".tmp"
		if err := os.WriteFile(tmp, body, 0o644); err != nil {
			return false, err
		}
		if err := os.Rename(tmp, path); err != nil {
			return false, err
		}
	}
	return true, nil
}

func equalStringMap(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for key, value := range a {
		if b[key] != value {
			return false
		}
	}
	return true
}

// SlideLinkCurrent compares all source material that participates in the
// retained snapshot, including attachment bytes whose path did not change.
func (s *Studio) SlideLinkCurrent(link *SlideLink) bool {
	if link == nil {
		return false
	}
	fragment, companion, err := s.ReadSlide(link.SourceDeck, link.SourceSlide)
	if err != nil {
		return false
	}
	if shortContentHash([]byte(strings.TrimSpace(fragment)+"\n")) != link.SourceFragmentHash ||
		shortContentHash([]byte(strings.TrimRight(companion, "\n")+"\n")) != link.SourceCompanionHash {
		return false
	}
	hashes := map[string]string{}
	attachments, _ := s.CompanionAttachments(link.SourceDeck, link.SourceSlide)
	for _, attachment := range attachments {
		body, err := os.ReadFile(filepath.Join(s.DeckDir(link.SourceDeck), filepath.FromSlash(attachment.Path)))
		if err != nil {
			return false
		}
		hashes[attachment.Path] = shortContentHash(body)
	}
	return equalStringMap(hashes, link.SourceAttachments)
}

func (s *Studio) DetachSlideLink(deck, id string) error {
	if !s.IsLinkedSlide(deck, id) {
		return fmt.Errorf("slide is not linked")
	}
	return os.Remove(s.LinkPath(deck, id))
}

// RefreshAccessibleLinks refreshes every linked snapshot whose source exists
// in this local studio. Hosted callers must use their authorization-aware
// server path instead.
func (s *Studio) RefreshAccessibleLinks(deck string) (int, error) {
	ids, err := s.SlideIDs(deck)
	if err != nil {
		return 0, err
	}
	changed := 0
	for _, id := range ids {
		if !s.IsLinkedSlide(deck, id) {
			continue
		}
		if did, refreshErr := s.RefreshSlideLink(deck, id); refreshErr == nil && did {
			changed++
		}
	}
	return changed, nil
}

func (s *Studio) rewriteSourceLinkRefs(sourceDeck, oldID, newID string) {
	decks, _ := s.ListDecks()
	for _, deck := range decks {
		ids, _ := s.SlideIDs(deck)
		for _, id := range ids {
			link, err := s.ReadSlideLink(deck, id)
			if err != nil || link.SourceDeck != sourceDeck || link.SourceSlide != oldID {
				continue
			}
			link.SourceSlide = newID
			b, err := yaml.Marshal(link)
			if err == nil {
				_ = os.WriteFile(s.LinkPath(deck, id), b, 0o644)
			}
		}
	}
}
