// Package catalog owns personal presentation organization that is independent
// of deck ownership and sharing permissions.
package catalog

import "time"

type Folder struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Position int    `json:"position"`
	Count    int    `json:"count"`
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
