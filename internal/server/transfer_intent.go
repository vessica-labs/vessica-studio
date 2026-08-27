package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type slideTransferIntent struct {
	UserID   string
	DeckID   string
	SlideIDs []string
	Expires  time.Time
}

func transferIntentKey(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

func (s *Server) handleCreateSlideTransferIntent(w http.ResponseWriter, r *http.Request) {
	deck := r.PathValue("deck")
	var req struct {
		SlideIDs []string `json:"slide_ids"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil || len(req.SlideIDs) == 0 || len(req.SlideIDs) > 200 {
		jsonErr(w, fmt.Errorf("select between 1 and 200 slides"), http.StatusBadRequest)
		return
	}
	userID, deckID := "local", deck
	if s.Collab != nil {
		ps, ok := s.playerSessionForDeck(r, deck)
		if !ok || !s.Collab.Can(r.Context(), ps.User.ID, ps.Deck, "fork") {
			jsonErr(w, fmt.Errorf("source presentation access denied"), http.StatusForbidden)
			return
		}
		userID, deckID = ps.User.ID, ps.Deck.ID
	} else if !s.canView(r, deck) || (s.Mode != ModeStudio && !s.isPresenter(r)) {
		jsonErr(w, fmt.Errorf("presenter access required"), http.StatusForbidden)
		return
	}
	available, err := s.St.SlideIDs(deck)
	if err != nil {
		jsonErr(w, err, http.StatusNotFound)
		return
	}
	wanted := map[string]bool{}
	for _, id := range req.SlideIDs {
		if wanted[id] {
			jsonErr(w, fmt.Errorf("duplicate slide %q", id), http.StatusBadRequest)
			return
		}
		wanted[id] = true
	}
	ordered := make([]string, 0, len(wanted))
	for _, id := range available {
		if wanted[id] {
			ordered = append(ordered, id)
		}
	}
	if len(ordered) != len(wanted) {
		jsonErr(w, fmt.Errorf("one or more selected slides no longer exist"), http.StatusBadRequest)
		return
	}
	raw := randToken(32)
	s.mu.Lock()
	for key, intent := range s.transferIntents {
		if time.Now().After(intent.Expires) {
			delete(s.transferIntents, key)
		}
	}
	s.transferIntents[transferIntentKey(raw)] = slideTransferIntent{UserID: userID, DeckID: deckID, SlideIDs: ordered, Expires: time.Now().Add(5 * time.Minute)}
	s.mu.Unlock()
	base := strings.TrimRight(s.appOrigin, "/")
	if base == "" {
		base = "/presentations"
	} else {
		base += "/presentations"
	}
	writeJSON(w, map[string]string{"url": base + "#transfer=" + raw})
}

func (s *Server) handleSlideTransferIntentExchange(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.requireCatalog(w, r, true)
	if !ok {
		return
	}
	var req struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<14)).Decode(&req); err != nil || req.Token == "" {
		jsonErr(w, fmt.Errorf("transfer intent token required"), http.StatusBadRequest)
		return
	}
	key := transferIntentKey(req.Token)
	s.mu.Lock()
	intent, found := s.transferIntents[key]
	delete(s.transferIntents, key) // always single use, including failed exchange
	s.mu.Unlock()
	if !found || time.Now().After(intent.Expires) || intent.UserID != userID {
		jsonErr(w, fmt.Errorf("transfer intent is invalid or expired"), http.StatusUnauthorized)
		return
	}
	writeJSON(w, map[string]any{"source_deck_id": intent.DeckID, "slide_ids": intent.SlideIDs})
}
