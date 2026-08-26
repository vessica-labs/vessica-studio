package server

// Presenter auth (GitHub Device Flow — mirrors the vessica-cli dashboard
// pattern: public client ID only, no client secret) and signed audience
// share links. All signing uses VSTD_SECRET (HMAC-SHA256); sessions are
// stateless cookies, share links are deck-scoped expiring tokens.
//
// Env:
//
//	VSTD_SECRET            required in public mode (cookie + share signing)
//	VSTD_GITHUB_CLIENT_ID  GitHub OAuth app client ID (device flow enabled)
//	VSTD_ALLOWED_GITHUB    comma-separated GitHub logins allowed as presenter

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	qrcode "github.com/skip2/go-qrcode"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/vessica-labs/vessica-studio/internal/studio"
)

const sessionCookie = "vstd_session"

type githubFlow struct {
	DeviceCode string
	ExpiresAt  time.Time
	Interval   int
}

func (s *Server) initAuth() {
	s.secret = os.Getenv("VSTD_SECRET")
	if s.secret == "" && s.St.Config.ShareSecretCmd != "" {
		// resolve the same secret the CLI uses (e.g. macOS Keychain), so QR
		// codes minted by a LOCAL serve validate on the HOSTED instance
		if out, err := exec.Command("sh", "-c", s.St.Config.ShareSecretCmd).Output(); err == nil {
			s.secret = strings.TrimSpace(string(out))
		}
	}
	s.ghClientID = os.Getenv("VSTD_GITHUB_CLIENT_ID")
	s.allowed = map[string]bool{}
	for _, l := range strings.Split(os.Getenv("VSTD_ALLOWED_GITHUB"), ",") {
		if l = strings.ToLower(strings.TrimSpace(l)); l != "" {
			s.allowed[l] = true
		}
	}
	if s.Mode == ModePublic && s.secret == "" {
		b := make([]byte, 32)
		rand.Read(b)
		s.secret = hex.EncodeToString(b)
		log.Printf("WARNING: VSTD_SECRET not set — using a random per-boot secret; sessions and share links will not survive restarts")
	}
	s.flows = map[string]githubFlow{}
}

func (s *Server) sign(data string) string {
	m := hmac.New(sha256.New, []byte(s.secret))
	m.Write([]byte(data))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

func randToken(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// ---- presenter sessions ----

func (s *Server) sessionValue(login string, exp time.Time) string {
	base := login + "|" + strconv.FormatInt(exp.Unix(), 10)
	return base + "|" + s.sign("session|"+base)
}

func (s *Server) isPresenter(r *http.Request) bool {
	if s.Collab != nil {
		if ps, ok := s.playerSession(r); ok {
			if deck := r.PathValue("deck"); deck != "" && ps.Deck.StorageKey != deck {
				return false
			}
			return ps.Mode == "present" || ps.Mode == "edit"
		}
		_, ok := s.accountSession(r)
		return ok
	}
	if s.Mode != ModePublic {
		return true // local modes: the machine owner is the presenter
	}
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return false
	}
	parts := strings.Split(c.Value, "|")
	if len(parts) != 3 {
		return false
	}
	exp, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || time.Now().Unix() > exp {
		return false
	}
	if !hmac.Equal([]byte(parts[2]), []byte(s.sign("session|"+parts[0]+"|"+parts[1]))) {
		return false
	}
	return s.allowed[strings.ToLower(parts[0])]
}

// ---- share links ----

// MintShare returns a deck-scoped share token valid for ttl.
func (s *Server) MintShare(deck string, ttl time.Duration) string {
	exp := strconv.FormatInt(time.Now().Add(ttl).Unix(), 10)
	return exp + "." + s.sign("share|"+deck+"|"+exp)
}

// MintShareToken is the CLI entry point (no server needed).
func MintShareToken(secret, deck string, ttl time.Duration) string {
	srv := &Server{secret: secret}
	return srv.MintShare(deck, ttl)
}

func (s *Server) shareValid(deck, tok string) bool {
	parts := strings.SplitN(tok, ".", 2)
	if len(parts) != 2 {
		return false
	}
	exp, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || time.Now().Unix() > exp {
		return false
	}
	return hmac.Equal([]byte(parts[1]), []byte(s.sign("share|"+deck+"|"+parts[0])))
}

func shareCookieName(deck string) string { return "vstd_share_" + deck }

func shareCookieMaxAge(tok string) int {
	parts := strings.SplitN(tok, ".", 2)
	if len(parts) != 2 {
		return 0
	}
	exp, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return 0
	}
	maxAge := int(time.Until(time.Unix(exp, 0)).Seconds())
	if maxAge < 1 {
		return 1
	}
	return maxAge
}

func (s *Server) setShareCookie(w http.ResponseWriter, r *http.Request, deck, tok string) {
	http.SetCookie(w, &http.Cookie{Name: shareCookieName(deck), Value: tok, Path: "/",
		HttpOnly: true, Secure: r.TLS != nil || s.Mode == ModePublic, SameSite: http.SameSiteLaxMode,
		MaxAge: shareCookieMaxAge(tok)})
}

func (s *Server) hasShare(r *http.Request, deck string) bool {
	if c, err := r.Cookie(shareCookieName(deck)); err == nil && s.shareValid(deck, c.Value) {
		return true
	}
	return false
}

// canView: in public mode a deck is visible to the presenter or a valid
// share-cookie holder; local modes are open.
func (s *Server) canView(r *http.Request, deck string) bool {
	if s.Collab != nil {
		if ps, ok := s.playerSessionForDeck(r, deck); ok && s.Collab.Can(r.Context(), ps.User.ID, ps.Deck, "view") {
			return true
		}
		return s.hasShare(r, deck)
	}
	return s.Mode != ModePublic || s.isPresenter(r) || s.hasShare(r, deck)
}

// hasAnyAccess gates shared assets (/library) in public mode. Loopback
// requests pass: the headless Chrome spawned for PDF export (export.go)
// fetches slide imagery cookie-less from inside this same machine/container.
func (s *Server) hasAnyAccess(r *http.Request) bool {
	if s.Collab != nil {
		if _, ok := s.playerSession(r); ok {
			return true
		}
		// Native image/video elements cannot attach Authorization headers. A
		// player-host-only HttpOnly cookie is accepted solely by media handlers;
		// it is never account or player API authority.
		if _, ok := s.playerMediaSession(r); ok {
			return true
		}
	}
	if s.Mode != ModePublic || s.isPresenter(r) || isLoopback(r) {
		return true
	}
	for _, c := range r.Cookies() {
		if strings.HasPrefix(c.Name, "vstd_share_") {
			deck := strings.TrimPrefix(c.Name, "vstd_share_")
			if s.shareValid(deck, c.Value) {
				return true
			}
		}
	}
	return false
}

// ---- HTTP handlers ----

func (s *Server) handleShareLanding(w http.ResponseWriter, r *http.Request) {
	deck, tok := r.PathValue("deck"), r.PathValue("token")
	if !studio.ValidDeckName(deck) || !s.shareValid(deck, tok) {
		http.Error(w, "This share link is invalid or has expired.", http.StatusForbidden)
		return
	}
	s.setShareCookie(w, r, deck, tok)
	http.Redirect(w, r, "/d/"+deck+"/", http.StatusFound)
}

// handleFollowLanding is an opt-in, memorable audience entrance for the one
// deck configured as follow_deck in studio.yaml. It never puts a share token
// in browser history: the route grants a 24-hour, deck-scoped cookie and then
// joins the same live-follow experience used by QR visitors.
func (s *Server) handleFollowLanding(w http.ResponseWriter, r *http.Request) {
	deck := strings.TrimSpace(s.St.Config.FollowDeck)
	if !studio.ValidDeckName(deck) {
		http.NotFound(w, r)
		return
	}
	if _, err := s.St.LoadDeckMeta(deck); err != nil {
		http.NotFound(w, r)
		return
	}
	tok := s.MintShare(deck, 24*time.Hour)
	s.setShareCookie(w, r, deck, tok)
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, "/d/"+deck+"/?follow=1", http.StatusFound)
}

func (s *Server) handleMintShare(w http.ResponseWriter, r *http.Request) {
	deck := r.PathValue("deck")
	if s.Collab != nil {
		ps, ok := s.playerSessionForDeck(r, deck)
		if !ok || ps.Mode != "edit" || !s.Collab.Can(r.Context(), ps.User.ID, ps.Deck, "external_share") {
			jsonErr(w, fmt.Errorf("presentation owner access required"), http.StatusForbidden)
			return
		}
		_ = s.Collab.Audit(r.Context(), ps.User.ID, "deck.external_share", ps.Deck.ID, "", nil)
	} else if !s.isPresenter(r) {
		jsonErr(w, fmt.Errorf("presenter auth required"), http.StatusUnauthorized)
		return
	}
	var req struct {
		TTLHours int `json:"ttl_hours"`
	}
	json.NewDecoder(io.LimitReader(r.Body, 1<<12)).Decode(&req)
	if req.TTLHours <= 0 {
		req.TTLHours = 72
	}
	if !studio.ValidDeckName(deck) {
		jsonErr(w, fmt.Errorf("invalid deck"), http.StatusBadRequest)
		return
	}
	if req.TTLHours > 24*30 {
		req.TTLHours = 24 * 30
	}
	expires := time.Now().Add(time.Duration(req.TTLHours) * time.Hour)
	tok := s.MintShare(deck, time.Duration(req.TTLHours)*time.Hour)
	writeJSON(w, map[string]string{
		"url":        s.shareBase(r) + "/v/" + deck + "/" + tok,
		"token":      tok,
		"expires_at": expires.UTC().Format(time.RFC3339),
	})
}

func (s *Server) shareBase(r *http.Request) string {
	base := s.St.Config.PublicHost
	if base == "" {
		scheme := "http"
		if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
			scheme = "https"
		}
		base = scheme + "://" + r.Host
	}
	return strings.TrimRight(base, "/")
}

// handleShareQR renders a QR code for a freshly-minted share link — embed
// it on a closing slide via <img src="/api/deck/<deck>/share-qr.png"> and it
// can never go stale. Points at public_host when configured (so phones in
// the room resolve it), else the request host.
func (s *Server) handleShareQR(w http.ResponseWriter, r *http.Request) {
	deck := r.PathValue("deck")
	if s.Collab != nil {
		ps, ok := s.nativePlayerSessionForDeck(r, deck)
		if !ok || ps.Mode != "edit" || !s.Collab.Can(r.Context(), ps.User.ID, ps.Deck, "external_share") {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		_ = s.Collab.Audit(r.Context(), ps.User.ID, "deck.external_share", ps.Deck.ID, "", map[string]any{"kind": "qr"})
	} else if !s.canView(r, deck) && !isLoopback(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	ttl := 72
	if v := r.URL.Query().Get("ttl"); v != "" {
		fmt.Sscanf(v, "%d", &ttl)
	}
	link := s.shareBase(r) + "/v/" + deck + "/" + s.MintShare(deck, time.Duration(ttl)*time.Hour)
	png, err := qrcode.Encode(link, qrcode.Medium, 640)
	if err != nil {
		jsonErr(w, err, 500)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(png)
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	if s.Collab != nil {
		if ps, ok := s.playerSession(r); ok {
			owned := ps.Deck.OwnerUserID == ps.User.ID
			present := ps.Mode == "present" || ps.Mode == "edit"
			editable := ps.Mode == "edit" && owned && s.ContentSync.Editable()
			deck := ps.Deck
			deck.Owned = owned
			writeJSON(w, map[string]any{
				"mode": ps.Mode, "presenter": present, "editable": editable, "audience": false,
				"user": ps.User, "deck": deck,
				"capabilities": map[string]bool{"view": true, "present": present, "edit": editable,
					"fork": false, "change_visibility": false, "external_share": owned && ps.Mode == "edit"},
				"content_sync": s.ContentSync.Status(), "app_origin": s.appOrigin,
			})
			return
		}
		if sess, ok := s.accountSession(r); ok {
			writeJSON(w, map[string]any{"mode": string(s.Mode), "presenter": true, "editable": false, "user": sess.User, "csrf": sess.CSRF})
			return
		}
		writeJSON(w, map[string]any{"mode": string(s.Mode), "presenter": false, "editable": false})
		return
	}
	writeJSON(w, map[string]any{
		"mode":         string(s.Mode),
		"presenter":    s.isPresenter(r),
		"editable":     s.canEdit(r),
		"content_sync": s.ContentSync.Status(),
	})
}

func (s *Server) handleContentSyncStatus(w http.ResponseWriter, r *http.Request) {
	if !s.isPresenter(r) {
		jsonErr(w, fmt.Errorf("presenter auth required"), http.StatusUnauthorized)
		return
	}
	writeJSON(w, s.ContentSync.Status())
}

func (s *Server) handlePresenting(w http.ResponseWriter, r *http.Request) {
	if !s.isPresenter(r) {
		jsonErr(w, fmt.Errorf("presenter auth required"), http.StatusUnauthorized)
		return
	}
	var req presentingUpdate
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<12)).Decode(&req); err != nil {
		jsonErr(w, err, 400)
		return
	}
	deck := r.PathValue("deck")
	if err := s.validatePresenting(deck, req.Index); err != nil {
		jsonErr(w, err, http.StatusBadRequest)
		return
	}
	relay := presentingUpdate{Index: req.Index, Seq: time.Now().UnixNano()}
	s.setPresenting(deck, req.Index, relay.Seq)
	if err := s.relayPresenting(r, deck, relay); err != nil {
		// Local viewing must remain usable if Railway is temporarily
		// unreachable; the error is logged so production diagnostics show it.
		log.Printf("presenting relay failed for %s slide %d: %v", deck, req.Index+1, err)
		writeJSON(w, map[string]string{"status": "local-only"})
		return
	}
	writeJSON(w, map[string]string{"status": "ok"})
}

// ---- GitHub device flow (vessica-cli pattern) ----

func (s *Server) handleAuthConfig(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{
		"mode":              string(s.Mode),
		"github_configured": s.ghClientID != "",
		"password_enabled":  s.Collab != nil,
	})
}

func (s *Server) handleGitHubDevice(w http.ResponseWriter, r *http.Request) {
	if s.Collab != nil && !s.validAppOrigin(r) {
		jsonErr(w, fmt.Errorf("request origin rejected"), http.StatusForbidden)
		return
	}
	if s.ghClientID == "" {
		jsonErr(w, fmt.Errorf("GitHub OAuth is not configured (set VSTD_GITHUB_CLIENT_ID)"), http.StatusServiceUnavailable)
		return
	}
	form := url.Values{"client_id": {s.ghClientID}, "scope": {"read:user"}}
	req, _ := http.NewRequestWithContext(r.Context(), http.MethodPost,
		"https://github.com/login/device/code", strings.NewReader(form.Encode()))
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		jsonErr(w, err, 502)
		return
	}
	defer resp.Body.Close()
	var device struct {
		DeviceCode      string `json:"device_code"`
		UserCode        string `json:"user_code"`
		VerificationURI string `json:"verification_uri"`
		ExpiresIn       int    `json:"expires_in"`
		Interval        int    `json:"interval"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&device); err != nil || device.DeviceCode == "" {
		jsonErr(w, fmt.Errorf("GitHub device flow failed"), 502)
		return
	}
	id := randToken(18)
	s.mu.Lock()
	s.flows[id] = githubFlow{DeviceCode: device.DeviceCode,
		ExpiresAt: time.Now().Add(time.Duration(device.ExpiresIn) * time.Second),
		Interval:  device.Interval}
	s.mu.Unlock()
	writeJSON(w, map[string]any{"id": id, "user_code": device.UserCode,
		"verification_uri": device.VerificationURI, "interval": device.Interval,
		"expires_in": device.ExpiresIn})
}

func (s *Server) handleGitHubPoll(w http.ResponseWriter, r *http.Request) {
	if s.Collab != nil && !s.validAppOrigin(r) {
		jsonErr(w, fmt.Errorf("request origin rejected"), http.StatusForbidden)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	s.mu.Lock()
	flow, ok := s.flows[r.PathValue("id")]
	s.mu.Unlock()
	if !ok || time.Now().After(flow.ExpiresAt) {
		jsonErr(w, fmt.Errorf("sign-in expired — reload to try again"), http.StatusGone)
		return
	}
	form := url.Values{"client_id": {s.ghClientID}, "device_code": {flow.DeviceCode},
		"grant_type": {"urn:ietf:params:oauth:grant-type:device_code"}}
	req, _ := http.NewRequestWithContext(r.Context(), http.MethodPost,
		"https://github.com/login/oauth/access_token", strings.NewReader(form.Encode()))
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		jsonErr(w, err, 502)
		return
	}
	defer resp.Body.Close()
	var result struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		jsonErr(w, err, 502)
		return
	}
	if result.AccessToken == "" {
		if result.Error == "authorization_pending" || result.Error == "slow_down" {
			w.WriteHeader(http.StatusAccepted)
			writeJSON(w, map[string]any{"status": result.Error, "retry_after": max(flow.Interval, 5)})
			return
		}
		jsonErr(w, fmt.Errorf("GitHub sign-in failed: %s", result.Error), http.StatusUnauthorized)
		return
	}
	userReq, _ := http.NewRequestWithContext(r.Context(), http.MethodGet, "https://api.github.com/user", nil)
	userReq.Header.Set("Authorization", "Bearer "+result.AccessToken)
	userReq.Header.Set("Accept", "application/vnd.github+json")
	userResp, err := (&http.Client{Timeout: 20 * time.Second}).Do(userReq)
	if err != nil {
		jsonErr(w, err, 502)
		return
	}
	defer userResp.Body.Close()
	var identity struct {
		ID    int64  `json:"id"`
		Login string `json:"login"`
	}
	if err := json.NewDecoder(userResp.Body).Decode(&identity); err != nil || identity.ID == 0 {
		jsonErr(w, fmt.Errorf("GitHub identity could not be read"), 502)
		return
	}
	if s.Collab == nil && !s.allowed[strings.ToLower(identity.Login)] {
		jsonErr(w, fmt.Errorf("GitHub user %q is not on the presenter allowlist", identity.Login), http.StatusForbidden)
		return
	}
	if s.Collab != nil {
		u, err := s.Collab.BootstrapGitHub(r.Context(), identity.ID, identity.Login, s.filesystemDecks())
		if err != nil {
			jsonErr(w, err, http.StatusForbidden)
			return
		}
		raw, _, err := s.Collab.CreateSession(r.Context(), u.ID)
		if err != nil {
			jsonErr(w, err, http.StatusInternalServerError)
			return
		}
		s.setAccountCookie(w, r, raw)
		_ = s.Collab.Audit(r.Context(), u.ID, "auth.github_login", "", u.ID, map[string]any{"github_login": identity.Login})
		s.mu.Lock()
		delete(s.flows, r.PathValue("id"))
		s.mu.Unlock()
		log.Printf("owner login: %s", identity.Login)
		writeJSON(w, map[string]any{"status": "completed", "login": identity.Login})
		return
	}
	exp := time.Now().Add(12 * time.Hour)
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: s.sessionValue(identity.Login, exp),
		Path: "/", HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode, Expires: exp})
	s.mu.Lock()
	delete(s.flows, r.PathValue("id"))
	s.mu.Unlock()
	log.Printf("presenter login: %s", identity.Login)
	writeJSON(w, map[string]any{"status": "completed", "login": identity.Login})
}

func (s *Server) handleLoginPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if s.Mode == ModePublic && s.isPresenter(r) {
		http.Redirect(w, r, "/presentations", http.StatusFound)
		return
	}
	if s.Collab != nil {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		io.WriteString(w, collaborationLoginPage())
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	io.WriteString(w, `<!DOCTYPE html><html><head><title>Vessica Studio — sign in</title><style>
body{font-family:'Trebuchet MS',sans-serif;background:#0C2B15;color:#E3FDDB;margin:0;
display:flex;align-items:center;justify-content:center;min-height:100vh}
.card{background:#12341d;border:1px solid #2c4a35;border-radius:16px;padding:44px 52px;max-width:460px;text-align:center}
h1{font-family:Georgia,serif;font-weight:400;font-size:30px;color:#FCFBFA;margin:0 0 18px}
.code{font-size:34px;letter-spacing:6px;color:#20E3AC;margin:18px 0;font-weight:700}
a{color:#21BF61} .sub{color:#8fb59a;font-size:14px} #err{color:#FE7C2B;margin-top:12px}
</style></head><body><div class="card"><h1>Presenter sign-in</h1>
<div id="body" class="sub">Contacting GitHub…</div><div id="err"></div></div>
<script>
(async()=>{
  const body=document.getElementById('body'),err=document.getElementById('err');
  try{
    const d=await (await fetch('/auth/github/device',{method:'POST'})).json();
    if(!d.user_code)throw new Error(d.error||'device flow unavailable');
    body.innerHTML='Enter this code at <a href="'+d.verification_uri+'" target="_blank">'+d.verification_uri+'</a>'+
      '<div class="code">'+d.user_code+'</div><span class="sub">Waiting for GitHub…</span>';
    for(;;){
      await new Promise(res=>setTimeout(res,(d.interval||5)*1000));
      const p=await fetch('/auth/github/poll/'+d.id,{method:'POST'});
      const j=await p.json();
      if(p.status===200&&j.status==='completed'){body.innerHTML='Signed in as <b>'+j.login+'</b> — redirecting…';location.replace('/presentations');return;}
      if(p.status!==202){throw new Error(j.error||('HTTP '+p.status));}
    }
  }catch(e){err.textContent=e.message;}
})();
</script></body></html>`)
}

const collaborationLoginCSS = `
body{min-height:100vh;display:grid;place-items:center;padding:40px 24px;background:#071d10}
[hidden]{display:none!important}
main.login-auth{width:min(920px,100%);max-width:none;margin:0;padding:0;display:grid;grid-template-columns:minmax(0,1.08fr) minmax(0,.92fr);overflow:hidden;background:#12341d;border:1px solid #31513a;border-radius:28px;box-shadow:0 28px 80px rgba(0,0,0,.3)}
.login-member,.login-owner{padding:56px}
.login-owner{display:flex;flex-direction:column;justify-content:center;background:#0d2917;border-left:1px solid #2c4a35}
.login-auth .eyebrow{margin:0 0 14px;color:#20e3ac;font-size:12px;font-weight:800;letter-spacing:.16em;text-transform:uppercase}
.login-auth h1{margin:0 0 12px;font-size:48px;line-height:1.02;letter-spacing:-.035em}
.login-auth h2{margin:0 0 10px;color:#fff;font:400 31px/1.12 Georgia,serif;letter-spacing:-.02em}
.login-auth .lede{max-width:420px;margin:0 0 34px;color:#a8c8b0;font-size:16px;line-height:1.55}
.login-form{display:grid;gap:18px}
.login-form label{display:grid;gap:8px;color:#d9eddd;font-size:13px;font-weight:700}
.login-form input{min-width:0;width:100%;height:48px;padding:0 15px;background:#f8fbf8;border-color:#d5e3d8;border-radius:12px;outline:none;transition:border-color .15s,box-shadow .15s}
.login-form input:focus{border-color:#20e3ac;box-shadow:0 0 0 4px rgba(32,227,172,.15)}
.login-form .sign-in-button{height:48px;margin-top:2px;border:0;border-radius:12px;background:#20d991;transition:transform .15s,background .15s}
.login-form .sign-in-button:hover{background:#35e6a2;transform:translateY(-1px)}
.forgot-button{justify-self:start;padding:0;border:0;background:transparent;color:#8fb59a;font-weight:650}
.forgot-button:hover{color:#dff9e6;text-decoration:underline}
.login-message{min-height:22px;margin:18px 0 0;color:#ffb195;font-size:14px;line-height:1.5}
.login-message.success{color:#77e8b2}
.github-intro{margin-top:26px}
.github-start{width:100%;min-height:50px;border:1px solid #4c6d55;border-radius:13px;background:#f7fbf8;color:#0a2513;transition:transform .15s,box-shadow .15s,background .15s}
.github-start:hover{background:#fff;box-shadow:0 10px 24px rgba(0,0,0,.18);transform:translateY(-1px)}
.github-promise{margin:14px 0 0;color:#8fb59a;font-size:13px;line-height:1.5;text-align:center}
.device-flow{margin-top:24px}
.device-topline{display:flex;align-items:center;justify-content:space-between;gap:16px;margin-bottom:14px}
.device-kicker{margin:0;color:#b9d2bf;font-size:13px;font-weight:750}
.new-code{padding:0;border:0;background:transparent;color:#76dfa9;font-size:13px;font-weight:750}
.new-code:hover{text-decoration:underline}
.device-instruction{margin:0 0 14px;color:#a8c8b0;font-size:14px;line-height:1.5}
.device-code{width:100%;padding:21px 14px 17px;display:grid;gap:7px;background:#071d10;border:1px solid #3e6749;border-radius:16px;color:#fff;text-align:center;transition:border-color .15s,transform .15s,box-shadow .15s}
.device-code:hover,.device-code:focus-visible{border-color:#20e3ac;box-shadow:0 0 0 4px rgba(32,227,172,.12);transform:translateY(-1px);outline:none}
.device-code-value{font:750 35px/1 ui-monospace,SFMono-Regular,Menlo,Consolas,monospace;letter-spacing:.16em;color:#20e3ac}
.copy-hint{color:#8fb59a;font-size:12px;font-weight:650;letter-spacing:0}
.github-actions{display:grid;grid-template-columns:1fr 1fr;gap:10px;margin-top:12px}
.github-actions button,.github-actions a{min-height:44px;display:grid;place-items:center;border-radius:11px;text-decoration:none;font-size:14px;font-weight:750}
.open-github{background:#20d991;color:#071d10;border:1px solid #20d991}
.copy-code{background:transparent;color:#dff9e6;border:1px solid #41614a}
.flow-meta{display:flex;justify-content:space-between;gap:12px;margin:15px 0 9px;color:#8fb59a;font-size:12px}
.flow-status{color:#cbe3d0}
.device-flow progress{display:block;width:100%;height:5px;overflow:hidden;border:0;border-radius:999px;background:#25402d;color:#20d991}
.device-flow progress::-webkit-progress-bar{background:#25402d;border-radius:999px}
.device-flow progress::-webkit-progress-value{background:#20d991;border-radius:999px;transition:width 1s linear}
.device-flow progress::-moz-progress-bar{background:#20d991;border-radius:999px}
.device-flow.expired .device-code{border-color:#805a45}
.device-flow.expired .device-code-value{color:#ffb195}
.device-flow.expired progress{color:#ff9b75}
@media(max-width:760px){body{display:block;padding:18px}.login-auth h1{font-size:41px}main.login-auth{grid-template-columns:1fr}.login-member,.login-owner{padding:36px 30px}.login-owner{border-left:0;border-top:1px solid #2c4a35}.login-owner{min-height:390px}}
@media(max-width:420px){body{padding:0;background:#12341d}main.login-auth{border:0;border-radius:0;box-shadow:none}.login-member,.login-owner{padding:34px 22px}.github-actions{grid-template-columns:1fr}.device-code-value{font-size:30px}}
@media(prefers-reduced-motion:reduce){.login-auth *{scroll-behavior:auto!important;transition:none!important}}
`

const collaborationLoginBody = `<section class="login-member">
<p class="eyebrow">Vessica Studio</p>
<h1>Welcome back</h1>
<p class="lede">Sign in to your presentation workspace.</p>
<form id="login" class="login-form">
<label>Email address<input name="email" type="email" autocomplete="email" placeholder="you@example.com" required></label>
<label>Password<input name="password" type="password" autocomplete="current-password" placeholder="Your password" required></label>
<button class="sign-in-button">Sign in</button>
<button class="forgot-button" type="button" id="forgot">Forgot password?</button>
</form>
<p id="loginMessage" class="login-message" role="status" aria-live="polite"></p>
</section>
<section class="login-owner" aria-labelledby="githubTitle">
<p class="eyebrow">Studio owner</p>
<h2 id="githubTitle">Sign in with GitHub</h2>
<p class="lede">Use the GitHub account connected to this studio.</p>
<div id="githubIntro" class="github-intro">
<button class="github-start" type="button" id="github">Continue with GitHub</button>
<p class="github-promise">We’ll open GitHub for you. No rushing, and no code to retype.</p>
</div>
<div id="githubFlow" class="device-flow" hidden>
<div class="device-topline"><p class="device-kicker">Your temporary code</p><button class="new-code" type="button" id="newGithubCode">Get a new code</button></div>
<p class="device-instruction">Click the code to copy it, then paste it into the GitHub tab.</p>
<button class="device-code" type="button" id="githubCode" aria-label="Copy GitHub device code">
<span class="device-code-value" id="githubCodeValue">—</span><span class="copy-hint" id="githubCopyHint">Click to copy</span>
</button>
<div class="github-actions"><a class="open-github" id="openGithub" target="_blank" rel="noopener">Open GitHub</a><button class="copy-code" type="button" id="copyGithubCode">Copy code</button></div>
<div class="flow-meta"><span class="flow-status" id="githubStatus" role="status" aria-live="polite">Waiting for GitHub…</span><span id="githubExpires">Code available</span></div>
<progress id="githubProgress" max="100" value="100" aria-label="Time remaining for GitHub device code"></progress>
</div>
<p id="githubMessage" class="login-message" role="alert" aria-live="assertive"></p>
</section>`

const collaborationLoginScript = `
const flowStorageKey='vstd_github_device_flow';
const loginForm=document.getElementById('login'),loginMessage=document.getElementById('loginMessage'),forgotButton=document.getElementById('forgot');
const githubButton=document.getElementById('github'),githubIntro=document.getElementById('githubIntro'),githubFlow=document.getElementById('githubFlow');
const githubCode=document.getElementById('githubCode'),githubCodeValue=document.getElementById('githubCodeValue'),githubCopyHint=document.getElementById('githubCopyHint');
const copyGithubCode=document.getElementById('copyGithubCode'),openGithub=document.getElementById('openGithub'),newGithubCode=document.getElementById('newGithubCode');
const githubStatus=document.getElementById('githubStatus'),githubExpires=document.getElementById('githubExpires'),githubProgress=document.getElementById('githubProgress'),githubMessage=document.getElementById('githubMessage');
let activeGithubFlow=null,pollGeneration=0,countdownTimer=0,copyTimer=0;

function setMessage(element,message,success){element.textContent=message||'';element.classList.toggle('success',!!success);}
function saveGithubFlow(flow){try{sessionStorage.setItem(flowStorageKey,JSON.stringify(flow));}catch(_){}}
function clearSavedGithubFlow(){try{sessionStorage.removeItem(flowStorageKey);}catch(_){}}
function remainingSeconds(){return activeGithubFlow?Math.max(0,Math.ceil((activeGithubFlow.expires_at-Date.now())/1000)):0;}
function formatRemaining(seconds){const minutes=Math.floor(seconds/60),rest=seconds%60;return minutes+':'+String(rest).padStart(2,'0');}
function updateGithubCountdown(){
  if(!activeGithubFlow)return;
  const remaining=remainingSeconds(),total=Math.max(1,activeGithubFlow.expires_in||900);
  githubExpires.textContent=remaining?'Available for '+formatRemaining(remaining):'Code expired';
  githubProgress.value=Math.max(0,Math.min(100,(remaining/total)*100));
  if(!remaining)expireGithubFlow();
}
function showGithubFlow(flow){
  activeGithubFlow=flow;githubIntro.hidden=true;githubFlow.hidden=false;githubFlow.classList.remove('expired');
  githubCodeValue.textContent=flow.user_code;openGithub.href=flow.verification_uri;githubCopyHint.textContent='Click to copy';
  githubStatus.textContent='Waiting for you on GitHub…';setMessage(githubMessage,'');
  clearInterval(countdownTimer);updateGithubCountdown();countdownTimer=setInterval(updateGithubCountdown,1000);
}
function expireGithubFlow(){
  pollGeneration++;clearInterval(countdownTimer);githubFlow.classList.add('expired');
  githubStatus.textContent='This code has expired';githubExpires.textContent='Get a new code when you’re ready';githubProgress.value=0;
  setMessage(githubMessage,'The code will stay here, but GitHub needs a fresh one to continue.');clearSavedGithubFlow();
}
async function copyDeviceCode(){
  if(!activeGithubFlow)return;
  try{
    let copied=false;
    if(navigator.clipboard&&navigator.clipboard.writeText){try{await navigator.clipboard.writeText(activeGithubFlow.user_code);copied=true;}catch(_){}}
    if(!copied){const field=document.createElement('textarea');field.value=activeGithubFlow.user_code;field.setAttribute('readonly','');field.style.position='fixed';field.style.opacity='0';document.body.appendChild(field);field.select();copied=document.execCommand('copy');field.remove();}
    if(!copied)throw new Error('copy unavailable');
    githubCopyHint.textContent='Copied to clipboard';copyGithubCode.textContent='Copied';clearTimeout(copyTimer);copyTimer=setTimeout(()=>{githubCopyHint.textContent='Click to copy';copyGithubCode.textContent='Copy code';},2200);
  }catch(_){githubCopyHint.textContent='Select and copy: '+activeGithubFlow.user_code;}
}
function scheduleGithubPoll(generation,seconds){setTimeout(()=>pollGithub(generation),Math.max(1,seconds)*1000);}
async function pollGithub(generation){
  if(generation!==pollGeneration||!activeGithubFlow||!remainingSeconds())return;
  try{
    const response=await fetch('/auth/github/poll/'+encodeURIComponent(activeGithubFlow.id),{method:'POST'});
    const result=await response.json().catch(()=>({}));
    if(response.status===200&&result.status==='completed'){
      clearSavedGithubFlow();clearInterval(countdownTimer);githubStatus.textContent='Signed in. Opening your presentations…';githubProgress.value=100;location.replace('/presentations');return;
    }
    if(response.status===202){githubStatus.textContent=result.status==='slow_down'?'Still waiting — take your time':'Waiting for you on GitHub…';scheduleGithubPoll(generation,result.retry_after||activeGithubFlow.interval||5);return;}
    if(response.status===410){expireGithubFlow();return;}
    if(response.status>=500){githubStatus.textContent='Connection interrupted. Retrying…';scheduleGithubPoll(generation,Math.max(activeGithubFlow.interval||5,8));return;}
    githubStatus.textContent='GitHub needs a new code';setMessage(githubMessage,result.error||'This sign-in could not be completed.');
  }catch(_){githubStatus.textContent='Connection interrupted. Retrying…';scheduleGithubPoll(generation,Math.max(activeGithubFlow.interval||5,8));}
}
async function startGithubFlow(openWindow){
  const githubPopup=openWindow?window.open('about:blank','_blank'):null;
  if(githubPopup){githubPopup.document.title='Opening GitHub…';githubPopup.document.body.textContent='Opening GitHub…';githubPopup.opener=null;}
  githubButton.disabled=true;githubButton.textContent='Preparing GitHub…';setMessage(githubMessage,'');
  try{
    const response=await fetch('/auth/github/device',{method:'POST'}),data=await response.json().catch(()=>({}));
    if(!response.ok||!data.user_code)throw new Error(data.error||'GitHub sign-in is unavailable right now.');
    const expiresIn=Math.max(60,Number(data.expires_in)||900);
    const flow={id:data.id,user_code:data.user_code,verification_uri:data.verification_uri,interval:Math.max(5,Number(data.interval)||5),expires_in:expiresIn,expires_at:Date.now()+expiresIn*1000};
    pollGeneration++;showGithubFlow(flow);saveGithubFlow(flow);githubCode.focus({preventScroll:true});
    if(githubPopup)githubPopup.location.replace(flow.verification_uri);
    const generation=pollGeneration;scheduleGithubPoll(generation,flow.interval);
  }catch(error){if(githubPopup)githubPopup.close();setMessage(githubMessage,error.message||'GitHub sign-in is unavailable right now.');}
  finally{githubButton.disabled=false;githubButton.textContent='Continue with GitHub';}
}

loginForm.onsubmit=async event=>{event.preventDefault();setMessage(loginMessage,'');const data=new FormData(event.target);const response=await fetch('/api/auth/password/login',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({email:data.get('email'),password:data.get('password')})});const result=await response.json().catch(()=>({}));if(response.ok)location.replace('/presentations');else setMessage(loginMessage,result.error||'We couldn’t sign you in with those details.');};
githubButton.onclick=()=>startGithubFlow(true);newGithubCode.onclick=()=>startGithubFlow(true);githubCode.onclick=copyDeviceCode;copyGithubCode.onclick=copyDeviceCode;
forgotButton.onclick=async()=>{const email=loginForm.elements.email;if(!email.value){setMessage(loginMessage,'Enter your email address first, then choose “Forgot password?”.');email.focus();return;}await fetch('/api/auth/password/forgot',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({email:email.value})});setMessage(loginMessage,'If that account exists, a reset link is on its way.',true);};
try{const saved=JSON.parse(sessionStorage.getItem(flowStorageKey)||'null');if(saved&&saved.id&&saved.user_code&&saved.verification_uri){showGithubFlow(saved);if(remainingSeconds()){const generation=++pollGeneration;scheduleGithubPoll(generation,1);}else expireGithubFlow();}}catch(_){clearSavedGithubFlow();}
if(location.hash.startsWith('#reset=')){const token=location.hash.slice(7);history.replaceState(null,'','/auth/login');const password=prompt('Create a new password (12+ characters)');if(password)fetch('/api/auth/password/reset',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({token,password})}).then(async response=>{const result=await response.json();setMessage(loginMessage,response.ok?'Password reset. Sign in with your new password.':(result.error||'Reset failed'),response.ok);});}
`

func collaborationLoginPage() string {
	return authDocument("Sign in to Vessica Studio", "auth login-auth", collaborationLoginBody, collaborationLoginScript, collaborationLoginCSS)
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
