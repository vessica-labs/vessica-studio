package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/vessica-labs/vessica-studio/internal/catalog"
	"github.com/vessica-labs/vessica-studio/internal/collab"
	"github.com/vessica-labs/vessica-studio/internal/studio"
)

const (
	accountSessionCookie = "vstd_account"
	playerMediaCookie    = "vstd_player_media"
)

func (s *Server) StartCollaboration(ctx context.Context) error {
	if os.Getenv("VSTD_COLLABORATION") != "1" {
		return nil
	}
	if s.Mode != ModePublic {
		return fmt.Errorf("VSTD_COLLABORATION requires public serving mode")
	}
	appOrigin := strings.TrimRight(strings.TrimSpace(os.Getenv("VSTD_APP_ORIGIN")), "/")
	playerOrigin := strings.TrimRight(strings.TrimSpace(os.Getenv("VSTD_PLAYER_ORIGIN")), "/")
	if appOrigin == "" {
		appOrigin = strings.TrimRight(strings.TrimSpace(s.St.Config.AppHost), "/")
	}
	if playerOrigin == "" {
		playerOrigin = strings.TrimRight(strings.TrimSpace(s.St.Config.PublicHost), "/")
	}
	if err := validateOrigin(appOrigin, s.Mode); err != nil {
		return fmt.Errorf("VSTD_APP_ORIGIN/app_host: %w", err)
	}
	if err := validateOrigin(playerOrigin, s.Mode); err != nil {
		return fmt.Errorf("VSTD_PLAYER_ORIGIN/public_host: %w", err)
	}
	if appOrigin == playerOrigin {
		return fmt.Errorf("collaboration requires separate app and player origins")
	}
	store, err := collab.Open(ctx, os.Getenv("DATABASE_URL"), os.Getenv("VSTD_OWNER_GITHUB_LOGIN"))
	if err != nil {
		return err
	}
	s.Collab = store
	s.appOrigin = appOrigin
	s.playerOrigin = playerOrigin
	s.St.Config.AppHost = appOrigin
	s.St.Config.PublicHost = playerOrigin
	if err := store.ReconcileDecks(ctx, s.filesystemDecks()); err != nil {
		store.Close()
		s.Collab = nil
		return fmt.Errorf("reconcile presentations: %w", err)
	}
	return nil
}

func validateOrigin(raw string, mode Mode) error {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("must be an origin such as https://studio.example.com")
	}
	hostname := strings.ToLower(u.Hostname())
	localHTTP := u.Scheme == "http" && (hostname == "localhost" || strings.HasSuffix(hostname, ".localhost") || net.ParseIP(hostname).IsLoopback())
	if mode == ModePublic && u.Scheme != "https" && !localHTTP {
		return fmt.Errorf("must use https in public mode")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("unsupported scheme")
	}
	return nil
}

func (s *Server) filesystemDecks() map[string]string {
	out := map[string]string{}
	decks, _ := s.St.ListDecks()
	for _, key := range decks {
		title := key
		if m, err := s.St.LoadDeckMeta(key); err == nil && strings.TrimSpace(m.Title) != "" {
			title = m.Title
		}
		out[key] = title
	}
	return out
}

func originHost(raw string) string {
	u, _ := url.Parse(raw)
	return strings.ToLower(u.Hostname())
}

func requestHost(r *http.Request) string {
	h := r.Host
	if host, _, err := net.SplitHostPort(h); err == nil {
		h = host
	}
	return strings.ToLower(strings.Trim(h, "[]"))
}

func (s *Server) isAppRequest(r *http.Request) bool {
	return s.Collab != nil && requestHost(r) == originHost(s.appOrigin)
}

func (s *Server) isPlayerRequest(r *http.Request) bool {
	return s.Collab != nil && requestHost(r) == originHost(s.playerOrigin)
}

func (s *Server) hostDispatch(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			next.ServeHTTP(w, r)
			return
		}
		// PDF/PPTX rendering launches a loopback Chrome process that loads the
		// one-time print route and its media through 127.0.0.1. Route it without
		// weakening external Host validation; the handlers still enforce their
		// print key or normal authorization.
		if isLoopback(r) {
			next.ServeHTTP(w, r)
			return
		}
		switch {
		case s.isAppRequest(r):
			if redirectLegacyPlayerPath(s, w, r) {
				return
			}
			if playerOnlyPath(r.URL.Path) {
				http.NotFound(w, r)
				return
			}
			next.ServeHTTP(w, r)
		case s.isPlayerRequest(r):
			if appOnlyPath(r.URL.Path) {
				http.NotFound(w, r)
				return
			}
			next.ServeHTTP(w, r)
		default:
			if r.URL.Path == "/" && (r.Method == http.MethodGet || r.Method == http.MethodHead) {
				http.Redirect(w, r, s.appOrigin+"/", http.StatusFound)
				return
			}
			http.Error(w, "unrecognized host", http.StatusMisdirectedRequest)
		}
	})
}

func appOnlyPath(path string) bool {
	return path == "/" || path == "/presentations" || path == "/team" || path == "/observability" || strings.HasPrefix(path, "/site/") ||
		strings.HasPrefix(path, "/auth/") || strings.HasPrefix(path, "/api/auth/") ||
		strings.HasPrefix(path, "/api/app/") || path == "/api/decks" || path == "/api/contact"
}

func playerOnlyPath(path string) bool {
	for _, prefix := range []string{"/d/", "/library/", "/assets/", "/api/deck/", "/api/player/", "/api/events", "/api/me", "/api/agent/", "/api/realtime/", "/api/observability/", "/api/vessica/", "/api/chat/", "/api/telnyx/", "/api/asset/", "/api/content-sync/", "/session"} {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return path == "/follow" || strings.HasPrefix(path, "/v/") || strings.HasPrefix(path, "/chat/")
}

func redirectLegacyPlayerPath(s *Server, w http.ResponseWriter, r *http.Request) bool {
	if r.URL.Path == "/follow" || strings.HasPrefix(r.URL.Path, "/v/") || strings.HasPrefix(r.URL.Path, "/chat/") {
		target := s.playerOrigin + r.URL.RequestURI()
		http.Redirect(w, r, target, http.StatusFound)
		return true
	}
	return false
}

func (s *Server) accountSession(r *http.Request) (collab.Session, bool) {
	if s.Collab == nil || (s.Mode == ModePublic && !s.isAppRequest(r)) {
		return collab.Session{}, false
	}
	c, err := r.Cookie(accountSessionCookie)
	if err != nil {
		return collab.Session{}, false
	}
	sess, err := s.Collab.Session(r.Context(), c.Value)
	return sess, err == nil
}

func bearerToken(r *http.Request) string {
	const prefix = "Bearer "
	v := r.Header.Get("Authorization")
	if !strings.HasPrefix(v, prefix) {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(v, prefix))
}

func (s *Server) playerSession(r *http.Request) (collab.PlayerSession, bool) {
	if s.Collab == nil || (s.Mode == ModePublic && !s.isPlayerRequest(r)) {
		return collab.PlayerSession{}, false
	}
	raw := bearerToken(r)
	if raw == "" {
		return collab.PlayerSession{}, false
	}
	ps, err := s.Collab.PlayerSession(r.Context(), raw)
	return ps, err == nil
}

func (s *Server) playerMediaSession(r *http.Request) (collab.PlayerSession, bool) {
	if s.Collab == nil || (s.Mode == ModePublic && !s.isPlayerRequest(r)) {
		return collab.PlayerSession{}, false
	}
	c, err := r.Cookie(playerMediaCookie)
	if err != nil {
		return collab.PlayerSession{}, false
	}
	ps, err := s.Collab.PlayerSession(r.Context(), c.Value)
	return ps, err == nil
}

func (s *Server) playerSessionForDeck(r *http.Request, deck string) (collab.PlayerSession, bool) {
	ps, ok := s.playerSession(r)
	return ps, ok && ps.Deck.StorageKey == deck
}

func (s *Server) nativePlayerSessionForDeck(r *http.Request, deck string) (collab.PlayerSession, bool) {
	if ps, ok := s.playerSessionForDeck(r, deck); ok {
		return ps, true
	}
	ps, ok := s.playerMediaSession(r)
	return ps, ok && ps.Deck.StorageKey == deck
}

func (s *Server) requireAccount(w http.ResponseWriter, r *http.Request, mutation, owner bool) (collab.Session, bool) {
	sess, ok := s.accountSession(r)
	if !ok {
		jsonErr(w, fmt.Errorf("authentication required"), http.StatusUnauthorized)
		return collab.Session{}, false
	}
	if owner && !s.Collab.CanUser(r.Context(), sess.User.ID, "administer_team") {
		jsonErr(w, fmt.Errorf("owner access required"), http.StatusForbidden)
		return collab.Session{}, false
	}
	if mutation && !s.validAppMutation(r, sess) {
		jsonErr(w, fmt.Errorf("request integrity check failed"), http.StatusForbidden)
		return collab.Session{}, false
	}
	return sess, true
}

func (s *Server) validAppOrigin(r *http.Request) bool {
	return s.Collab == nil || r.Header.Get("Origin") == s.appOrigin
}

func (s *Server) validAppMutation(r *http.Request, sess collab.Session) bool {
	return s.validAppOrigin(r) && sess.CSRF != "" && r.Header.Get("X-CSRF-Token") == sess.CSRF
}

func (s *Server) setAccountCookie(w http.ResponseWriter, r *http.Request, raw string) {
	http.SetCookie(w, &http.Cookie{Name: accountSessionCookie, Value: raw, Path: "/", HttpOnly: true,
		Secure: strings.HasPrefix(s.appOrigin, "https://") || r.TLS != nil, SameSite: http.SameSiteLaxMode,
		Expires: time.Now().Add(30 * 24 * time.Hour), MaxAge: 30 * 24 * 60 * 60})
}

func (s *Server) clearAccountCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: accountSessionCookie, Path: "/", HttpOnly: true, Secure: strings.HasPrefix(s.appOrigin, "https://"),
		SameSite: http.SameSiteLaxMode, MaxAge: -1, Expires: time.Unix(1, 0)})
}

func (s *Server) handlePasswordLogin(w http.ResponseWriter, r *http.Request) {
	if s.Collab == nil {
		http.NotFound(w, r)
		return
	}
	if !s.validAppOrigin(r) {
		jsonErr(w, fmt.Errorf("request origin rejected"), http.StatusForbidden)
		return
	}
	var req struct{ Email, Password string }
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		jsonErr(w, err, http.StatusBadRequest)
		return
	}
	u, err := s.Collab.LoginPassword(r.Context(), req.Email, req.Password)
	if err != nil {
		jsonErr(w, fmt.Errorf("invalid email or password"), http.StatusUnauthorized)
		return
	}
	raw, sess, err := s.Collab.CreateSession(r.Context(), u.ID)
	if err != nil {
		jsonErr(w, err, http.StatusInternalServerError)
		return
	}
	s.setAccountCookie(w, r, raw)
	_ = s.Collab.Audit(r.Context(), u.ID, "auth.password_login", "", "", nil)
	writeJSON(w, map[string]any{"status": "completed", "user": sess.User, "csrf": sess.CSRF})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if s.Collab == nil {
		s.clearAccountCookie(w)
		writeJSON(w, map[string]string{"status": "ok"})
		return
	}
	sess, ok := s.requireAccount(w, r, true, false)
	if !ok {
		return
	}
	if c, err := r.Cookie(accountSessionCookie); err == nil {
		_ = s.Collab.RevokeSession(r.Context(), c.Value)
	}
	_ = s.Collab.RevokePlayerSessions(r.Context(), sess.User.ID)
	s.clearAccountCookie(w)
	_ = s.Collab.Audit(r.Context(), sess.User.ID, "auth.logout", "", "", nil)
	writeJSON(w, map[string]string{"status": "ok"})
}

func (s *Server) handleAcceptInvitation(w http.ResponseWriter, r *http.Request) {
	if s.Collab == nil {
		http.NotFound(w, r)
		return
	}
	if !s.validAppOrigin(r) {
		jsonErr(w, fmt.Errorf("request origin rejected"), http.StatusForbidden)
		return
	}
	var req struct{ Token, Name, Password string }
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		jsonErr(w, err, http.StatusBadRequest)
		return
	}
	u, err := s.Collab.AcceptInvitation(r.Context(), req.Token, req.Name, req.Password)
	if err != nil {
		jsonErr(w, fmt.Errorf("invitation is invalid, expired, or already used"), http.StatusBadRequest)
		return
	}
	raw, sess, err := s.Collab.CreateSession(r.Context(), u.ID)
	if err != nil {
		jsonErr(w, err, http.StatusInternalServerError)
		return
	}
	s.setAccountCookie(w, r, raw)
	writeJSON(w, map[string]any{"status": "completed", "user": sess.User, "csrf": sess.CSRF})
}

func (s *Server) handleForgotPassword(w http.ResponseWriter, r *http.Request) {
	if s.Collab == nil {
		http.NotFound(w, r)
		return
	}
	if !s.validAppOrigin(r) {
		jsonErr(w, fmt.Errorf("request origin rejected"), http.StatusForbidden)
		return
	}
	var req struct{ Email string }
	_ = json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&req)
	if raw, u, err := s.Collab.CreatePasswordReset(r.Context(), req.Email); err == nil {
		_ = s.Collab.Audit(r.Context(), u.ID, "auth.password_reset_request", "", u.ID, nil)
		link := s.appOrigin + "/auth/login#reset=" + raw
		_ = s.sendSystemEmail(r.Context(), u.Email, "Reset your Vessica Studio password", "Use this link within one hour to reset your password:\n\n"+link)
	}
	writeJSON(w, map[string]string{"status": "ok"})
}

func (s *Server) handleResetPassword(w http.ResponseWriter, r *http.Request) {
	if s.Collab == nil {
		http.NotFound(w, r)
		return
	}
	if !s.validAppOrigin(r) {
		jsonErr(w, fmt.Errorf("request origin rejected"), http.StatusForbidden)
		return
	}
	var req struct{ Token, Password string }
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		jsonErr(w, err, http.StatusBadRequest)
		return
	}
	if err := s.Collab.ResetPassword(r.Context(), req.Token, req.Password); err != nil {
		jsonErr(w, fmt.Errorf("reset link is invalid, expired, or already used"), http.StatusBadRequest)
		return
	}
	writeJSON(w, map[string]string{"status": "ok"})
}

func (s *Server) handleAcceptInvitePage(w http.ResponseWriter, r *http.Request) {
	if s.Collab == nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	io.WriteString(w, authShell("Accept invitation", `<form id="accept"><input name="name" autocomplete="name" placeholder="Your name" required><input name="password" type="password" autocomplete="new-password" placeholder="Create password (12+ characters)" minlength="12" required><button>Join the team</button></form><div id="err"></div>`, `
const token=location.hash.slice(1);document.getElementById('accept').onsubmit=async e=>{e.preventDefault();const f=new FormData(e.target);const r=await fetch('/api/auth/invitations/accept',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({token,name:f.get('name'),password:f.get('password')})});const j=await r.json();if(r.ok)location.replace('/presentations');else document.getElementById('err').textContent=j.error||'Could not accept invitation';};`))
}

func authShell(title, body, script string) string {
	return authDocument(title, "auth", `<h1>`+html.EscapeString(title)+`</h1>`+body, script, "")
}

func authDocument(title, mainClass, body, script, extraCSS string) string {
	return `<!DOCTYPE html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><meta name="referrer" content="no-referrer"><title>` + html.EscapeString(title) + `</title><style>` + appCSS + extraCSS + `</style></head><body><main class="` + html.EscapeString(mainClass) + `">` + body + `</main><script>` + script + `</script></body></html>`
}

func (s *Server) sendSystemEmail(ctx context.Context, to, subject, bodyText string) error {
	key, from := strings.TrimSpace(os.Getenv("RESEND_API_KEY")), strings.TrimSpace(os.Getenv("RESEND_FROM"))
	if key == "" || from == "" {
		return fmt.Errorf("email is not configured")
	}
	payload := map[string]any{"from": from, "to": []string{to}, "subject": subject, "text": bodyText}
	_, code, err := postJSON(resendSendURL, key, payload)
	if err != nil {
		return err
	}
	if code < 200 || code >= 300 {
		return fmt.Errorf("email provider returned %d", code)
	}
	return nil
}

func (s *Server) handleAppDecks(w http.ResponseWriter, r *http.Request) {
	if s.Collab == nil {
		if _, ok := s.requireCatalog(w, r, false); !ok {
			return
		}
		decks, err := s.localCatalogDecks()
		if err != nil {
			jsonErr(w, err, http.StatusInternalServerError)
			return
		}
		writeJSON(w, decks)
		return
	}
	sess, ok := s.requireAccount(w, r, false, false)
	if !ok {
		return
	}
	decks, err := s.Collab.ListDecks(r.Context(), sess.User.ID)
	if err != nil {
		jsonErr(w, err, http.StatusInternalServerError)
		return
	}
	writeJSON(w, decks)
}

func (s *Server) handleCreateDeck(w http.ResponseWriter, r *http.Request) {
	if s.Collab == nil {
		if _, ok := s.requireCatalog(w, r, true); !ok {
			return
		}
		var req struct{ Title string }
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&req); err != nil || strings.TrimSpace(req.Title) == "" {
			jsonErr(w, fmt.Errorf("title is required"), http.StatusBadRequest)
			return
		}
		base := collab.Slug(req.Title)
		key := base
		for i := 2; ; i++ {
			if _, err := os.Stat(s.St.DeckDir(key)); errors.Is(err, os.ErrNotExist) {
				break
			}
			key = fmt.Sprintf("%s-%d", base, i)
		}
		if err := s.St.NewDeck(key, strings.TrimSpace(req.Title)); err != nil {
			jsonErr(w, err, 500)
			return
		}
		w.WriteHeader(http.StatusCreated)
		writeJSON(w, catalog.Deck{ID: key, StorageKey: key, Title: strings.TrimSpace(req.Title), Owned: true, Visibility: "private"})
		return
	}
	sess, ok := s.requireAccount(w, r, true, false)
	if !ok {
		return
	}
	var req struct{ Title string }
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&req); err != nil || strings.TrimSpace(req.Title) == "" {
		jsonErr(w, fmt.Errorf("title is required"), http.StatusBadRequest)
		return
	}
	key, err := s.Collab.UniqueStorageKey(r.Context(), req.Title)
	if err != nil {
		jsonErr(w, err, http.StatusInternalServerError)
		return
	}
	if err := s.St.NewDeck(key, strings.TrimSpace(req.Title)); err != nil {
		jsonErr(w, err, http.StatusInternalServerError)
		return
	}
	d, err := s.Collab.CreateDeck(r.Context(), sess.User.ID, key, strings.TrimSpace(req.Title), "")
	if err != nil {
		_ = os.RemoveAll(s.St.DeckDir(key))
		jsonErr(w, err, http.StatusInternalServerError)
		return
	}
	s.ContentSync.Notify()
	s.Broadcast("reload")
	w.WriteHeader(http.StatusCreated)
	writeJSON(w, d)
}

func (s *Server) handleDeckVisibility(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.requireAccount(w, r, true, false)
	if !ok {
		return
	}
	var req struct{ Visibility string }
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<12)).Decode(&req); err != nil {
		jsonErr(w, err, http.StatusBadRequest)
		return
	}
	if err := s.Collab.SetVisibility(r.Context(), r.PathValue("id"), sess.User.ID, req.Visibility); err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, sql.ErrNoRows) {
			status = http.StatusForbidden
		}
		jsonErr(w, err, status)
		return
	}
	if deck, err := s.Collab.DeckByID(r.Context(), r.PathValue("id")); err == nil {
		s.syncDeckVisibilityFile(deck)
		s.ContentSync.Notify()
	}
	writeJSON(w, map[string]string{"status": "ok", "visibility": req.Visibility})
}

func (s *Server) handleForkDeck(w http.ResponseWriter, r *http.Request) {
	if s.Collab == nil {
		if _, ok := s.requireCatalog(w, r, true); !ok {
			return
		}
		source := r.PathValue("id")
		if !studio.ValidDeckName(source) {
			jsonErr(w, fmt.Errorf("presentation not found"), 404)
			return
		}
		meta, err := s.St.LoadDeckMeta(source)
		if err != nil {
			jsonErr(w, err, 404)
			return
		}
		var req struct{ Title, Slug string }
		_ = json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&req)
		if strings.TrimSpace(req.Title) == "" {
			req.Title = meta.Title + " (copy)"
		}
		key := strings.TrimSpace(req.Slug)
		if key == "" {
			base := collab.Slug(req.Title)
			key = base
			for i := 2; ; i++ {
				if _, err := os.Stat(s.St.DeckDir(key)); errors.Is(err, os.ErrNotExist) {
					break
				}
				key = fmt.Sprintf("%s-%d", base, i)
			}
		}
		if err := s.St.ForkAs(source, key, strings.TrimSpace(req.Title)); err != nil {
			jsonErr(w, err, 400)
			return
		}
		w.WriteHeader(http.StatusCreated)
		writeJSON(w, catalog.Deck{ID: key, StorageKey: key, Title: strings.TrimSpace(req.Title), Owned: true, Visibility: "private"})
		return
	}
	sess, ok := s.requireAccount(w, r, true, false)
	if !ok {
		return
	}
	source, err := s.Collab.DeckByID(r.Context(), r.PathValue("id"))
	if err != nil || !s.Collab.Can(r.Context(), sess.User.ID, source, "fork") {
		jsonErr(w, fmt.Errorf("presentation not found"), http.StatusNotFound)
		return
	}
	var req struct{ Title, Slug string }
	_ = json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&req)
	if strings.TrimSpace(req.Title) == "" {
		req.Title = source.Title + " (copy)"
	}
	key := strings.TrimSpace(req.Slug)
	if key == "" {
		key, err = s.Collab.UniqueStorageKey(r.Context(), req.Title)
	} else if available, checkErr := s.Collab.StorageKeyAvailable(r.Context(), key); checkErr != nil {
		err = checkErr
	} else if !available {
		err = fmt.Errorf("presentation slug is already in use")
	}
	if err != nil {
		jsonErr(w, err, http.StatusBadRequest)
		return
	}
	if err := s.St.ForkAs(source.StorageKey, key, strings.TrimSpace(req.Title)); err != nil {
		jsonErr(w, err, 500)
		return
	}
	d, err := s.Collab.CreateDeck(r.Context(), sess.User.ID, key, strings.TrimSpace(req.Title), source.ID)
	if err != nil {
		_ = os.RemoveAll(s.St.DeckDir(key))
		jsonErr(w, err, 500)
		return
	}
	s.ContentSync.Notify()
	s.Broadcast("reload")
	w.WriteHeader(http.StatusCreated)
	writeJSON(w, d)
}

func (s *Server) handleLaunchDeck(w http.ResponseWriter, r *http.Request) {
	if s.Collab == nil {
		if _, ok := s.requireCatalog(w, r, true); !ok {
			return
		}
		deck := r.PathValue("id")
		if !studio.ValidDeckName(deck) {
			jsonErr(w, fmt.Errorf("presentation not found"), 404)
			return
		}
		if store, err := s.localCatalogStore(); err == nil {
			store.Touch(deck)
		}
		writeJSON(w, map[string]string{"url": "/d/" + deck + "/"})
		return
	}
	sess, ok := s.requireAccount(w, r, true, false)
	if !ok {
		return
	}
	var req struct{ Mode string }
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<12)).Decode(&req); err != nil {
		jsonErr(w, err, http.StatusBadRequest)
		return
	}
	raw, err := s.Collab.CreateHandoff(r.Context(), sess.User.ID, r.PathValue("id"), req.Mode)
	if err != nil {
		jsonErr(w, fmt.Errorf("presentation access denied"), http.StatusForbidden)
		return
	}
	writeJSON(w, map[string]string{"url": s.playerOrigin + "/session#" + raw})
}

func (s *Server) handleAppTeam(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.requireAccount(w, r, false, true)
	if !ok {
		return
	}
	team, err := s.Collab.Team(r.Context(), sess.User.ID)
	if err != nil {
		jsonErr(w, err, 500)
		return
	}
	writeJSON(w, team)
}

func (s *Server) handleCreateInvitation(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.requireAccount(w, r, true, true)
	if !ok {
		return
	}
	var req struct{ Email string }
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&req); err != nil {
		jsonErr(w, err, 400)
		return
	}
	inv, raw, err := s.Collab.CreateInvitation(r.Context(), sess.User.ID, req.Email)
	if err != nil {
		jsonErr(w, err, 400)
		return
	}
	link := s.appOrigin + "/auth/accept-invite#" + raw
	if err := s.sendSystemEmail(r.Context(), inv.Email, "You're invited to Vessica Studio", "You have been invited to the Vessica Studio team. Create your account within seven days:\n\n"+link); err != nil {
		_ = s.Collab.RevokeInvitation(r.Context(), sess.User.ID, inv.ID)
		jsonErr(w, err, http.StatusBadGateway)
		return
	}
	w.WriteHeader(http.StatusCreated)
	writeJSON(w, inv)
}

func (s *Server) handleResendInvitation(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.requireAccount(w, r, true, true)
	if !ok {
		return
	}
	inv, raw, err := s.Collab.ResendInvitation(r.Context(), sess.User.ID, r.PathValue("id"))
	if err != nil {
		jsonErr(w, err, http.StatusNotFound)
		return
	}
	link := s.appOrigin + "/auth/accept-invite#" + raw
	if err := s.sendSystemEmail(r.Context(), inv.Email, "Your Vessica Studio invitation", "Create your account within seven days:\n\n"+link); err != nil {
		_ = s.Collab.RevokeInvitation(r.Context(), sess.User.ID, inv.ID)
		jsonErr(w, err, http.StatusBadGateway)
		return
	}
	writeJSON(w, inv)
}

func (s *Server) handleRevokeInvitation(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.requireAccount(w, r, true, true)
	if !ok {
		return
	}
	if err := s.Collab.RevokeInvitation(r.Context(), sess.User.ID, r.PathValue("id")); err != nil {
		jsonErr(w, err, http.StatusNotFound)
		return
	}
	writeJSON(w, map[string]string{"status": "ok"})
}

func (s *Server) handleRemoveMember(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.requireAccount(w, r, true, true)
	if !ok {
		return
	}
	if err := s.Collab.RemoveMember(r.Context(), sess.User.ID, r.PathValue("id")); err != nil {
		jsonErr(w, err, http.StatusNotFound)
		return
	}
	writeJSON(w, map[string]string{"status": "ok"})
}

const teamPageCSS = `
.team-side nav{display:grid;gap:5px}.team-side .navbtn{text-decoration:none}.team-side .navbtn>span{display:flex;align-items:center;gap:10px}.team-side .navbtn i{color:#7ccc90;font-size:15px}.team-content{max-width:1500px;width:100%}.team-top-copy{max-width:650px}.team-top-copy .meta{font-size:14px}.team-account{align-items:center}.team-account .signed-in{font-size:12px}.team-account button{gap:7px}.invite-card{display:grid;grid-template-columns:minmax(260px,.8fr) minmax(420px,1.2fr);align-items:end;gap:32px;margin:36px 0 20px;padding:26px 28px;background:linear-gradient(145deg,#12371f,#0b2716);border:1px solid #31543a;border-radius:22px;box-shadow:0 18px 48px #0003}.invite-card h2,.team-panel h2{font:400 26px/1.1 var(--display);letter-spacing:-.025em;color:#fff;margin:0}.invite-card p{color:#8eae96;line-height:1.55;margin:8px 0 0}.invite-form{display:grid;grid-template-columns:minmax(0,1fr) auto;gap:10px}.invite-field{position:relative}.invite-field i{position:absolute;left:15px;top:50%;transform:translateY(-50%);color:#76927e}.invite-field input{width:100%;min-height:48px;padding:11px 14px 11px 42px;background:#0e2819cc;color:#e8f5eb;border:1px solid #355740;border-radius:var(--control-radius);outline:none;box-shadow:inset 0 1px 0 #ffffff09}.invite-field input::placeholder{color:#76927e}.invite-field input:hover{background:#12321f;border-color:#456d51}.invite-field input:focus{background:#12321f;border-color:#7cf29a;box-shadow:0 0 0 3px #7cf29a26}.invite-form .primary{gap:7px;padding-inline:17px}.team-columns{display:grid;grid-template-columns:minmax(0,1.15fr) minmax(330px,.85fr);gap:20px}.team-panel{min-width:0;padding:23px;background:linear-gradient(150deg,#102f1b,#0b2515);border:1px solid #31543a;border-radius:22px}.team-panel-head{display:flex;align-items:center;justify-content:space-between;gap:16px;margin-bottom:16px}.team-count{display:inline-grid;place-items:center;min-width:28px;height:28px;padding:0 8px;border-radius:10px;background:#1d492b;color:#a8d8b2;font-size:11px;font-weight:600}.team-list{display:grid;gap:9px}.team-row{display:grid;grid-template-columns:38px minmax(0,1fr) auto auto;align-items:center;gap:11px;min-height:68px;padding:12px;background:#14371f;border:1px solid #2d5337;border-radius:14px;margin:0;transition:.15s background,.15s border-color}.team-row:hover{background:#183f25;border-color:#416e4c}.team-row.invitation{grid-template-columns:38px minmax(0,1fr)}.team-row.invitation .row-actions{grid-column:2}.team-row-icon{width:38px;height:38px;display:grid;place-items:center;border-radius:11px;background:#1e4b2b;color:#80e196;font-size:16px}.team-identity{min-width:0;display:grid;gap:3px}.team-identity strong{overflow:hidden;text-overflow:ellipsis;color:#f4fff6;font-size:13px}.team-identity span{overflow:hidden;text-overflow:ellipsis;color:#8eae96;font-size:11px}.role-badge{padding:5px 8px;border:1px solid #3e6848;border-radius:9px;color:#a9cbb0;font-size:10px;font-weight:600;text-transform:capitalize}.role-badge.owner{background:#244b30;color:#d3f2da}.row-actions{display:flex;align-items:center;gap:5px}.row-action{min-height:31px;padding:5px 8px;gap:5px;border-radius:10px;font-size:10px;box-shadow:none}.row-action i{font-size:12px}.empty-row{padding:28px 16px;text-align:center;border:1px dashed #31543a;border-radius:14px;color:#7f9e86;font-size:12px}.team-dialog{width:min(520px,calc(100vw - 30px))}.team-dialog .dialog-actions{margin-top:22px}.team-dialog .dialog-error{margin-top:9px}.team-footer-note{margin-top:18px;color:#6f8f76;font-size:11px;line-height:1.5}
@media(max-width:1050px){.invite-card{grid-template-columns:1fr;align-items:start}.team-columns{grid-template-columns:1fr}}@media(max-width:800px){.team-side nav{display:flex}.team-content{padding-top:28px}.team-account .signed-in{display:none}.invite-card{margin-top:26px}}@media(max-width:600px){.invite-card{padding:22px}.invite-form{grid-template-columns:1fr}.invite-form .primary{width:100%}.team-row,.team-row.invitation{grid-template-columns:38px minmax(0,1fr) auto}.team-row .role-badge{grid-column:2}.team-row .row-actions{grid-column:2/-1}.team-panel{padding:18px}.row-action span{position:absolute;width:1px;height:1px;padding:0;margin:-1px;overflow:hidden;clip:rect(0,0,0,0);white-space:nowrap;border:0}}
`

func (s *Server) handleTeamPage(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.accountSession(r)
	if !ok {
		http.Redirect(w, r, "/auth/login", http.StatusFound)
		return
	}
	if !s.Collab.CanUser(r.Context(), sess.User.ID, "administer_team") {
		http.Error(w, "owner access required", http.StatusForbidden)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	io.WriteString(w, teamPageHTML(sess.User.DisplayName, sess.CSRF))
}

func teamPageHTML(userName, csrf string) string {
	return `<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Team · Vessica Studio</title><style>` + catalogPageCSS + teamPageCSS + `</style></head><body>
<div class="app"><aside class="side team-side"><div class="brand">Vessica Studio</div><nav aria-label="Workspace navigation"><a class="navbtn" href="/presentations"><span><i class="bi bi-collection" aria-hidden="true"></i>Presentations</span></a><a class="navbtn active" href="/team" aria-current="page"><span><i class="bi bi-people" aria-hidden="true"></i>Team</span></a></nav><a class="ownerlink" href="/observability"><i class="bi bi-activity" aria-hidden="true"></i><span>Monitoring</span></a><a class="docslink" href="` + html.EscapeString(documentationURL()) + `" target="_blank" rel="noopener noreferrer"><span>Documentation</span><span>Open</span></a></aside>
<main class="content team-content"><header class="top"><div class="team-top-copy"><div class="eyebrow">Workspace</div><h1>Team</h1><div class="meta">Invite collaborators and manage who can create, edit, and share presentations.</div></div><div class="account team-account"><span class="signed-in">Signed in as ` + html.EscapeString(userName) + `</span><button class="secondary" id="logout"><i class="bi bi-box-arrow-right" aria-hidden="true"></i><span>Log out</span></button></div></header>
<section class="invite-card" aria-labelledby="inviteHeading"><div><div class="eyebrow">Grow your workspace</div><h2 id="inviteHeading">Invite a teammate</h2><p>They’ll receive a secure invitation to join this Vessica Studio workspace.</p></div><form class="invite-form" id="invite"><label class="visually-hidden" for="inviteEmail">Teammate email</label><div class="invite-field"><i class="bi bi-envelope" aria-hidden="true"></i><input id="inviteEmail" name="email" type="email" placeholder="teammate@example.com" autocomplete="email" required></div><button class="primary" id="inviteSubmit"><i class="bi bi-person-plus" aria-hidden="true"></i><span>Send invite</span></button></form></section>
<div class="team-columns"><section class="team-panel" aria-labelledby="membersTitle"><header class="team-panel-head"><h2 id="membersTitle">Members</h2><span class="team-count" id="memberCount" aria-label="Member count">0</span></header><div class="team-list" id="members"></div><div class="team-footer-note">Removing a member transfers their presentations to the workspace owner.</div></section><section class="team-panel" aria-labelledby="invitesTitle"><header class="team-panel-head"><h2 id="invitesTitle">Pending invitations</h2><span class="team-count" id="inviteCount" aria-label="Pending invitation count">0</span></header><div class="team-list" id="invites"></div></section></div></main></div>
<div class="toast" id="toast" role="status" aria-live="polite"></div>
<dialog class="team-dialog" id="memberDialog"><div class="eyebrow">Team access</div><h2>Remove team member?</h2><p class="dialog-copy" id="memberCopy">Their presentations will transfer to you. This cannot be undone.</p><div class="dialog-error" id="memberError" role="alert"></div><div class="dialog-actions"><button class="secondary" type="button" id="cancelMember">Cancel</button><button class="danger" type="button" id="confirmMember"><i class="bi bi-person-dash" aria-hidden="true"></i> Remove member</button></div></dialog>
<dialog class="team-dialog" id="inviteDialog"><div class="eyebrow">Pending invitation</div><h2>Revoke invitation?</h2><p class="dialog-copy" id="inviteCopy"></p><div class="dialog-error" id="inviteError" role="alert"></div><div class="dialog-actions"><button class="secondary" type="button" id="cancelInvite">Cancel</button><button class="danger" type="button" id="confirmInvite"><i class="bi bi-x-circle" aria-hidden="true"></i> Revoke invitation</button></div></dialog>
<script>` + appScript(csrf) + `
let teamState={members:[],invitations:[]},memberToRemove='',inviteToRevoke='';const $=id=>document.getElementById(id);function say(message){toast.textContent=message;toast.classList.add('on');setTimeout(()=>toast.classList.remove('on'),2800)}function roleLabel(role){return role==='owner'?'Owner':'Member'}
function memberRow(m){const removable=m.role==='member'&&m.status==='active';return '<article class="team-row"><div class="team-row-icon"><i class="bi '+(m.role==='owner'?'bi-person-badge':'bi-person')+'" aria-hidden="true"></i></div><div class="team-identity"><strong>'+esc(m.display_name||m.email||m.github_login||'Team member')+'</strong><span>'+esc(m.email||m.github_login||'Active member')+'</span></div><span class="role-badge '+esc(m.role)+'">'+roleLabel(m.role)+'</span>'+(removable?'<button class="danger row-action" type="button" data-remove="'+esc(m.id)+'" title="Remove team member"><i class="bi bi-person-dash" aria-hidden="true"></i><span>Remove</span></button>':'')+'</article>'}
function inviteRow(i){return '<article class="team-row invitation"><div class="team-row-icon"><i class="bi bi-envelope-paper" aria-hidden="true"></i></div><div class="team-identity"><strong>'+esc(i.email)+'</strong><span>Invitation awaiting acceptance</span></div><div class="row-actions"><button class="secondary row-action" type="button" data-resend="'+esc(i.id)+'" title="Resend invitation"><i class="bi bi-arrow-clockwise" aria-hidden="true"></i><span>Resend</span></button><button class="danger row-action" type="button" data-revoke="'+esc(i.id)+'" title="Revoke invitation"><i class="bi bi-x-circle" aria-hidden="true"></i><span>Revoke</span></button></div></article>'}
function wireTeamRows(){document.querySelectorAll('[data-remove]').forEach(b=>b.onclick=()=>openMemberDialog(b.dataset.remove));document.querySelectorAll('[data-resend]').forEach(b=>b.onclick=()=>resendInvite(b));document.querySelectorAll('[data-revoke]').forEach(b=>b.onclick=()=>openInviteDialog(b.dataset.revoke))}
async function load(){teamState=await api('/api/app/team');memberCount.textContent=teamState.members.length;inviteCount.textContent=teamState.invitations.length;members.innerHTML=teamState.members.length?teamState.members.map(memberRow).join(''):'<div class="empty-row">No members yet.</div>';invites.innerHTML=teamState.invitations.length?teamState.invitations.map(inviteRow).join(''):'<div class="empty-row">No pending invitations.</div>';wireTeamRows()}
invite.onsubmit=async e=>{e.preventDefault();inviteSubmit.disabled=true;const f=new FormData(e.target);try{await api('/api/app/team/invitations','POST',{email:f.get('email')});e.target.reset();say('Invitation sent');await load()}catch(err){say(err.message)}finally{inviteSubmit.disabled=false}};
function openMemberDialog(id){const member=teamState.members.find(m=>m.id===id);if(!member)return;memberToRemove=id;memberCopy.textContent='Remove '+(member.display_name||member.email||'this member')+'? Their presentations will transfer to you. This cannot be undone.';memberError.textContent='';memberDialog.showModal()}
cancelMember.onclick=()=>memberDialog.close();confirmMember.onclick=async()=>{confirmMember.disabled=true;try{await api('/api/app/team/members/'+memberToRemove,'DELETE');memberDialog.close();say('Team member removed');await load()}catch(err){memberError.textContent=err.message}finally{confirmMember.disabled=false}};
async function resendInvite(button){button.disabled=true;try{await api('/api/app/team/invitations/'+button.dataset.resend+'/resend','POST',{});say('Invitation resent')}catch(err){say(err.message)}finally{button.disabled=false}}
function openInviteDialog(id){const invitation=teamState.invitations.find(i=>i.id===id);if(!invitation)return;inviteToRevoke=id;inviteCopy.textContent='Revoke the invitation for '+invitation.email+'? The existing invitation link will stop working.';inviteError.textContent='';inviteDialog.showModal()}
cancelInvite.onclick=()=>inviteDialog.close();confirmInvite.onclick=async()=>{confirmInvite.disabled=true;try{await api('/api/app/team/invitations/'+inviteToRevoke,'DELETE');inviteDialog.close();say('Invitation revoked');await load()}catch(err){inviteError.textContent=err.message}finally{confirmInvite.disabled=false}};
logout.onclick=async()=>{await api('/api/auth/logout','POST',{});location.replace('/auth/login')};load().catch(err=>say(err.message));
</script></body></html>`
}

func appScript(csrf string) string {
	return `const csrf=` + strconvQuote(csrf) + `;const esc=s=>String(s||'').replace(/[&<>"']/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));async function api(path,method='GET',body){const o={method,headers:{}};if(method!=='GET'){o.headers['Content-Type']='application/json';o.headers['X-CSRF-Token']=csrf;o.body=JSON.stringify(body||{});}const r=await fetch(path,o);const j=await r.json().catch(()=>({}));if(!r.ok)throw new Error(j.error||('HTTP '+r.status));return j;}`
}

func strconvQuote(v string) string {
	b, _ := json.Marshal(v)
	return string(b)
}

const appCSS = `@import url('https://fonts.googleapis.com/css2?family=Geist:wght@400;500;600&family=Fraunces:opsz,wght@9..144,300;9..144,400&display=swap');*{box-sizing:border-box}body{font-family:Geist,Inter,system-ui,sans-serif;background:#071d10;color:#e3fddb;margin:0}main{max-width:1000px;margin:0 auto;padding:48px 28px}main.auth{max-width:460px;margin:12vh auto;background:#12341d;border:1px solid #2c4a35;border-radius:18px}h1{font-family:Fraunces,Georgia,serif;font-weight:300;color:#fff;font-size:48px;letter-spacing:-.035em}h2{margin-top:36px;font-family:Fraunces,Georgia,serif;font-weight:400}nav{display:flex;gap:10px;align-items:center;justify-content:flex-end}a{color:#21bf61}.nav-button{display:inline-flex;align-items:center;min-height:42px;padding:9px 16px;border:1px solid #41614a;border-radius:999px;background:#102e1b;color:#e3fddb;text-decoration:none;font-weight:700}input,button,select{font:inherit;border-radius:12px;padding:11px 14px;border:1px solid #41614a}input{background:#fff;color:#111;min-width:240px}button{background:#7cf29a;color:#071d10;font-weight:700;cursor:pointer}button.secondary{background:#102e1b;color:#e3fddb}button.danger{background:#652b24;color:#ffd8d0;border-color:#9a5042}article{background:#12341d;border:1px solid #2c4a35;border-radius:14px;padding:16px;margin:10px 0}form{display:flex;gap:10px;flex-wrap:wrap}.grid{display:grid;grid-template-columns:repeat(auto-fit,minmax(270px,1fr));gap:16px}.muted{color:#8fb59a}.actions{display:flex;gap:8px;flex-wrap:wrap;margin-top:14px}.auth form{display:grid}.auth input{width:100%}#err,#memberError{color:#ff9b75;margin-top:12px}dialog{color:#e3fddb;background:#12341d;border:1px solid #41614a;border-radius:22px;padding:28px;box-shadow:0 30px 80px #0009}dialog::backdrop{background:#000b;backdrop-filter:blur(4px)}dialog form{display:grid;min-width:340px}`

func (s *Server) renderCollabPresentations(w http.ResponseWriter, r *http.Request, sess collab.Session) {
	s.renderCatalogPage(w, sess.User.DisplayName, sess.User.Role, sess.CSRF, true)
}

func (s *Server) handlePlayerSessionPage(w http.ResponseWriter, r *http.Request) {
	if s.Collab == nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Referrer-Policy", "no-referrer")
	io.WriteString(w, authShell("Opening presentation", `<p class="muted" id="status">Authorizing…</p>`, `
(async()=>{const token=location.hash.slice(1);history.replaceState(null,'','/session');try{const r=await fetch('/api/player/session',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({token})});const j=await r.json();if(!r.ok)throw new Error(j.error||'Authorization failed');sessionStorage.setItem('vstd_player_session',j.access_token);location.replace(j.deck_url);}catch(e){status.textContent=e.message;}})();`))
}

func (s *Server) handlePlayerSessionExchange(w http.ResponseWriter, r *http.Request) {
	if s.Collab == nil {
		http.NotFound(w, r)
		return
	}
	if r.Header.Get("Origin") != s.playerOrigin {
		jsonErr(w, fmt.Errorf("request origin rejected"), http.StatusForbidden)
		return
	}
	var req struct{ Token string }
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&req); err != nil {
		jsonErr(w, err, 400)
		return
	}
	access, ps, err := s.Collab.ExchangeHandoff(r.Context(), req.Token)
	if err != nil {
		jsonErr(w, fmt.Errorf("handoff is invalid, expired, or already used"), http.StatusUnauthorized)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: playerMediaCookie, Value: access, Path: "/", HttpOnly: true,
		Secure: strings.HasPrefix(s.playerOrigin, "https://") || r.TLS != nil, SameSite: http.SameSiteLaxMode,
		Expires: time.Now().Add(12 * time.Hour), MaxAge: 12 * 60 * 60})
	writeJSON(w, map[string]string{"access_token": access, "deck_url": "/d/" + ps.Deck.StorageKey + "/", "expires_in": "43200"})
}

func playerBootstrapHTML(deck, appOrigin string) string {
	return `<!DOCTYPE html><html><head><meta name="referrer" content="no-referrer"><title>Opening presentation</title></head><body><p>Opening presentation…</p><script>(async()=>{const t=sessionStorage.getItem('vstd_player_session');if(!t){location.replace(` + strconvQuote(appOrigin+"/presentations") + `);return;}const r=await fetch('/api/player/deck/` + url.PathEscape(deck) + `/document',{headers:{Authorization:'Bearer '+t}});if(!r.ok){sessionStorage.removeItem('vstd_player_session');location.replace(` + strconvQuote(appOrigin+"/presentations") + `);return;}const h=await r.text();document.open();document.write(h);document.close();})()</script></body></html>`
}

func (s *Server) handlePlayerDocument(w http.ResponseWriter, r *http.Request) {
	deck := r.PathValue("deck")
	ps, ok := s.playerSessionForDeck(r, deck)
	if !ok || !s.Collab.Can(r.Context(), ps.User.ID, ps.Deck, "view") {
		http.Error(w, "access denied", http.StatusForbidden)
		return
	}
	s.refreshDeckLinks(r, deck)
	out, err := s.St.Build(deck)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	http.ServeFile(w, r, out)
}

func (s *Server) syncDeckVisibilityFile(deck collab.Deck) {
	m, err := s.St.LoadDeckMeta(deck.StorageKey)
	if err != nil {
		return
	}
	m.Visibility = deck.Visibility
	_ = s.St.SaveDeckMeta(deck.StorageKey, m)
}

func safeRemoveDeck(root, key string) {
	if studio.ValidDeckName(key) {
		_ = os.RemoveAll(filepath.Join(root, "decks", key))
	}
}
