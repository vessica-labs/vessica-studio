package catalog

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLocalCatalogPersistsFoldersPlacementsAndDeleteToRoot(t *testing.T) {
	t.Setenv("VSTD_USER_CONFIG_DIR", t.TempDir())
	root := t.TempDir()
	store, err := OpenLocal(root)
	if err != nil {
		t.Fatal(err)
	}
	folder, err := store.CreateFolder("Client work")
	if err != nil {
		t.Fatal(err)
	}
	accessible := map[string]bool{"owned": true, "shared": true}
	if err := store.MoveDecks([]string{"owned", "shared"}, folder.ID, accessible); err != nil {
		t.Fatal(err)
	}
	store.Touch("shared")

	reloaded, err := OpenLocal(root)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := reloaded.Snapshot([]Deck{{ID: "owned"}, {ID: "shared"}})
	if len(snapshot.Folders) != 2 || snapshot.Folders[0].ID != folder.ID || snapshot.Folders[0].Count != 2 || !snapshot.Folders[1].System || snapshot.Folders[1].Name != TrashFolderName {
		t.Fatalf("folders = %#v", snapshot.Folders)
	}
	if snapshot.Decks[0].FolderID != folder.ID || snapshot.Decks[1].LastOpenedAt == nil || snapshot.Decks[1].LastOpenedAt.IsZero() {
		t.Fatalf("placements = %#v", snapshot.Decks)
	}
	if err := reloaded.DeleteFolder(folder.ID); err != nil {
		t.Fatal(err)
	}
	snapshot = reloaded.Snapshot([]Deck{{ID: "owned"}, {ID: "shared"}})
	if len(snapshot.Folders) != 1 || !snapshot.Folders[0].System || snapshot.Decks[0].FolderID != "" || snapshot.Decks[1].FolderID != "" {
		t.Fatalf("delete did not return decks to root: %#v", snapshot)
	}

	entries, err := filepath.Glob(filepath.Join(os.Getenv("VSTD_USER_CONFIG_DIR"), "catalog", "*.json"))
	if err != nil || len(entries) != 1 {
		t.Fatalf("catalog files = %v, %v", entries, err)
	}
	info, err := os.Stat(entries[0])
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("catalog permissions = %v, %v", info, err)
	}
}

func TestLocalTrashFolderIsPermanentAndPersistsPlacements(t *testing.T) {
	t.Setenv("VSTD_USER_CONFIG_DIR", t.TempDir())
	root := t.TempDir()
	store, err := OpenLocal(root)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := store.Snapshot([]Deck{{ID: "deck"}})
	if len(snapshot.Folders) != 1 || !snapshot.Folders[0].System || snapshot.Folders[0].Name != TrashFolderName {
		t.Fatalf("trash folder = %#v", snapshot.Folders)
	}
	trash := snapshot.Folders[0]
	if err := store.MoveDecks([]string{"deck"}, trash.ID, map[string]bool{"deck": true}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RenameFolder(trash.ID, "Archive"); err == nil {
		t.Fatal("expected permanent Trash rename rejection")
	}
	if err := store.DeleteFolder(trash.ID); err == nil {
		t.Fatal("expected permanent Trash delete rejection")
	}
	if _, err := store.CreateFolder("trash"); err == nil {
		t.Fatal("expected reserved Trash name rejection")
	}
	reloaded, err := OpenLocal(root)
	if err != nil {
		t.Fatal(err)
	}
	snapshot = reloaded.Snapshot([]Deck{{ID: "deck"}})
	if len(snapshot.Folders) != 1 || snapshot.Decks[0].FolderID != snapshot.Folders[0].ID || snapshot.Folders[0].Count != 1 {
		t.Fatalf("trash placement did not persist: %#v", snapshot)
	}
}

func TestLocalCatalogRejectsUnknownFolderAndInaccessibleDeck(t *testing.T) {
	t.Setenv("VSTD_USER_CONFIG_DIR", t.TempDir())
	store, err := OpenLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MoveDecks([]string{"deck"}, "missing", map[string]bool{"deck": true}); err == nil {
		t.Fatal("expected unknown folder rejection")
	}
	folder, _ := store.CreateFolder("Folder")
	if err := store.MoveDecks([]string{"private"}, folder.ID, map[string]bool{}); err == nil {
		t.Fatal("expected inaccessible deck rejection")
	}
}
