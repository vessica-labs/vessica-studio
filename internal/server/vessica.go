// Vessica demo tools (Phase 1): the engine side of the on-stage agent's
// real-world reach. Every capability is a thin endpoint under /api/vessica/*
// that the player's realtime voice session calls as a function tool. Durable
// state lives under <root>/_vessica/<deck>/ (gitignored — the KB holds
// audience PII and must never be committed):
//
//	kb/*.md        presentation knowledge base (company brief, participants)
//	tasks.md       Vessica's durable task list (markdown checklist)
//	display.html   current Live Display content
//	inbox.json     inbound SMS replies (via Telnyx webhook)
//	sent.json      outbound log; maps phone numbers -> {deck, name}
//	artifacts/     files produced by code-interpreter runs
//
// Secrets stay in the engine environment: TELNYX_API_KEY, TELNYX_FROM_NUMBER,
// RESEND_API_KEY, RESEND_FROM, VSTD_TOOLS_MODEL (Responses API model for
// web_search / code_interpreter; default gpt-5.5).
package server

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/vessica-labs/vessica-studio/internal/studio"
)

// ---- storage helpers ----

var vesMu sync.Mutex // serializes _vessica file mutations

func (s *Server) vesDir(deck string, parts ...string) string {
	p := filepath.Join(append([]string{s.St.Root, "_vessica", deck}, parts...)...)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		// surface the real cause (e.g. permission denied on the studio root)
		// instead of letting the caller's write fail with a bare ENOENT
		log.Printf("vessica: cannot create %s: %v", filepath.Dir(p), err)
	}
	return p
}

var safeNameRe = regexp.MustCompile(`^[a-zA-Z0-9._ -]{1,80}\.md$`)
var safeArtRe = regexp.MustCompile(`^[a-zA-Z0-9._-]{1,120}$`)

func (s *Server) vesGate(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.isPresenter(r) {
			jsonErr(w, fmt.Errorf("presenter auth required"), http.StatusUnauthorized)
			return
		}
		if d := r.PathValue("deck"); d != "" && !studio.ValidDeckName(d) {
			jsonErr(w, fmt.Errorf("invalid deck"), 400)
			return
		}
		h(w, r)
	}
}

// VessicaRoutes registers the demo-tool endpoints on mux.
func (s *Server) VessicaRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/vessica/{deck}/kb", s.vesGate(s.handleKBList))
	mux.HandleFunc("GET /api/vessica/{deck}/kb/{name}", s.vesGate(s.handleKBRead))
	mux.HandleFunc("POST /api/vessica/{deck}/kb/{name}", s.vesGate(s.handleKBWrite))
	mux.HandleFunc("GET /api/vessica/{deck}/tasks", s.vesGate(s.handleTasksGet))
	mux.HandleFunc("POST /api/vessica/{deck}/tasks", s.vesGate(s.handleTasksPost))
	mux.HandleFunc("GET /api/vessica/{deck}/display", s.vesGate(s.handleDisplayGet))
	mux.HandleFunc("POST /api/vessica/{deck}/display", s.vesGate(s.handleDisplayPost))
	mux.HandleFunc("POST /api/vessica/{deck}/sms", s.vesGate(s.handleSMS))
	mux.HandleFunc("POST /api/vessica/{deck}/email", s.vesGate(s.handleEmail))
	mux.HandleFunc("POST /api/vessica/{deck}/search", s.vesGate(s.handleWebSearch))
	mux.HandleFunc("POST /api/vessica/{deck}/code", s.vesGate(s.handleRunCode))
	mux.HandleFunc("GET /api/vessica/{deck}/inbox", s.vesGate(s.handleInbox))
	mux.HandleFunc("GET /api/vessica/{deck}/artifact/{name}", s.vesGate(s.handleArtifact))
	mux.HandleFunc("POST /api/vessica/{deck}/call", s.vesGate(s.handleCall))
	// Telnyx webhook: unauthenticated by design (Telnyx calls it); inert
	// unless TELNYX_API_KEY is configured.
	mux.HandleFunc("POST /api/telnyx/webhook", s.handleTelnyxWebhook)
	mux.HandleFunc("GET /api/telnyx/media", s.handleTelnyxMedia)
}

// ---- knowledge base ----

func (s *Server) handleKBList(w http.ResponseWriter, r *http.Request) {
	deck := r.PathValue("deck")
	dir := filepath.Join(s.St.Root, "_vessica", deck, "kb")
	type doc struct {
		Name  string `json:"name"`
		Bytes int    `json:"bytes"`
	}
	docs := []doc{}
	var digest strings.Builder
	if ents, err := os.ReadDir(dir); err == nil {
		sort.Slice(ents, func(i, j int) bool { return ents[i].Name() < ents[j].Name() })
		for _, e := range ents {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
				continue
			}
			b, err := os.ReadFile(filepath.Join(dir, e.Name()))
			if err != nil {
				continue
			}
			docs = append(docs, doc{e.Name(), len(b)})
			// digest: full doc up to 6 KB each, capped at ~24 KB total —
			// presentation KBs are small; she should "just know" them.
			c := string(b)
			if len(c) > 6*1024 {
				c = c[:6*1024] + "\n…(truncated — use kb_read for the rest)"
			}
			if digest.Len() < 24*1024 {
				fmt.Fprintf(&digest, "\n\n===== %s =====\n%s", e.Name(), c)
			}
		}
	}
	writeJSON(w, map[string]any{"docs": docs, "digest": digest.String()})
}

func (s *Server) handleKBRead(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !safeNameRe.MatchString(name) {
		jsonErr(w, fmt.Errorf("invalid kb doc name"), 400)
		return
	}
	b, err := os.ReadFile(filepath.Join(s.St.Root, "_vessica", r.PathValue("deck"), "kb", name))
	if err != nil {
		jsonErr(w, fmt.Errorf("kb doc not found"), 404)
		return
	}
	writeJSON(w, map[string]string{"name": name, "content": string(b)})
}

func (s *Server) handleKBWrite(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !safeNameRe.MatchString(name) {
		jsonErr(w, fmt.Errorf("kb doc name must be a simple .md filename"), 400)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil || len(body) == 0 {
		jsonErr(w, fmt.Errorf("empty upload"), 400)
		return
	}
	deck := r.PathValue("deck")
	if err := os.WriteFile(s.vesDir(deck, "kb", name), body, 0o600); err != nil {
		jsonErr(w, err, 500)
		return
	}
	s.Broadcast("vkb|" + deck)
	writeJSON(w, map[string]string{"status": "ok", "name": name})
}

// ---- durable task list ----

// tasks.md format:  # Project: <name>\n- [ ] item\n- [x] item — note
type vesTask struct {
	Text string `json:"text"`
	Done bool   `json:"done"`
	Note string `json:"note,omitempty"`
}

func parseTasks(c string) (project string, items []vesTask) {
	for _, l := range strings.Split(c, "\n") {
		l = strings.TrimRight(l, " ")
		if strings.HasPrefix(l, "# Project: ") {
			project = strings.TrimPrefix(l, "# Project: ")
		} else if strings.HasPrefix(l, "- [ ] ") {
			items = append(items, vesTask{Text: strings.TrimPrefix(l, "- [ ] ")})
		} else if strings.HasPrefix(l, "- [x] ") {
			t := strings.TrimPrefix(l, "- [x] ")
			note := ""
			if i := strings.Index(t, " — "); i >= 0 {
				t, note = t[:i], t[i+len(" — "):]
			}
			items = append(items, vesTask{Text: t, Done: true, Note: note})
		}
	}
	return
}

func renderTasks(project string, items []vesTask) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Project: %s\n\n", project)
	for _, t := range items {
		if t.Done {
			if t.Note != "" {
				fmt.Fprintf(&b, "- [x] %s — %s\n", t.Text, t.Note)
			} else {
				fmt.Fprintf(&b, "- [x] %s\n", t.Text)
			}
		} else {
			fmt.Fprintf(&b, "- [ ] %s\n", t.Text)
		}
	}
	return b.String()
}

func (s *Server) handleTasksGet(w http.ResponseWriter, r *http.Request) {
	b, _ := os.ReadFile(filepath.Join(s.St.Root, "_vessica", r.PathValue("deck"), "tasks.md"))
	project, items := parseTasks(string(b))
	writeJSON(w, map[string]any{"project": project, "items": items})
}

// POST body: {op:"create", project, items:[..]} | {op:"add", items:[..]}
// | {op:"check", item, note} | {op:"clear"}
func (s *Server) handleTasksPost(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Op      string   `json:"op"`
		Project string   `json:"project"`
		Items   []string `json:"items"`
		Item    string   `json:"item"`
		Note    string   `json:"note"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<18)).Decode(&req); err != nil {
		jsonErr(w, err, 400)
		return
	}
	deck := r.PathValue("deck")
	vesMu.Lock()
	defer vesMu.Unlock()
	path := s.vesDir(deck, "tasks.md")
	b, _ := os.ReadFile(path)
	project, items := parseTasks(string(b))
	switch req.Op {
	case "create":
		project = req.Project
		items = nil
		for _, t := range req.Items {
			items = append(items, vesTask{Text: t})
		}
	case "add":
		for _, t := range req.Items {
			items = append(items, vesTask{Text: t})
		}
	case "check":
		found := false
		for i := range items {
			if !items[i].Done && strings.Contains(strings.ToLower(items[i].Text), strings.ToLower(req.Item)) {
				items[i].Done, items[i].Note, found = true, req.Note, true
				break
			}
		}
		if !found {
			jsonErr(w, fmt.Errorf("no open task matching %q", req.Item), 404)
			return
		}
	case "clear":
		project, items = "", nil
	default:
		jsonErr(w, fmt.Errorf("op must be create|add|check|clear"), 400)
		return
	}
	if err := os.WriteFile(path, []byte(renderTasks(project, items)), 0o644); err != nil {
		jsonErr(w, err, 500)
		return
	}
	s.Broadcast("vtasks|" + deck)
	writeJSON(w, map[string]any{"project": project, "items": items})
}

// ---- Live Display ----

func (s *Server) handleDisplayGet(w http.ResponseWriter, r *http.Request) {
	b, _ := os.ReadFile(filepath.Join(s.St.Root, "_vessica", r.PathValue("deck"), "display.html"))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(b)
}

// POST body: {op:"render"|"append"|"clear", html}
func (s *Server) handleDisplayPost(w http.ResponseWriter, r *http.Request) {
	var req struct{ Op, HTML string }
	if err := json.NewDecoder(io.LimitReader(r.Body, 2<<20)).Decode(&req); err != nil {
		jsonErr(w, err, 400)
		return
	}
	deck := r.PathValue("deck")
	vesMu.Lock()
	defer vesMu.Unlock()
	path := s.vesDir(deck, "display.html")
	switch req.Op {
	case "render":
		os.WriteFile(path, []byte(req.HTML), 0o644)
	case "append":
		cur, _ := os.ReadFile(path)
		os.WriteFile(path, append(cur, []byte(req.HTML)...), 0o644)
	case "clear":
		os.WriteFile(path, nil, 0o644)
	default:
		jsonErr(w, fmt.Errorf("op must be render|append|clear"), 400)
		return
	}
	s.Broadcast("vdisplay|" + deck)
	writeJSON(w, map[string]string{"status": "ok"})
}

// ---- outbound SMS + inbound webhook (Telnyx) ----

type sentEntry struct {
	Deck string `json:"deck"`
	Name string `json:"name"`
	Time string `json:"time"`
}

func (s *Server) rememberSent(deck, number, name string) {
	vesMu.Lock()
	defer vesMu.Unlock()
	path := filepath.Join(s.St.Root, "_vessica", "sent.json")
	os.MkdirAll(filepath.Dir(path), 0o755)
	m := map[string]sentEntry{}
	if b, err := os.ReadFile(path); err == nil {
		json.Unmarshal(b, &m)
	}
	m[number] = sentEntry{deck, name, time.Now().Format(time.RFC3339)}
	b, _ := json.MarshalIndent(m, "", " ")
	os.WriteFile(path, b, 0o600)
}

func (s *Server) lookupSent(number string) (sentEntry, bool) {
	vesMu.Lock()
	defer vesMu.Unlock()
	m := map[string]sentEntry{}
	if b, err := os.ReadFile(filepath.Join(s.St.Root, "_vessica", "sent.json")); err == nil {
		json.Unmarshal(b, &m)
	}
	e, ok := m[number]
	return e, ok
}

var e164Re = regexp.MustCompile(`^\+[0-9]{7,15}$`)

// normalizeE164 accepts human-formatted phone numbers — "(415) 555-2671",
// "415.555.2671", "1-415-555-2671", "+1 415 555 2671" — and returns strict
// E.164. Bare 10-digit numbers are assumed US (+1). Returns "" if it can't
// make a valid number.
func normalizeE164(raw string) string {
	digits := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, raw)
	hadPlus := strings.HasPrefix(strings.TrimSpace(raw), "+")
	var out string
	switch {
	case hadPlus:
		out = "+" + digits
	case len(digits) == 10:
		out = "+1" + digits // bare US number
	case len(digits) == 11 && digits[0] == '1':
		out = "+" + digits
	default:
		out = "+" + digits
	}
	if !e164Re.MatchString(out) {
		return ""
	}
	return out
}

func (s *Server) handleSMS(w http.ResponseWriter, r *http.Request) {
	key, from := os.Getenv("TELNYX_API_KEY"), os.Getenv("TELNYX_FROM_NUMBER")
	if key == "" || from == "" {
		jsonErr(w, fmt.Errorf("texting not configured (TELNYX_API_KEY / TELNYX_FROM_NUMBER)"), 501)
		return
	}
	var req struct{ To, ToName, Body string }
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&req); err != nil {
		jsonErr(w, err, 400)
		return
	}
	req.To = normalizeE164(req.To)
	if req.To == "" {
		jsonErr(w, fmt.Errorf("to is not a valid phone number — use E.164 like +15551234567 (US formats like (415) 555-2671 are auto-normalized)"), 400)
		return
	}
	if strings.TrimSpace(req.Body) == "" {
		jsonErr(w, fmt.Errorf("empty message body"), 400)
		return
	}
	payload := map[string]string{"from": from, "to": req.To, "text": req.Body}
	body, code, err := postJSON("https://api.telnyx.com/v2/messages", key, payload)
	if err != nil {
		jsonErr(w, err, 502)
		return
	}
	if code >= 300 {
		jsonErr(w, fmt.Errorf("telnyx: %s", trim(body, 400)), 502)
		return
	}
	deck := r.PathValue("deck")
	s.rememberSent(deck, req.To, req.ToName)
	s.appendInbox(deck, inboxMsg{Dir: "out", Channel: "sms", With: req.ToName,
		Number: req.To, Text: req.Body, Time: time.Now().Format(time.RFC3339)})
	writeJSON(w, map[string]string{"status": "sent", "to": req.To})
}

type inboxMsg struct {
	Dir     string `json:"dir"` // in | out
	Channel string `json:"channel"`
	With    string `json:"with,omitempty"`
	Number  string `json:"number,omitempty"`
	Text    string `json:"text"`
	Time    string `json:"time"`
}

func (s *Server) appendInbox(deck string, m inboxMsg) {
	vesMu.Lock()
	defer vesMu.Unlock()
	path := s.vesDir(deck, "inbox.json")
	var msgs []inboxMsg
	if b, err := os.ReadFile(path); err == nil {
		json.Unmarshal(b, &msgs)
	}
	msgs = append(msgs, m)
	if len(msgs) > 500 {
		msgs = msgs[len(msgs)-500:]
	}
	b, _ := json.MarshalIndent(msgs, "", " ")
	os.WriteFile(path, b, 0o600)
}

func (s *Server) handleInbox(w http.ResponseWriter, r *http.Request) {
	var msgs []inboxMsg
	if b, err := os.ReadFile(filepath.Join(s.St.Root, "_vessica", r.PathValue("deck"), "inbox.json")); err == nil {
		json.Unmarshal(b, &msgs)
	}
	if msgs == nil {
		msgs = []inboxMsg{}
	}
	writeJSON(w, map[string]any{"messages": msgs})
}

// handleTelnyxWebhook receives message.received events. The reply is matched
// to a deck via the outbound sent-map; unmatched senders are ignored.
func (s *Server) handleTelnyxWebhook(w http.ResponseWriter, r *http.Request) {
	if os.Getenv("TELNYX_API_KEY") == "" {
		http.Error(w, "not configured", 501)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<18))
	if err != nil {
		http.Error(w, "bad body", 400)
		return
	}
	var ev struct {
		Data struct {
			EventType string `json:"event_type"`
			Payload   struct {
				Text          string          `json:"text"`
				CallControlID string          `json:"call_control_id"`
				From          json.RawMessage `json:"from"`
			} `json:"payload"`
		} `json:"data"`
	}
	if json.Unmarshal(body, &ev) != nil {
		w.Write([]byte("ok"))
		return
	}
	if strings.HasPrefix(ev.Data.EventType, "call.") {
		s.handleCallEvent(ev.Data.EventType, ev.Data.Payload.CallControlID)
		w.Write([]byte("ok"))
		return
	}
	if ev.Data.EventType != "message.received" {
		w.Write([]byte("ok")) // ack everything else (delivery receipts, …)
		return
	}
	var fromObj struct {
		PhoneNumber string `json:"phone_number"`
	}
	json.Unmarshal(ev.Data.Payload.From, &fromObj)
	num, text := fromObj.PhoneNumber, strings.TrimSpace(ev.Data.Payload.Text)
	ent, ok := s.lookupSent(num)
	if !ok || text == "" {
		w.Write([]byte("ok"))
		return
	}
	m := inboxMsg{Dir: "in", Channel: "sms", With: ent.Name, Number: num,
		Text: text, Time: time.Now().Format(time.RFC3339)}
	s.appendInbox(ent.Deck, m)
	b, _ := json.Marshal(map[string]any{"deck": ent.Deck, "from": ent.Name, "number": num, "text": text})
	s.Broadcast("vinbox|" + string(b))
	w.Write([]byte("ok"))
}

// ---- outbound email (Resend) ----

func (s *Server) handleEmail(w http.ResponseWriter, r *http.Request) {
	key, from := os.Getenv("RESEND_API_KEY"), os.Getenv("RESEND_FROM")
	if key == "" || from == "" {
		jsonErr(w, fmt.Errorf("email not configured (RESEND_API_KEY / RESEND_FROM)"), 501)
		return
	}
	var req struct{ To, ToName, Subject, Body string }
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<18)).Decode(&req); err != nil {
		jsonErr(w, err, 400)
		return
	}
	req.To = strings.TrimSpace(req.To)
	if !strings.Contains(req.To, "@") || req.Subject == "" || req.Body == "" {
		jsonErr(w, fmt.Errorf("need to (email), subject, body"), 400)
		return
	}
	payload := map[string]any{"from": from, "to": []string{req.To},
		"subject": req.Subject, "text": req.Body}
	body, code, err := postJSON("https://api.resend.com/emails", key, payload)
	if err != nil {
		jsonErr(w, err, 502)
		return
	}
	if code >= 300 {
		jsonErr(w, fmt.Errorf("resend: %s", trim(body, 400)), 502)
		return
	}
	deck := r.PathValue("deck")
	s.appendInbox(deck, inboxMsg{Dir: "out", Channel: "email", With: req.ToName,
		Number: req.To, Text: req.Subject + " — " + req.Body, Time: time.Now().Format(time.RFC3339)})
	writeJSON(w, map[string]string{"status": "sent", "to": req.To})
}

// ---- web search + code interpreter (OpenAI Responses API, server-side) ----

func toolsModel() string {
	if m := os.Getenv("VSTD_TOOLS_MODEL"); m != "" {
		return m
	}
	return "gpt-5.5"
}

// responsesText extracts the assistant text from a Responses API result.
func responsesText(body []byte) string {
	var res struct {
		Output []struct {
			Type    string `json:"type"`
			Content []struct {
				Type        string `json:"type"`
				Text        string `json:"text"`
				Annotations []struct {
					Type        string `json:"type"`
					ContainerID string `json:"container_id"`
					FileID      string `json:"file_id"`
					Filename    string `json:"filename"`
				} `json:"annotations"`
			} `json:"content"`
		} `json:"output"`
	}
	if json.Unmarshal(body, &res) != nil {
		return ""
	}
	var b strings.Builder
	for _, o := range res.Output {
		if o.Type != "message" {
			continue
		}
		for _, c := range o.Content {
			if c.Type == "output_text" && c.Text != "" {
				if b.Len() > 0 {
					b.WriteString("\n")
				}
				b.WriteString(c.Text)
			}
		}
	}
	return b.String()
}

func (s *Server) handleWebSearch(w http.ResponseWriter, r *http.Request) {
	if !s.OAI.HasKey() {
		jsonErr(w, fmt.Errorf("search not configured (no OpenAI key)"), 501)
		return
	}
	var req struct{ Query string }
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&req); err != nil || strings.TrimSpace(req.Query) == "" {
		jsonErr(w, fmt.Errorf("need query"), 400)
		return
	}
	payload := map[string]any{
		"model": toolsModel(),
		"tools": []map[string]any{{"type": "web_search"}},
		"input": req.Query,
		"instructions": "Search the web and answer concisely for a live spoken " +
			"presentation context. Lead with the answer; include 1-3 source names inline.",
	}
	body, code, err := s.OAI.PostJSON("/responses", payload)
	if err != nil {
		jsonErr(w, err, 502)
		return
	}
	if code >= 300 {
		jsonErr(w, fmt.Errorf("responses api: %s", trim(body, 400)), 502)
		return
	}
	writeJSON(w, map[string]string{"result": responsesText(body)})
}

func (s *Server) handleRunCode(w http.ResponseWriter, r *http.Request) {
	if !s.OAI.HasKey() {
		jsonErr(w, fmt.Errorf("code tool not configured (no OpenAI key)"), 501)
		return
	}
	var req struct{ Task string }
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<18)).Decode(&req); err != nil || strings.TrimSpace(req.Task) == "" {
		jsonErr(w, fmt.Errorf("need task"), 400)
		return
	}
	deck := r.PathValue("deck")
	payload := map[string]any{
		"model": toolsModel(),
		"tools": []map[string]any{{"type": "code_interpreter", "container": map[string]string{"type": "auto"}}},
		"input": req.Task,
		"instructions": "Use the code interpreter to complete the task. If a chart or " +
			"image is the right output, produce it as a PNG file. Reply with a short " +
			"summary of what you computed.",
	}
	body, code, err := s.OAI.PostJSON("/responses", payload)
	if err != nil {
		jsonErr(w, err, 502)
		return
	}
	if code >= 300 {
		jsonErr(w, fmt.Errorf("responses api: %s", trim(body, 400)), 502)
		return
	}
	// collect any produced files (container file citations) into artifacts/
	var res struct {
		Output []struct {
			Type    string `json:"type"`
			Content []struct {
				Annotations []struct {
					Type        string `json:"type"`
					ContainerID string `json:"container_id"`
					FileID      string `json:"file_id"`
					Filename    string `json:"filename"`
				} `json:"annotations"`
			} `json:"content"`
		} `json:"output"`
	}
	json.Unmarshal(body, &res)
	var arts []string
	seen := map[string]bool{}
	for _, o := range res.Output {
		for _, c := range o.Content {
			for _, a := range c.Annotations {
				if a.Type != "container_file_citation" || a.FileID == "" || seen[a.FileID] {
					continue
				}
				seen[a.FileID] = true
				name := a.Filename
				if name == "" || !safeArtRe.MatchString(name) {
					name = a.FileID + ".bin"
				}
				fb, _, err := s.OAI.GetRaw("/containers/" + a.ContainerID + "/files/" + a.FileID + "/content")
				if err != nil || len(fb) == 0 {
					continue
				}
				if os.WriteFile(s.vesDir(deck, "artifacts", name), fb, 0o644) == nil {
					arts = append(arts, "/api/vessica/"+deck+"/artifact/"+name)
				}
			}
		}
	}
	writeJSON(w, map[string]any{"result": responsesText(body), "artifacts": arts})
}

func (s *Server) handleArtifact(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !safeArtRe.MatchString(name) {
		http.NotFound(w, r)
		return
	}
	http.ServeFile(w, r, filepath.Join(s.St.Root, "_vessica", r.PathValue("deck"), "artifacts", name))
}

// ---- small http helpers ----

func postJSON(url, bearer string, payload any) ([]byte, int, error) {
	b, _ := json.Marshal(payload)
	req, err := http.NewRequest("POST", url, strings.NewReader(string(b)))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+bearer)
	req.Header.Set("Content-Type", "application/json")
	cl := &http.Client{Timeout: 30 * time.Second}
	res, err := cl.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	return body, res.StatusCode, nil
}

func trim(b []byte, n int) string {
	s := string(b)
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}
