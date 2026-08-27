package catalog

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type localPlacement struct {
	FolderID     string    `json:"folder_id,omitempty"`
	LastOpenedAt time.Time `json:"last_opened_at,omitempty"`
}

type localState struct {
	Version    int                       `json:"version"`
	Folders    []Folder                  `json:"folders"`
	Placements map[string]localPlacement `json:"placements"`
}

type LocalStore struct {
	mu    sync.Mutex
	path  string
	state localState
}

func OpenLocal(studioRoot string) (*LocalStore, error) {
	base := strings.TrimSpace(os.Getenv("VSTD_USER_CONFIG_DIR"))
	if base == "" {
		var err error
		base, err = os.UserConfigDir()
		if err != nil {
			return nil, err
		}
		base = filepath.Join(base, "vessica-studio")
	}
	abs, err := filepath.Abs(studioRoot)
	if err != nil {
		return nil, err
	}
	h := sha256.Sum256([]byte(abs))
	path := filepath.Join(base, "catalog", hex.EncodeToString(h[:8])+".json")
	s := &LocalStore{path: path, state: localState{Version: 1, Placements: map[string]localPlacement{}}}
	b, err := os.ReadFile(path)
	if err == nil {
		if err := json.Unmarshal(b, &s.state); err != nil {
			return nil, fmt.Errorf("local catalog: %w", err)
		}
	}
	if s.state.Placements == nil {
		s.state.Placements = map[string]localPlacement{}
	}
	if s.ensureTrashLocked() {
		if err := s.saveLocked(); err != nil {
			return nil, err
		}
	}
	return s, nil
}

func (s *LocalStore) ensureTrashLocked() bool {
	trashIndex := -1
	maxPosition := -1
	for i := range s.state.Folders {
		if IsTrashFolderName(s.state.Folders[i].Name) {
			if trashIndex == -1 {
				trashIndex = i
			}
			continue
		}
		if s.state.Folders[i].Position > maxPosition {
			maxPosition = s.state.Folders[i].Position
		}
	}
	if trashIndex == -1 {
		s.state.Folders = append(s.state.Folders, Folder{ID: TrashFolderID(""), Name: TrashFolderName, Position: maxPosition + 1, System: true})
		return true
	}
	f := &s.state.Folders[trashIndex]
	changed := f.Name != TrashFolderName || f.Position != maxPosition+1 || !f.System
	f.Name = TrashFolderName
	f.Position = maxPosition + 1
	f.System = true
	return changed
}

func randomFolderID() string {
	b := make([]byte, 10)
	_, _ = rand.Read(b)
	return "folder_" + hex.EncodeToString(b)
}

func (s *LocalStore) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s.state, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func validateFolderName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || len([]rune(name)) > 80 || strings.ContainsAny(name, "\r\n") {
		return "", fmt.Errorf("folder name must be between 1 and 80 characters")
	}
	return name, nil
}

func (s *LocalStore) Snapshot(decks []Deck) Catalog {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensureTrashLocked()
	out := Catalog{Folders: append([]Folder{}, s.state.Folders...), Decks: append([]Deck{}, decks...)}
	counts := map[string]int{}
	for i := range out.Decks {
		if p, ok := s.state.Placements[out.Decks[i].ID]; ok {
			out.Decks[i].FolderID = p.FolderID
			if !p.LastOpenedAt.IsZero() {
				opened := p.LastOpenedAt
				out.Decks[i].LastOpenedAt = &opened
			}
			counts[p.FolderID]++
		}
	}
	for i := range out.Folders {
		out.Folders[i].Count = counts[out.Folders[i].ID]
	}
	sort.SliceStable(out.Folders, func(i, j int) bool {
		if out.Folders[i].Position == out.Folders[j].Position {
			return strings.ToLower(out.Folders[i].Name) < strings.ToLower(out.Folders[j].Name)
		}
		return out.Folders[i].Position < out.Folders[j].Position
	})
	return out
}

func (s *LocalStore) CreateFolder(name string) (Folder, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	name, err := validateFolderName(name)
	if err != nil {
		return Folder{}, err
	}
	if IsTrashFolderName(name) {
		return Folder{}, fmt.Errorf("Trash is a permanent system folder")
	}
	for _, f := range s.state.Folders {
		if strings.EqualFold(f.Name, name) {
			return Folder{}, fmt.Errorf("a folder with that name already exists")
		}
	}
	f := Folder{ID: randomFolderID(), Name: name, Position: len(s.state.Folders)}
	s.state.Folders = append(s.state.Folders, f)
	return f, s.saveLocked()
}

func (s *LocalStore) RenameFolder(id, name string) (Folder, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, f := range s.state.Folders {
		if f.ID == id && (f.System || IsTrashFolderName(f.Name)) {
			return Folder{}, fmt.Errorf("Trash is a permanent system folder")
		}
	}
	name, err := validateFolderName(name)
	if err != nil {
		return Folder{}, err
	}
	for _, f := range s.state.Folders {
		if f.ID != id && strings.EqualFold(f.Name, name) {
			return Folder{}, fmt.Errorf("a folder with that name already exists")
		}
	}
	for i := range s.state.Folders {
		if s.state.Folders[i].ID == id {
			s.state.Folders[i].Name = name
			return s.state.Folders[i], s.saveLocked()
		}
	}
	return Folder{}, os.ErrNotExist
}

func (s *LocalStore) DeleteFolder(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	found := false
	keep := s.state.Folders[:0]
	for _, f := range s.state.Folders {
		if f.ID == id {
			if f.System || IsTrashFolderName(f.Name) {
				return fmt.Errorf("Trash is a permanent system folder")
			}
			found = true
			continue
		}
		keep = append(keep, f)
	}
	if !found {
		return os.ErrNotExist
	}
	s.state.Folders = keep
	for key, p := range s.state.Placements {
		if p.FolderID == id {
			p.FolderID = ""
			s.state.Placements[key] = p
		}
	}
	return s.saveLocked()
}

func (s *LocalStore) ReorderFolders(ids []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(ids) != len(s.state.Folders) {
		return fmt.Errorf("folder order must contain every folder")
	}
	byID := map[string]*Folder{}
	for i := range s.state.Folders {
		byID[s.state.Folders[i].ID] = &s.state.Folders[i]
	}
	for i, id := range ids {
		f := byID[id]
		if f == nil {
			return fmt.Errorf("unknown folder")
		}
		f.Position = i
		delete(byID, id)
	}
	if len(byID) != 0 {
		return fmt.Errorf("folder order contains duplicates")
	}
	return s.saveLocked()
}

func (s *LocalStore) MoveDecks(deckIDs []string, folderID string, accessible map[string]bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if folderID != "" {
		found := false
		for _, f := range s.state.Folders {
			found = found || f.ID == folderID
		}
		if !found {
			return fmt.Errorf("folder not found")
		}
	}
	if len(deckIDs) == 0 || len(deckIDs) > 500 {
		return fmt.Errorf("select between 1 and 500 presentations")
	}
	for _, id := range deckIDs {
		if !accessible[id] {
			return fmt.Errorf("presentation access denied")
		}
	}
	for _, id := range deckIDs {
		p := s.state.Placements[id]
		p.FolderID = folderID
		s.state.Placements[id] = p
	}
	return s.saveLocked()
}

func (s *LocalStore) Touch(deckID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.state.Placements[deckID]
	p.LastOpenedAt = time.Now().UTC()
	s.state.Placements[deckID] = p
	_ = s.saveLocked()
}
