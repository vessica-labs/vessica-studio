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

func (s *Server) hasShare(r *http.Request, deck string) bool {
	if c, err := r.Cookie(shareCookieName(deck)); err == nil && s.shareValid(deck, c.Value) {
		return true
	}
	return false
}

// canView: in public mode a deck is visible to the presenter or a valid
// share-cookie holder; local modes are open.
func (s *Server) canView(r *http.Request, deck string) bool {
	return s.Mode != ModePublic || s.isPresenter(r) || s.hasShare(r, deck)
}

// hasAnyAccess gates shared assets (/library) in public mode. Loopback
// requests pass: the headless Chrome spawned for PDF export (export.go)
// fetches slide imagery cookie-less from inside this same machine/container.
func (s *Server) hasAnyAccess(r *http.Request) bool {
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
	maxAge := 0
	if parts := strings.SplitN(tok, ".", 2); len(parts) == 2 {
		if exp, err := strconv.ParseInt(parts[0], 10, 64); err == nil {
			maxAge = int(time.Until(time.Unix(exp, 0)).Seconds())
			if maxAge < 1 {
				maxAge = 1
			}
		}
	}
	http.SetCookie(w, &http.Cookie{Name: shareCookieName(deck), Value: tok, Path: "/",
		HttpOnly: true, Secure: r.TLS != nil || s.Mode == ModePublic, SameSite: http.SameSiteLaxMode,
		MaxAge: maxAge})
	http.Redirect(w, r, "/d/"+deck+"/", http.StatusFound)
}

func (s *Server) handleMintShare(w http.ResponseWriter, r *http.Request) {
	if !s.isPresenter(r) {
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
	deck := r.PathValue("deck")
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
	if !s.canView(r, deck) && !isLoopback(r) {
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
	var req struct {
		Index int `json:"index"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<12)).Decode(&req); err != nil {
		jsonErr(w, err, 400)
		return
	}
	s.Broadcast("follow|" + r.PathValue("deck") + "|" + strconv.Itoa(req.Index))
	writeJSON(w, map[string]string{"status": "ok"})
}

// ---- GitHub device flow (vessica-cli pattern) ----

func (s *Server) handleAuthConfig(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{
		"mode":              string(s.Mode),
		"github_configured": s.ghClientID != "",
	})
}

func (s *Server) handleGitHubDevice(w http.ResponseWriter, r *http.Request) {
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
		"verification_uri": device.VerificationURI, "interval": device.Interval})
}

func (s *Server) handleGitHubPoll(w http.ResponseWriter, r *http.Request) {
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
	if !s.allowed[strings.ToLower(identity.Login)] {
		jsonErr(w, fmt.Errorf("GitHub user %q is not on the presenter allowlist", identity.Login), http.StatusForbidden)
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
      if(p.status===200&&j.status==='completed'){body.innerHTML='Signed in as <b>'+j.login+'</b> — redirecting…';location.href='/';return;}
      if(p.status!==202){throw new Error(j.error||('HTTP '+p.status));}
    }
  }catch(e){err.textContent=e.message;}
})();
</script></body></html>`)
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
