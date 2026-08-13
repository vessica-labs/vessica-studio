// Package server is the vstd HTTP engine: deck switcher, built-deck serving
// with live reload (SSE), the structured edit API (studio mode), the
// realtime token endpoint, and the async requests/ queue processor.
//
// Serving modes:
//
//	studio  — localhost authoring: everything enabled (default)
//	present — localhost presenting: read-only content + realtime tokens
//	public  — hosted (Railway): audiences are read-only; authenticated
//	          presenters may edit when Git-backed content sync is enabled
package server

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/vessica-labs/vessica-studio/internal/oai"
	"github.com/vessica-labs/vessica-studio/internal/studio"
	"gopkg.in/yaml.v3"
)

type Mode string

const (
	ModeStudio  Mode = "studio"
	ModePresent Mode = "present"
	ModePublic  Mode = "public"
)

type Server struct {
	St   *studio.Studio
	Mode Mode
	OAI  *oai.Client

	mu   sync.Mutex
	subs map[chan string]bool

	// auth (see auth.go)
	secret     string
	ghClientID string
	allowed    map[string]bool
	flows      map[string]githubFlow

	tokenMints  []time.Time         // realtime-token rate limiting
	printJobs   map[string]printJob // one-time keys for in-flight PDF exports (export.go)
	Agent       *agentWorker
	ContentSync *ContentSync
}

func New(st *studio.Studio, mode Mode) *Server {
	c := oai.New(st.Config.OpenAI.BaseURL, st.Config.OpenAI.APIKeyCmd)
	s := &Server{St: st, Mode: mode, OAI: c, subs: map[chan string]bool{}}
	s.initAuth()
	return s
}

func (s *Server) canEdit(r *http.Request) bool {
	if s.Mode == ModeStudio {
		return true
	}
	return s.Mode == ModePublic && s.ContentSync.Editable() && s.isPresenter(r)
}

// ---- SSE hub ----

func (s *Server) Broadcast(event string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for ch := range s.subs {
		select {
		case ch <- event:
		default:
		}
	}
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", 500)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	ch := make(chan string, 8)
	s.mu.Lock()
	s.subs[ch] = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.subs, ch)
		s.mu.Unlock()
	}()
	fmt.Fprintf(w, "event: hello\ndata: vstd\n\n")
	fl.Flush()
	for {
		select {
		case <-r.Context().Done():
			return
		case ev := <-ch:
			name, data := ev, "1"
			if i := strings.Index(ev, "|"); i >= 0 {
				name, data = ev[:i], ev[i+1:]
			}
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", name, data)
			fl.Flush()
		case <-time.After(25 * time.Second):
			fmt.Fprintf(w, ": ping\n\n")
			fl.Flush()
		}
	}
}

// ---- HTTP ----

func (s *Server) Routes() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.handleSwitcher)
	mux.HandleFunc("GET /api/events", s.handleEvents)
	mux.HandleFunc("GET /api/decks", s.handleDecks)
	mux.HandleFunc("GET /d/{deck}/{$}", s.handleDeck)
	mux.HandleFunc("GET /d/{deck}", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/d/"+r.PathValue("deck")+"/", http.StatusFound)
	})
	lib := http.StripPrefix("/library/", http.FileServer(http.Dir(filepath.Join(s.St.Root, "library"))))
	mux.Handle("GET /library/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.hasAnyAccess(r) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		lib.ServeHTTP(w, r)
	}))

	mux.HandleFunc("GET /assets/video/{id}", s.handleVideo)
	mux.HandleFunc("GET /assets/video/{id}/poster", s.handleVideoPoster)
	mux.HandleFunc("POST /api/asset/video", s.editOnly(s.handleVideoUpload))
	mux.HandleFunc("POST /api/asset/image", s.editOnly(s.handleImageUpload))

	mux.HandleFunc("GET /api/deck/{deck}/export.pdf", s.handleExportPDF)
	mux.HandleFunc("GET /api/deck/{deck}/export.pptx", s.handleExportPPTX)
	mux.HandleFunc("GET /api/deck/{deck}/print.html", s.handlePrintHTML)
	mux.HandleFunc("GET /api/deck/{deck}/status", s.handleDeckStatus)
	mux.HandleFunc("GET /api/deck/{deck}/slide/{id}", s.handleGetSlide)
	mux.HandleFunc("PUT /api/deck/{deck}/slide/{id}/fragment", s.editOnly(s.handlePutFragment))
	mux.HandleFunc("PUT /api/deck/{deck}/slide/{id}/companion", s.editOnly(s.handlePutFullCompanion))
	mux.HandleFunc("PUT /api/deck/{deck}/slide/{id}/companion/{section}", s.editOnly(s.handlePutCompanion))
	mux.HandleFunc("POST /api/deck/{deck}/slide/{id}/attachment", s.editOnly(s.handleSourceAttachmentUpload))
	mux.HandleFunc("GET /api/deck/{deck}/source/{name}", s.handleSourceAttachment)
	mux.HandleFunc("PUT /api/deck/{deck}/slide/{id}/title", s.editOnly(s.handlePutTitle))
	mux.HandleFunc("POST /api/deck/{deck}/slides", s.editOnly(s.handleNewSlide))
	mux.HandleFunc("POST /api/deck/{deck}/slide/{id}/move", s.editOnly(s.handleMoveSlide))
	mux.HandleFunc("POST /api/agent/cap", s.editOnly(s.handleAgentCap))
	mux.HandleFunc("POST /api/realtime/token", s.handleRealtimeToken)

	// Phase 4: auth, share links, live-follow, health
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })
	mux.HandleFunc("GET /api/me", s.handleMe)
	mux.HandleFunc("GET /auth/login", s.handleLoginPage)
	mux.HandleFunc("GET /auth/config", s.handleAuthConfig)
	mux.HandleFunc("POST /auth/github/device", s.handleGitHubDevice)
	mux.HandleFunc("POST /auth/github/poll/{id}", s.handleGitHubPoll)
	mux.HandleFunc("GET /v/{deck}/{token}", s.handleShareLanding)
	mux.HandleFunc("POST /api/deck/{deck}/share", s.handleMintShare)
	mux.HandleFunc("GET /api/deck/{deck}/share-qr.png", s.handleShareQR)
	mux.HandleFunc("POST /api/deck/{deck}/presenting", s.handlePresenting)
	mux.HandleFunc("GET /api/content-sync/status", s.handleContentSyncStatus)
	s.VessicaRoutes(mux)      // demo tools: kb, tasks, display, sms, email, search, code
	s.AudienceChatRoutes(mux) // QR-scanned per-person audience chat
	return mux
}

func (s *Server) editOnly(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.canEdit(r) {
			http.Error(w, `{"error":"read-only mode"}`, http.StatusForbidden)
			return
		}
		cw := &statusCapture{ResponseWriter: w, status: http.StatusOK}
		h(cw, r)
		if cw.status < http.StatusBadRequest {
			s.ContentSync.Notify()
			s.Broadcast("reload")
		}
	}
}

type statusCapture struct {
	http.ResponseWriter
	status int
}

func (w *statusCapture) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func (s *Server) handleSwitcher(w http.ResponseWriter, r *http.Request) {
	if s.Mode == ModePublic && !s.isPresenter(r) {
		// audiences arrive via share links; presenters sign in
		http.Redirect(w, r, "/auth/login", http.StatusFound)
		return
	}
	decks, err := s.St.ListDecks()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	var b strings.Builder
	b.WriteString(`<!DOCTYPE html><html><head><title>Vessica Studio</title><style>
body{font-family:'Trebuchet MS',sans-serif;background:#0C2B15;color:#E3FDDB;margin:0;padding:60px}
h1{font-family:Georgia,serif;font-weight:400;font-size:42px;color:#FCFBFA}
a.deck{display:block;background:#12341d;border:1px solid #2c4a35;border-radius:14px;
padding:22px 28px;margin:14px 0;color:#E3FDDB;text-decoration:none;font-size:20px;max-width:720px}
a.deck:hover{border-color:#21BF61}
.sub{color:#8fb59a;font-size:14px;margin-top:4px}
.mode{position:fixed;top:16px;right:20px;border:1px solid #2c4a35;border-radius:999px;padding:4px 14px;font-size:12px;color:#8fb59a}
</style></head><body><div class="mode">mode: ` + string(s.Mode) + `</div><h1>Vessica Studio</h1>`)
	for _, d := range decks {
		meta, err := s.St.LoadDeckMeta(d)
		title := d
		desc := ""
		if err == nil {
			title = meta.Title
			desc = meta.Description
			if meta.ForkedFrom != "" {
				desc = "fork of " + meta.ForkedFrom + " · " + desc
			}
		}
		fmt.Fprintf(&b, `<a class="deck" href="/d/%s/">%s<div class="sub">%s · %s</div></a>`, d, title, d, desc)
	}
	b.WriteString(`</body></html>`)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	io.WriteString(w, b.String())
}

func (s *Server) handleDecks(w http.ResponseWriter, r *http.Request) {
	if s.Mode == ModePublic && !s.isPresenter(r) {
		jsonErr(w, fmt.Errorf("presenter auth required"), http.StatusUnauthorized)
		return
	}
	decks, err := s.St.ListDecks()
	if err != nil {
		jsonErr(w, err, 500)
		return
	}
	type item struct {
		Name, Title, Theme, ForkedFrom string
		Slides                         int
	}
	var out []item
	for _, d := range decks {
		meta, _ := s.St.LoadDeckMeta(d)
		ids, _ := s.St.SlideIDs(d)
		it := item{Name: d, Slides: len(ids)}
		if meta != nil {
			it.Title, it.Theme, it.ForkedFrom = meta.Title, meta.Theme, meta.ForkedFrom
		}
		out = append(out, it)
	}
	writeJSON(w, out)
}

func (s *Server) handleDeck(w http.ResponseWriter, r *http.Request) {
	deck := r.PathValue("deck")
	if !studio.ValidDeckName(deck) {
		http.NotFound(w, r)
		return
	}
	if !s.canView(r, deck) {
		http.Error(w, "This deck requires a share link or presenter sign-in.", http.StatusForbidden)
		return
	}
	out, err := s.St.Build(deck)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	http.ServeFile(w, r, out)
}

func (s *Server) handleGetSlide(w http.ResponseWriter, r *http.Request) {
	if !s.canView(r, r.PathValue("deck")) {
		jsonErr(w, fmt.Errorf("access denied"), http.StatusForbidden)
		return
	}
	frag, comp, err := s.St.ReadSlide(r.PathValue("deck"), r.PathValue("id"))
	if err != nil {
		jsonErr(w, err, 404)
		return
	}
	attachments, _ := s.St.CompanionAttachments(r.PathValue("deck"), r.PathValue("id"))
	for i := range attachments {
		attachments[i].URL = "/api/deck/" + r.PathValue("deck") + "/source/" +
			url.PathEscape(filepath.Base(attachments[i].Path))
	}
	writeJSON(w, map[string]any{
		"fragment": frag, "companion": comp,
		"companion_hash": s.St.HashCompanion(r.PathValue("deck"), r.PathValue("id")),
		"attachments":    attachments,
	})
}

// handlePutFragment writes a slide fragment. When the caller supplies
// X-VSTD-Base-Hash (the fragment hash its DOM was loaded from), the write is
// rejected with 409 if the on-disk fragment has changed since — this is what
// stops a stale edit-mode tab from silently clobbering a redesign pass that
// landed after the tab loaded. Callers without the header (agents, curl,
// cloud sessions) write unconditionally, as before.
func (s *Server) handlePutFragment(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 2<<20))
	if err != nil {
		jsonErr(w, err, 400)
		return
	}
	deck, id := r.PathValue("deck"), r.PathValue("id")
	if base := r.Header.Get("X-VSTD-Base-Hash"); base != "" {
		if cur := s.St.HashSlide(deck, id); cur != "" && cur != base {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusConflict)
			json.NewEncoder(w).Encode(map[string]string{
				"error":   "conflict: the slide changed on disk after this page loaded — reload to pick up the newer version",
				"current": cur,
			})
			return
		}
	}
	if err := s.St.WriteFragment(deck, id, string(body)); err != nil {
		jsonErr(w, err, 400)
		return
	}
	s.St.AppendLog(deck, id, "manual edit saved from player")
	writeJSON(w, map[string]string{"status": "ok", "hash": s.St.HashSlide(deck, id)})
}

func (s *Server) handlePutCompanion(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		jsonErr(w, err, 400)
		return
	}
	err = s.St.UpdateCompanionSection(r.PathValue("deck"), r.PathValue("id"),
		r.PathValue("section"), string(body))
	if err != nil {
		jsonErr(w, err, 400)
		return
	}
	writeJSON(w, map[string]string{"status": "ok"})
}

func (s *Server) handlePutFullCompanion(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 2<<20))
	if err != nil {
		jsonErr(w, err, 400)
		return
	}
	deck, id := r.PathValue("deck"), r.PathValue("id")
	if base := r.Header.Get("X-VSTD-Companion-Hash"); base != "" {
		if cur := s.St.HashCompanion(deck, id); cur != "" && cur != base {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusConflict)
			json.NewEncoder(w).Encode(map[string]string{
				"error":   "conflict: the companion changed after the drawer loaded",
				"current": cur,
			})
			return
		}
	}
	if err := s.St.WriteCompanion(deck, id, string(body)); err != nil {
		jsonErr(w, err, 400)
		return
	}
	writeJSON(w, map[string]string{"status": "ok", "hash": s.St.HashCompanion(deck, id)})
}

func (s *Server) handlePutTitle(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<16))
	title := strings.TrimSpace(string(body))
	if title == "" {
		jsonErr(w, fmt.Errorf("empty title"), 400)
		return
	}
	if err := s.St.SetTitle(r.PathValue("deck"), r.PathValue("id"), title); err != nil {
		jsonErr(w, err, 400)
		return
	}
	writeJSON(w, map[string]string{"status": "ok"})
}

// handleDeckStatus reports background work in flight: slides with unresolved
// "## Edit requests" (redesign pass pending) and queued image generations
// attributed to a slide via the request yaml's deck:/slide: fields.
func (s *Server) handleDeckStatus(w http.ResponseWriter, r *http.Request) {
	if !s.hasAnyAccess(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	deck := r.PathValue("deck")
	if !studio.ValidDeckName(deck) {
		jsonErr(w, fmt.Errorf("invalid deck"), 400)
		return
	}
	type wp struct {
		Kind string `json:"kind"`
		Pct  int    `json:"pct"`
	}
	pending := map[string][]wp{}
	// slides whose generation request is still queued stay on hold
	onHold := map[string]bool{}
	if ents, err := os.ReadDir(filepath.Join(s.St.Root, "requests")); err == nil {
		for _, e := range ents {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
				continue
			}
			b, err := os.ReadFile(filepath.Join(s.St.Root, "requests", e.Name()))
			if err != nil {
				continue
			}
			var req assetRequest
			if yaml.Unmarshal(b, &req) == nil && req.Slide != "" {
				onHold[req.Slide] = true
			}
		}
	}
	if ids, err := s.St.SlideIDs(deck); err == nil {
		for _, id := range ids {
			b, err := os.ReadFile(s.St.SlidePath(deck, id, ".md"))
			if err != nil {
				continue
			}
			c := string(b)
			idx := strings.Index(c, "## Edit requests")
			if idx < 0 {
				continue
			}
			sec := c[idx:]
			if n := strings.Index(sec[1:], "\n## "); n >= 0 {
				sec = sec[:n+1]
			}
			if strings.Contains(sec, "(worker error") {
				pending[id] = append(pending[id], wp{"error", 15})
				continue
			}
			live := false
			for _, l := range strings.Split(sec, "\n") {
				if strings.HasPrefix(l, "- ") && !strings.HasPrefix(l, "- resolved:") && !strings.HasPrefix(l, "- awaiting") {
					live = true
				}
			}
			if !live && strings.Contains(sec, "- awaiting") && !onHold[id] {
				live = true // hold lifted — imagery generated, placement pending
			}
			if !strings.Contains(sec, "(resolved") && live {
				pct := 15
				if i := strings.Index(sec, "(in progress"); i >= 0 {
					pct = 60
					end := i + 48
					if end > len(sec) {
						end = len(sec)
					}
					if m := pctMarkRe.FindStringSubmatch(sec[i:end]); m != nil {
						if v, err := strconv.Atoi(m[1]); err == nil && v > 0 && v < 100 {
							pct = v
						}
					}
				}
				pending[id] = append(pending[id], wp{"redesign", pct})
			}
		}
	}
	queued := 0
	if ents, err := os.ReadDir(filepath.Join(s.St.Root, "requests")); err == nil {
		for _, e := range ents {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
				continue
			}
			queued++
			b, err := os.ReadFile(filepath.Join(s.St.Root, "requests", e.Name()))
			if err != nil {
				continue
			}
			var req assetRequest
			if yaml.Unmarshal(b, &req) != nil {
				continue
			}
			if (req.Deck == "" || req.Deck == deck) && req.Slide != "" {
				pct := 15
				if _, busy := genNow.Load(e.Name()); busy {
					pct = 60
				}
				pending[req.Slide] = append(pending[req.Slide], wp{"image", pct})
			}
		}
	}
	resp := map[string]any{
		"pending": pending, "imageQueue": queued,
		"agent": map[string]any{"enabled": false},
	}
	if s.Agent != nil {
		resp["agent"] = s.Agent.Info()
	}
	writeJSON(w, resp)
}

func (s *Server) handleMoveSlide(w http.ResponseWriter, r *http.Request) {
	var req struct{ After string }
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&req); err != nil {
		jsonErr(w, err, 400)
		return
	}
	newID, err := s.St.MoveSlide(r.PathValue("deck"), r.PathValue("id"), req.After)
	if err != nil {
		jsonErr(w, err, 400)
		return
	}
	writeJSON(w, map[string]string{"status": "ok", "id": newID})
}

func (s *Server) handleAgentCap(w http.ResponseWriter, r *http.Request) {
	if s.Agent == nil {
		jsonErr(w, fmt.Errorf("agent worker not running"), 400)
		return
	}
	var req struct{ MaxPerHour int }
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<12)).Decode(&req); err != nil {
		jsonErr(w, err, 400)
		return
	}
	writeJSON(w, map[string]any{"status": "ok", "maxPerHour": s.Agent.SetMax(req.MaxPerHour)})
}

func (s *Server) handleNewSlide(w http.ResponseWriter, r *http.Request) {
	var req struct{ ID, Title, HTML string }
	if err := json.NewDecoder(io.LimitReader(r.Body, 2<<20)).Decode(&req); err != nil {
		jsonErr(w, err, 400)
		return
	}
	if err := s.St.NewSlide(r.PathValue("deck"), req.ID, req.Title, req.HTML); err != nil {
		jsonErr(w, err, 400)
		return
	}
	writeJSON(w, map[string]string{"status": "ok", "id": req.ID})
}

func (s *Server) handleRealtimeToken(w http.ResponseWriter, r *http.Request) {
	if !s.isPresenter(r) {
		jsonErr(w, fmt.Errorf("presenter auth required"), http.StatusUnauthorized)
		return
	}
	// rate limit: 10 mints per 5 minutes
	s.mu.Lock()
	cut := time.Now().Add(-5 * time.Minute)
	kept := s.tokenMints[:0]
	for _, t := range s.tokenMints {
		if t.After(cut) {
			kept = append(kept, t)
		}
	}
	s.tokenMints = append(kept, time.Now())
	over := len(s.tokenMints) > 10
	s.mu.Unlock()
	if over {
		jsonErr(w, fmt.Errorf("rate limit: too many realtime sessions"), http.StatusTooManyRequests)
		return
	}
	body, code, err := s.OAI.MintRealtimeToken(
		s.St.Config.OpenAI.RealtimeTokenPath, s.St.Config.OpenAI.RealtimeModel)
	if err != nil {
		jsonErr(w, err, 502)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	w.Write(body)
}

// ---- watcher + requests queue ----

// Watch polls the content tree and (a) broadcasts reload events on change,
// (b) in studio mode, processes queued asset requests from requests/*.yaml.
func (s *Server) Watch(interval time.Duration) {
	last := s.scan()
	for {
		time.Sleep(interval)
		cur := s.scan()
		if cur != last {
			last = cur
			log.Printf("change detected — broadcasting reload")
			s.Broadcast("reload")
		}
		if s.Mode == ModeStudio {
			s.processRequests()
		}
	}
}

// scan fingerprints mtimes of everything that affects built decks.
func (s *Server) scan() string {
	var b strings.Builder
	roots := []string{filepath.Join(s.St.Root, "decks"), filepath.Join(s.St.Root, "themes"),
		filepath.Join(s.St.Root, "library")}
	for _, root := range roots {
		filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			if info.IsDir() {
				if info.Name() == "build" {
					return filepath.SkipDir
				}
				return nil
			}
			fmt.Fprintf(&b, "%s:%d;", p, info.ModTime().UnixNano())
			return nil
		})
	}
	return b.String()
}

// genNow tracks request files currently being generated, for status pct.
var genNow sync.Map

// pctMarkRe extracts an explicit percentage from an "(in progress — 40%)" marker.
var pctMarkRe = regexp.MustCompile(`([0-9]{1,2})%`)

type assetRequest struct {
	Type   string   `yaml:"type,omitempty"` // "" | "image" (default) | "video"
	Prompt string   `yaml:"prompt"`
	Path   string   `yaml:"path,omitempty"` // video: file to ingest, relative to the studio root
	Family string   `yaml:"family"`
	Tags   []string `yaml:"tags"`
	Size   string   `yaml:"size"`
	Slug   string   `yaml:"slug"`
	Deck   string   `yaml:"deck,omitempty"`
	Slide  string   `yaml:"slide,omitempty"`
}

func (s *Server) processRequests() {
	dir := filepath.Join(s.St.Root, "requests")
	ents, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		p := filepath.Join(dir, e.Name())
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var req assetRequest
		if err := yaml.Unmarshal(b, &req); err != nil {
			log.Printf("requests: skipping malformed %s", e.Name())
			s.archiveRequest(p, e.Name(), "malformed")
			continue
		}
		if req.Type == "video" {
			if req.Path == "" {
				log.Printf("requests: video request %s missing path:", e.Name())
				s.archiveRequest(p, e.Name(), "malformed")
				continue
			}
			s.processVideoRequest(p, e.Name(), req)
			continue
		}
		if req.Prompt == "" {
			log.Printf("requests: skipping malformed %s", e.Name())
			s.archiveRequest(p, e.Name(), "malformed")
			continue
		}
		if !s.OAI.HasKey() {
			return // leave queued until a key is configured
		}
		log.Printf("requests: generating asset for %s", e.Name())
		genNow.Store(e.Name(), true)
		asset, err := s.OAI.GenerateAsset(filepath.Join(s.St.Root, "library"),
			s.St.Config.OpenAI.ImageModel, req.Prompt, req.Family, req.Size, req.Slug, req.Tags, false)
		genNow.Delete(e.Name())
		if err != nil {
			log.Printf("requests: %s failed: %v", e.Name(), err)
			s.archiveRequest(p, e.Name(), "failed")
			continue
		}
		log.Printf("requests: created asset %s", asset.ID)
		s.archiveRequest(p, e.Name(), "done")
		s.Broadcast("reload")
	}
}

func (s *Server) archiveRequest(p, name, status string) {
	done := filepath.Join(s.St.Root, "requests", "done")
	os.MkdirAll(done, 0o755)
	os.Rename(p, filepath.Join(done, status+"-"+name))
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func jsonErr(w http.ResponseWriter, err error, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}
