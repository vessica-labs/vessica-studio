package collab

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/vessica-labs/vessica-studio/internal/catalog"
)

func cleanFolderName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || len([]rune(name)) > 80 || strings.ContainsAny(name, "\r\n") {
		return "", fmt.Errorf("folder name must be between 1 and 80 characters")
	}
	return name, nil
}

func (s *Store) ensureTrashFolder(ctx context.Context, userID string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO vstd_folders(id,team_id,owner_user_id,name,position)
SELECT $1,$2,$3,$4,COALESCE((SELECT max(position)+1 FROM vstd_folders WHERE team_id=$2 AND owner_user_id=$3),0)
WHERE NOT EXISTS (SELECT 1 FROM vstd_folders WHERE team_id=$2 AND owner_user_id=$3 AND lower(name)=lower($4))
ON CONFLICT DO NOTHING`, catalog.TrashFolderID(userID), DefaultTeamID, userID, catalog.TrashFolderName)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `UPDATE vstd_folders SET name=$1,updated_at=now()
WHERE team_id=$2 AND owner_user_id=$3 AND lower(name)=lower($1) AND name<>$1`, catalog.TrashFolderName, DefaultTeamID, userID)
	return err
}

func (s *Store) Catalog(ctx context.Context, userID string) (catalog.Catalog, error) {
	decks, err := s.ListDecks(ctx, userID)
	if err != nil {
		return catalog.Catalog{}, err
	}
	if err := s.ensureTrashFolder(ctx, userID); err != nil {
		return catalog.Catalog{}, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id,name,position FROM vstd_folders WHERE team_id=$1 AND owner_user_id=$2 ORDER BY CASE WHEN lower(name)=lower($3) THEN 1 ELSE 0 END,position,lower(name)`, DefaultTeamID, userID, catalog.TrashFolderName)
	if err != nil {
		return catalog.Catalog{}, err
	}
	defer rows.Close()
	out := catalog.Catalog{Folders: []catalog.Folder{}, Decks: []catalog.CatalogDeck{}}
	for rows.Next() {
		var f catalog.Folder
		if err := rows.Scan(&f.ID, &f.Name, &f.Position); err != nil {
			return catalog.Catalog{}, err
		}
		f.System = catalog.IsTrashFolderName(f.Name)
		out.Folders = append(out.Folders, f)
	}
	if err := rows.Err(); err != nil {
		return catalog.Catalog{}, err
	}
	placements := map[string]struct {
		folder string
		opened sql.NullTime
	}{}
	prows, err := s.db.QueryContext(ctx, `SELECT deck_id,COALESCE(folder_id,''),last_opened_at FROM vstd_deck_placements WHERE user_id=$1`, userID)
	if err != nil {
		return catalog.Catalog{}, err
	}
	defer prows.Close()
	for prows.Next() {
		var deckID, folderID string
		var opened sql.NullTime
		if err := prows.Scan(&deckID, &folderID, &opened); err != nil {
			return catalog.Catalog{}, err
		}
		placements[deckID] = struct {
			folder string
			opened sql.NullTime
		}{folderID, opened}
	}
	counts := map[string]int{}
	for _, d := range decks {
		cd := catalog.Deck{ID: d.ID, StorageKey: d.StorageKey, Title: d.Title, OwnerUserID: d.OwnerUserID, OwnerName: d.OwnerName, Visibility: d.Visibility, ForkedFrom: d.ForkedFrom, Owned: d.Owned}
		if p, ok := placements[d.ID]; ok {
			cd.FolderID = p.folder
			if p.opened.Valid {
				opened := p.opened.Time
				cd.LastOpenedAt = &opened
			}
			counts[p.folder]++
		}
		out.Decks = append(out.Decks, cd)
	}
	for i := range out.Folders {
		out.Folders[i].Count = counts[out.Folders[i].ID]
	}
	return out, nil
}

func (s *Store) CreateFolder(ctx context.Context, userID, name string) (catalog.Folder, error) {
	name, err := cleanFolderName(name)
	if err != nil {
		return catalog.Folder{}, err
	}
	if catalog.IsTrashFolderName(name) {
		return catalog.Folder{}, fmt.Errorf("Trash is a permanent system folder")
	}
	f := catalog.Folder{ID: randomID("folder"), Name: name}
	err = s.db.QueryRowContext(ctx, `INSERT INTO vstd_folders(id,team_id,owner_user_id,name,position)
VALUES($1,$2,$3,$4,COALESCE((SELECT max(position)+1 FROM vstd_folders WHERE team_id=$2 AND owner_user_id=$3),0)) RETURNING position`, f.ID, DefaultTeamID, userID, name).Scan(&f.Position)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return catalog.Folder{}, fmt.Errorf("a folder with that name already exists")
		}
		return catalog.Folder{}, err
	}
	_ = s.Audit(ctx, userID, "folder.create", "", "", map[string]any{"folder_id": f.ID})
	return f, nil
}

func (s *Store) RenameFolder(ctx context.Context, userID, id, name string) (catalog.Folder, error) {
	var currentName string
	if err := s.db.QueryRowContext(ctx, `SELECT name FROM vstd_folders WHERE id=$1 AND owner_user_id=$2`, id, userID).Scan(&currentName); err != nil {
		return catalog.Folder{}, err
	}
	if catalog.IsTrashFolderName(currentName) {
		return catalog.Folder{}, fmt.Errorf("Trash is a permanent system folder")
	}
	name, err := cleanFolderName(name)
	if err != nil {
		return catalog.Folder{}, err
	}
	var f catalog.Folder
	err = s.db.QueryRowContext(ctx, `UPDATE vstd_folders SET name=$1,updated_at=now() WHERE id=$2 AND owner_user_id=$3 RETURNING id,name,position`, name, id, userID).Scan(&f.ID, &f.Name, &f.Position)
	if err != nil {
		return catalog.Folder{}, err
	}
	_ = s.Audit(ctx, userID, "folder.rename", "", "", map[string]any{"folder_id": id})
	return f, nil
}

func (s *Store) DeleteFolder(ctx context.Context, userID, id string) error {
	var name string
	if err := s.db.QueryRowContext(ctx, `SELECT name FROM vstd_folders WHERE id=$1 AND owner_user_id=$2`, id, userID).Scan(&name); err != nil {
		return err
	}
	if catalog.IsTrashFolderName(name) {
		return fmt.Errorf("Trash is a permanent system folder")
	}
	res, err := s.db.ExecContext(ctx, `DELETE FROM vstd_folders WHERE id=$1 AND owner_user_id=$2`, id, userID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return sql.ErrNoRows
	}
	return s.Audit(ctx, userID, "folder.delete", "", "", map[string]any{"folder_id": id})
}

func (s *Store) ReorderFolders(ctx context.Context, userID string, ids []string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM vstd_folders WHERE owner_user_id=$1 AND team_id=$2`, userID, DefaultTeamID).Scan(&count); err != nil {
		return err
	}
	if count != len(ids) {
		return fmt.Errorf("folder order must contain every folder")
	}
	seen := map[string]bool{}
	for i, id := range ids {
		if seen[id] {
			return fmt.Errorf("folder order contains duplicates")
		}
		seen[id] = true
		res, err := tx.ExecContext(ctx, `UPDATE vstd_folders SET position=$1,updated_at=now() WHERE id=$2 AND owner_user_id=$3`, i, id, userID)
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		if n != 1 {
			return fmt.Errorf("unknown folder")
		}
	}
	return tx.Commit()
}

func (s *Store) MoveDecksToFolder(ctx context.Context, userID string, deckIDs []string, folderID string) error {
	if len(deckIDs) == 0 || len(deckIDs) > 500 {
		return fmt.Errorf("select between 1 and 500 presentations")
	}
	if folderID != "" {
		var ok bool
		if err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM vstd_folders WHERE id=$1 AND owner_user_id=$2)`, folderID, userID).Scan(&ok); err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("folder not found")
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, deckID := range deckIDs {
		deck, err := s.DeckByID(ctx, deckID)
		if err != nil || !s.Can(ctx, userID, deck, "view") {
			return fmt.Errorf("presentation access denied")
		}
		var folder any
		if folderID != "" {
			folder = folderID
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO vstd_deck_placements(user_id,deck_id,folder_id) VALUES($1,$2,$3)
ON CONFLICT(user_id,deck_id) DO UPDATE SET folder_id=EXCLUDED.folder_id,updated_at=now()`, userID, deckID, folder); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return s.Audit(ctx, userID, "folder.move_decks", "", "", map[string]any{"folder_id": folderID, "deck_count": len(deckIDs)})
}

func (s *Store) TouchDeck(ctx context.Context, userID, deckID string) {
	_, _ = s.db.ExecContext(ctx, `INSERT INTO vstd_deck_placements(user_id,deck_id,last_opened_at) VALUES($1,$2,now())
ON CONFLICT(user_id,deck_id) DO UPDATE SET last_opened_at=now(),updated_at=now()`, userID, deckID)
}
