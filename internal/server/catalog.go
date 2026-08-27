package server

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/vessica-labs/vessica-studio/internal/catalog"
	"github.com/vessica-labs/vessica-studio/internal/studio"
)

func (s *Server) localCatalogStore() (*catalog.LocalStore, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.LocalCatalog != nil {
		return s.LocalCatalog, nil
	}
	store, err := catalog.OpenLocal(s.St.Root)
	if err != nil {
		return nil, err
	}
	s.LocalCatalog = store
	return store, nil
}

func (s *Server) localCatalogDecks() ([]catalog.Deck, error) {
	keys, err := s.St.ListDecks()
	if err != nil {
		return nil, err
	}
	out := make([]catalog.Deck, 0, len(keys))
	for _, key := range keys {
		meta, _ := s.St.LoadDeckMeta(key)
		d := catalog.Deck{ID: key, StorageKey: key, Title: key, Owned: true, Visibility: "private"}
		if meta != nil {
			d.Title = meta.Title
			d.Visibility = meta.Visibility
			d.ForkedFrom = meta.ForkedFrom
		}
		out = append(out, d)
	}
	return out, nil
}

func (s *Server) requireCatalog(w http.ResponseWriter, r *http.Request, mutation bool) (string, bool) {
	if s.Collab != nil {
		sess, ok := s.requireAccount(w, r, mutation, false)
		if !ok {
			return "", false
		}
		return sess.User.ID, true
	}
	if s.Mode == ModePublic && !s.isPresenter(r) {
		jsonErr(w, fmt.Errorf("presenter auth required"), http.StatusUnauthorized)
		return "", false
	}
	return "local", true
}

func (s *Server) handleCatalog(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.requireCatalog(w, r, false)
	if !ok {
		return
	}
	if s.Collab != nil {
		out, err := s.Collab.Catalog(r.Context(), userID)
		if err != nil {
			jsonErr(w, err, 500)
			return
		}
		writeJSON(w, out)
		return
	}
	decks, err := s.localCatalogDecks()
	if err != nil {
		jsonErr(w, err, 500)
		return
	}
	store, err := s.localCatalogStore()
	if err != nil {
		jsonErr(w, err, 500)
		return
	}
	writeJSON(w, store.Snapshot(decks))
}

func (s *Server) handleCreateFolder(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.requireCatalog(w, r, true)
	if !ok {
		return
	}
	var req struct{ Name string }
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<14)).Decode(&req); err != nil {
		jsonErr(w, err, 400)
		return
	}
	var f catalog.Folder
	var err error
	if s.Collab != nil {
		f, err = s.Collab.CreateFolder(r.Context(), userID, req.Name)
	} else {
		var store *catalog.LocalStore
		store, err = s.localCatalogStore()
		if err == nil {
			f, err = store.CreateFolder(req.Name)
		}
	}
	if err != nil {
		jsonErr(w, err, 400)
		return
	}
	w.WriteHeader(http.StatusCreated)
	writeJSON(w, f)
}

func (s *Server) handleRenameFolder(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.requireCatalog(w, r, true)
	if !ok {
		return
	}
	var req struct{ Name string }
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<14)).Decode(&req); err != nil {
		jsonErr(w, err, 400)
		return
	}
	var f catalog.Folder
	var err error
	if s.Collab != nil {
		f, err = s.Collab.RenameFolder(r.Context(), userID, r.PathValue("id"), req.Name)
	} else {
		var store *catalog.LocalStore
		store, err = s.localCatalogStore()
		if err == nil {
			f, err = store.RenameFolder(r.PathValue("id"), req.Name)
		}
	}
	if err != nil {
		status := 400
		if errors.Is(err, os.ErrNotExist) || errors.Is(err, sql.ErrNoRows) {
			status = 404
		}
		jsonErr(w, err, status)
		return
	}
	writeJSON(w, f)
}

func (s *Server) handleDeleteFolder(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.requireCatalog(w, r, true)
	if !ok {
		return
	}
	var err error
	if s.Collab != nil {
		err = s.Collab.DeleteFolder(r.Context(), userID, r.PathValue("id"))
	} else {
		var store *catalog.LocalStore
		store, err = s.localCatalogStore()
		if err == nil {
			err = store.DeleteFolder(r.PathValue("id"))
		}
	}
	if err != nil {
		jsonErr(w, err, 404)
		return
	}
	writeJSON(w, map[string]string{"status": "ok"})
}

func (s *Server) handleReorderFolders(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.requireCatalog(w, r, true)
	if !ok {
		return
	}
	var req struct {
		FolderIDs []string `json:"folder_ids"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&req); err != nil {
		jsonErr(w, err, 400)
		return
	}
	var err error
	if s.Collab != nil {
		err = s.Collab.ReorderFolders(r.Context(), userID, req.FolderIDs)
	} else {
		var store *catalog.LocalStore
		store, err = s.localCatalogStore()
		if err == nil {
			err = store.ReorderFolders(req.FolderIDs)
		}
	}
	if err != nil {
		jsonErr(w, err, 400)
		return
	}
	writeJSON(w, map[string]string{"status": "ok"})
}

func (s *Server) handleMoveDeckFolder(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.requireCatalog(w, r, true)
	if !ok {
		return
	}
	var req struct {
		FolderID string   `json:"folder_id"`
		DeckIDs  []string `json:"deck_ids"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&req); err != nil {
		jsonErr(w, err, 400)
		return
	}
	if len(req.DeckIDs) == 0 {
		req.DeckIDs = []string{r.PathValue("id")}
	}
	var err error
	if s.Collab != nil {
		err = s.Collab.MoveDecksToFolder(r.Context(), userID, req.DeckIDs, req.FolderID)
	} else {
		var store *catalog.LocalStore
		store, err = s.localCatalogStore()
		if err == nil {
			decks, listErr := s.localCatalogDecks()
			if listErr != nil {
				err = listErr
			} else {
				accessible := map[string]bool{}
				for _, d := range decks {
					accessible[d.ID] = true
				}
				err = store.MoveDecks(req.DeckIDs, req.FolderID, accessible)
			}
		}
	}
	if err != nil {
		jsonErr(w, err, 400)
		return
	}
	writeJSON(w, map[string]string{"status": "ok"})
}

var catalogTitleRe = regexp.MustCompile(`(?is)<(?:div|h[1-6])[^>]*class=["'][^"']*\bs-title\b[^"']*["'][^>]*>(.*?)</(?:div|h[1-6])>`)
var catalogTagRe = regexp.MustCompile(`(?s)<[^>]+>`)

func slideCatalogTitle(id, fragment string) string {
	if m := catalogTitleRe.FindStringSubmatch(fragment); len(m) == 2 {
		if title := strings.TrimSpace(catalogTagRe.ReplaceAllString(m[1], "")); title != "" {
			return title
		}
	}
	return strings.ReplaceAll(strings.TrimPrefix(id[strings.IndexByte(id, '-')+1:], "-"), "-", " ")
}

func (s *Server) resolveAppDeck(r *http.Request, userID, id string) (catalog.Deck, error) {
	if s.Collab != nil {
		d, err := s.Collab.DeckByID(r.Context(), id)
		if err != nil || !s.Collab.Can(r.Context(), userID, d, "view") {
			return catalog.Deck{}, sql.ErrNoRows
		}
		return catalog.Deck{ID: d.ID, StorageKey: d.StorageKey, Title: d.Title, OwnerUserID: d.OwnerUserID, OwnerName: d.OwnerName, Visibility: d.Visibility, Owned: d.OwnerUserID == userID}, nil
	}
	if !studio.ValidDeckName(id) {
		return catalog.Deck{}, sql.ErrNoRows
	}
	meta, err := s.St.LoadDeckMeta(id)
	if err != nil {
		return catalog.Deck{}, err
	}
	return catalog.Deck{ID: id, StorageKey: id, Title: meta.Title, Owned: true, Visibility: meta.Visibility}, nil
}

func (s *Server) handleAppDeckSlides(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.requireCatalog(w, r, false)
	if !ok {
		return
	}
	deck, err := s.resolveAppDeck(r, userID, r.PathValue("id"))
	if err != nil {
		jsonErr(w, fmt.Errorf("presentation not found"), 404)
		return
	}
	ids, err := s.St.SlideIDs(deck.StorageKey)
	if err != nil {
		jsonErr(w, err, 500)
		return
	}
	type slideItem struct {
		ID     string `json:"id"`
		Title  string `json:"title"`
		Number int    `json:"number"`
		Linked bool   `json:"linked"`
		Parked bool   `json:"parked"`
	}
	out := make([]slideItem, 0, len(ids))
	for i, id := range ids {
		fragment, _, err := s.St.ReadSlide(deck.StorageKey, id)
		if err != nil {
			continue
		}
		out = append(out, slideItem{ID: id, Title: slideCatalogTitle(id, fragment), Number: i + 1, Linked: s.St.IsLinkedSlide(deck.StorageKey, id), Parked: studio.SlideParked(fragment)})
	}
	writeJSON(w, map[string]any{"deck": deck, "slides": out})
}

// handleAppDeckThumbnail serves only an already-generated first-slide cache
// artifact. Directory browsing must never start Chromium or expose slide HTML
// on the account/app origin.
func (s *Server) handleAppDeckThumbnail(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.requireCatalog(w, r, false)
	if !ok {
		return
	}
	deck, err := s.resolveAppDeck(r, userID, r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	ids, err := s.St.SlideIDs(deck.StorageKey)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	first := ""
	for _, id := range ids {
		fragment, _, readErr := s.St.ReadSlide(deck.StorageKey, id)
		if readErr == nil && !studio.SlideParked(fragment) {
			first = id
			break
		}
	}
	if first == "" {
		http.NotFound(w, r)
		return
	}
	base := s.powerpointCacheDir(deck.StorageKey)
	manifest := readPowerPointManifest(base)
	entry, exists := manifest.Entries[cacheKey("visual", first)]
	fingerprint, fpErr := s.slidePowerPointFingerprint(deck.StorageKey, first, "visual")
	if !exists || fpErr != nil || entry.Fingerprint != fingerprint {
		http.NotFound(w, r)
		return
	}
	body, err := os.ReadFile(filepath.Join(base, filepath.FromSlash(entry.File)))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "private, max-age=300")
	w.Header().Set("ETag", `"`+fingerprint+`"`)
	_, _ = w.Write(body)
}

func (s *Server) canMutateLocalCatalogContent(r *http.Request) bool {
	if s.Mode == ModeStudio {
		return true
	}
	return s.Mode == ModePublic && s.isPresenter(r) && s.ContentSync.Editable()
}

func (s *Server) handleSlideTransfer(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.requireCatalog(w, r, true)
	if !ok {
		return
	}
	var req struct {
		SourceDeckID string   `json:"source_deck_id"`
		TargetDeckID string   `json:"target_deck_id"`
		SlideIDs     []string `json:"slide_ids"`
		Mode         string   `json:"mode"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		jsonErr(w, err, 400)
		return
	}
	source, err := s.resolveAppDeck(r, userID, req.SourceDeckID)
	if err != nil {
		jsonErr(w, fmt.Errorf("source presentation not found"), 404)
		return
	}
	target, err := s.resolveAppDeck(r, userID, req.TargetDeckID)
	if err != nil || !target.Owned {
		jsonErr(w, fmt.Errorf("target presentation must be owned by you"), 403)
		return
	}
	if s.Collab != nil {
		sourceRecord, _ := s.Collab.DeckByID(r.Context(), source.ID)
		targetRecord, _ := s.Collab.DeckByID(r.Context(), target.ID)
		if !s.Collab.Can(r.Context(), userID, sourceRecord, "fork") || !s.Collab.Can(r.Context(), userID, targetRecord, "edit") || !s.ContentSync.Editable() {
			jsonErr(w, fmt.Errorf("slide transfer access denied"), 403)
			return
		}
	} else if !s.canMutateLocalCatalogContent(r) {
		jsonErr(w, fmt.Errorf("slide transfer requires editable studio mode"), 403)
		return
	}
	result, err := s.St.TransferSlides(studio.SlideTransferRequest{
		SourceDeckID: source.ID, SourceDeck: source.StorageKey, SourceDeckTitle: source.Title,
		TargetDeck: target.StorageKey, SlideIDs: req.SlideIDs, Mode: req.Mode,
	})
	if err != nil {
		jsonErr(w, err, 400)
		return
	}
	if s.Collab != nil {
		_ = s.Collab.Audit(r.Context(), userID, "slide.transfer."+req.Mode, target.ID, "", map[string]any{"source_deck_id": source.ID, "slide_count": len(result.SlideIDs)})
	}
	s.ContentSync.Notify()
	s.Broadcast("reload")
	w.WriteHeader(http.StatusCreated)
	writeJSON(w, result)
}

func (s *Server) linkedSourceAuthorized(r *http.Request, deck, id string) (*studio.SlideLink, bool) {
	link, err := s.St.ReadSlideLink(deck, id)
	if err != nil {
		return nil, false
	}
	if s.Collab == nil {
		_, _, err := s.St.ReadSlide(link.SourceDeck, link.SourceSlide)
		return link, err == nil
	}
	target, err := s.Collab.DeckByStorage(r.Context(), deck)
	if err != nil {
		return link, false
	}
	// Links are a property of the target owner's deck. Shared viewers may use
	// the retained snapshot, but their own access must not make the snapshot
	// fresher or staler than it is for the owner.
	ownerID := target.OwnerUserID
	var sourceOK bool
	if link.SourceDeckID != "" {
		if source, err := s.Collab.DeckByID(r.Context(), link.SourceDeckID); err == nil {
			sourceOK = s.Collab.Can(r.Context(), ownerID, source, "fork")
		}
	} else if source, err := s.Collab.DeckByStorage(r.Context(), link.SourceDeck); err == nil {
		sourceOK = s.Collab.Can(r.Context(), ownerID, source, "fork")
	}
	if sourceOK {
		_, _, err = s.St.ReadSlide(link.SourceDeck, link.SourceSlide)
		sourceOK = err == nil
	}
	return link, sourceOK
}

func (s *Server) handleRefreshSlideLink(w http.ResponseWriter, r *http.Request) {
	deck, id := r.PathValue("deck"), r.PathValue("id")
	link, ok := s.linkedSourceAuthorized(r, deck, id)
	if link == nil {
		jsonErr(w, fmt.Errorf("slide is not linked"), 404)
		return
	}
	if !ok {
		jsonErr(w, fmt.Errorf("source unavailable; retained snapshot was not changed"), http.StatusConflict)
		return
	}
	changed, err := s.St.RefreshSlideLink(deck, id)
	if err != nil {
		jsonErr(w, err, 409)
		return
	}
	writeJSON(w, map[string]any{"status": "ok", "changed": changed, "link": link})
}

func (s *Server) handleDetachSlideLink(w http.ResponseWriter, r *http.Request) {
	if err := s.St.DetachSlideLink(r.PathValue("deck"), r.PathValue("id")); err != nil {
		jsonErr(w, err, 400)
		return
	}
	writeJSON(w, map[string]string{"status": "ok"})
}

func (s *Server) refreshDeckLinks(r *http.Request, deck string) bool {
	ids, _ := s.St.SlideIDs(deck)
	changed := false
	for _, id := range ids {
		if !s.St.IsLinkedSlide(deck, id) {
			continue
		}
		_, ok := s.linkedSourceAuthorized(r, deck, id)
		if !ok {
			continue
		}
		if did, err := s.St.RefreshSlideLink(deck, id); err == nil && did {
			changed = true
		}
	}
	if changed {
		s.ContentSync.Notify()
		s.Broadcast("reload")
	}
	return changed
}

func linkStatus(s *Server, r *http.Request, deck, id string) map[string]any {
	link, ok := s.linkedSourceAuthorized(r, deck, id)
	if link == nil {
		return nil
	}
	stale := !ok || !s.St.SlideLinkCurrent(link)
	return map[string]any{"source_deck_id": link.SourceDeckID, "source_deck": link.SourceDeck, "source_deck_title": link.SourceDeckTitle, "source_slide": link.SourceSlide, "last_refreshed_at": link.LastRefreshedAt, "stale": stale}
}
