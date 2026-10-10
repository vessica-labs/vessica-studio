package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"time"

	"github.com/vessica-labs/vessica-studio/internal/avatar"
)

var avatarRequestID = regexp.MustCompile(`^[a-zA-Z0-9-]{32,64}$`)

func (s *Server) handleAvatar(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !s.isPresenter(r) {
		jsonErr(w, fmt.Errorf("presenter auth required"), 401)
		return
	}
	// Cross-origin pages must never start paid renderers on localhost or with a
	// presenter's cookies. Non-browser trusted callers may omit Origin.
	if origin := r.Header.Get("Origin"); origin != "" {
		u, err := url.Parse(origin)
		if err != nil || u.Host != r.Host || (u.Scheme != "https" && u.Scheme != "http") {
			jsonErr(w, fmt.Errorf("same origin required"), 403)
			return
		}
	}
	var req struct {
		ID string `json:"id"`
	}
	if json.NewDecoder(io.LimitReader(r.Body, 1024)).Decode(&req) != nil || !avatarRequestID.MatchString(req.ID) {
		jsonErr(w, fmt.Errorf("invalid avatar request"), 400)
		return
	}
	key := os.Getenv("LIVEAVATAR_API_KEY")
	if key == "" {
		jsonErr(w, fmt.Errorf("LiveAvatar is not configured"), 503)
		return
	}
	s.avatarOnce.Do(func() { s.avatars = avatar.New(avatar.Provider{Key: key}, 45*time.Second) })
	digest := sha256.Sum256([]byte(r.Header.Get("Cookie")))
	owner := hex.EncodeToString(digest[:])
	switch r.PathValue("action") {
	case "start":
		room, err := s.avatars.Start(owner, req.ID)
		if err != nil {
			jsonErr(w, err, 502)
			return
		}
		writeJSON(w, room)
	case "stop":
		if err := s.avatars.Stop(owner, req.ID); err != nil {
			jsonErr(w, err, 502)
			return
		}
		writeJSON(w, map[string]bool{"stopped": true})
	case "keepalive":
		if err := s.avatars.KeepAlive(owner, req.ID); err != nil {
			jsonErr(w, err, 404)
			return
		}
		writeJSON(w, map[string]bool{"alive": true})
	default:
		http.NotFound(w, r)
	}
}
