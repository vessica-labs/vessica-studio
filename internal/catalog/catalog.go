// Package catalog owns personal presentation organization that is independent
// of deck ownership and sharing permissions.
package catalog

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"
)

const TrashFolderName = "Trash"

func IsTrashFolderName(name string) bool {
	return strings.EqualFold(strings.TrimSpace(name), TrashFolderName)
}

// TrashFolderID returns a stable system-folder ID for a personal catalog.
// The scope distinguishes collaboration users; local catalogs use an empty
// scope because their state files are already isolated by studio root.
func TrashFolderID(scope string) string {
	if scope == "" {
		return "folder_trash"
	}
	sum := sha256.Sum256([]byte(scope))
	return "folder_trash_" + hex.EncodeToString(sum[:8])
}

type Folder struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Position int    `json:"position"`
	Count    int    `json:"count"`
	System   bool   `json:"system,omitempty"`
}

type CatalogDeck struct {
	ID           string     `json:"id"`
	StorageKey   string     `json:"storage_key"`
	Title        string     `json:"title"`
	OwnerUserID  string     `json:"owner_user_id,omitempty"`
	OwnerName    string     `json:"owner_name,omitempty"`
	Visibility   string     `json:"visibility,omitempty"`
	ForkedFrom   string     `json:"forked_from_id,omitempty"`
	Owned        bool       `json:"owned"`
	FolderID     string     `json:"folder_id,omitempty"`
	LastOpenedAt *time.Time `json:"last_opened_at,omitempty"`
}

// Deck is retained as a source-compatible alias for catalog adapters.
type Deck = CatalogDeck

type Catalog struct {
	Folders []Folder      `json:"folders"`
	Decks   []CatalogDeck `json:"decks"`
}
