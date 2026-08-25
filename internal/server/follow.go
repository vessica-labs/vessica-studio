package server

import (
	"bytes"
	"crypto/hmac"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/vessica-labs/vessica-studio/internal/studio"
)

type presentingState struct {
	Index int
	Seq   int64
}

type presentingUpdate struct {
	Index int   `json:"index"`
	Seq   int64 `json:"seq,omitempty"`
}

func (s *Server) setPresenting(deck string, index int, seq int64) bool {
	if seq == 0 {
		seq = time.Now().UnixNano()
	}
	s.mu.Lock()
	current, exists := s.presenting[deck]
	if exists && current.Seq >= seq {
		s.mu.Unlock()
		return false
	}
	s.presenting[deck] = presentingState{Index: index, Seq: seq}
	s.mu.Unlock()
	s.Broadcast("follow|" + deck + "|" + strconv.Itoa(index))
	return true
}

func (s *Server) presentingFor(deck string) (presentingState, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, ok := s.presenting[deck]
	return state, ok
}

func (s *Server) validatePresenting(deck string, index int) error {
	if !studio.ValidDeckName(deck) {
		return fmt.Errorf("invalid deck")
	}
	ids, err := s.St.SlideIDs(deck)
	if err != nil {
		return err
	}
	if index < 0 || index >= len(ids) {
		return fmt.Errorf("slide index out of range")
	}
	return nil
}

func (s *Server) relayPresenting(r *http.Request, deck string, update presentingUpdate) error {
	// The production public server is already the audience event hub. A local
	// public-mode preview, however, still needs to relay to that hub; loopback
	// distinguishes it from Railway edge traffic.
	if (s.Mode == ModePublic && !isLoopback(r)) || strings.TrimSpace(s.St.Config.PublicHost) == "" {
		return nil
	}
	body, _ := json.Marshal(update)
	base := strings.TrimRight(s.St.Config.PublicHost, "/")
	target := base + "/api/deck/" + url.PathEscape(deck) + "/presenting-relay"
	req, err := http.NewRequest(http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-VSTD-Presenting-Signature", s.sign(fmt.Sprintf("presenting|%s|%d|%d", deck, update.Index, update.Seq)))
	client := &http.Client{Timeout: 4 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("hosted relay returned %s: %s", resp.Status, strings.TrimSpace(string(msg)))
	}
	return nil
}

func (s *Server) handlePresentingRelay(w http.ResponseWriter, r *http.Request) {
	if s.Mode != ModePublic {
		http.NotFound(w, r)
		return
	}
	deck := r.PathValue("deck")
	var update presentingUpdate
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<12)).Decode(&update); err != nil {
		jsonErr(w, err, http.StatusBadRequest)
		return
	}
	if err := s.validatePresenting(deck, update.Index); err != nil {
		jsonErr(w, err, http.StatusBadRequest)
		return
	}
	delta := time.Since(time.Unix(0, update.Seq))
	if update.Seq == 0 || delta < -2*time.Minute || delta > 2*time.Minute {
		jsonErr(w, fmt.Errorf("stale presenting relay"), http.StatusUnauthorized)
		return
	}
	want := s.sign(fmt.Sprintf("presenting|%s|%d|%d", deck, update.Index, update.Seq))
	if !hmac.Equal([]byte(r.Header.Get("X-VSTD-Presenting-Signature")), []byte(want)) {
		jsonErr(w, fmt.Errorf("invalid presenting relay signature"), http.StatusUnauthorized)
		return
	}
	s.setPresenting(deck, update.Index, update.Seq)
	writeJSON(w, map[string]string{"status": "ok"})
}
