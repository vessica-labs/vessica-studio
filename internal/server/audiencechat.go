// Audience web chat: the carrier-free channel for soliciting live input.
//
// Flow: on stage, Vessica calls audience_ask("what I want to learn") then
// show_chat_qr. Each audience member scans the QR (a share-tokened link to
// /chat/<deck>), gives their name, and lands in a one-on-one text chat with
// a per-person Vessica instance (Responses API) briefed with the KB and the
// current ask. Every audience message is appended to the deck inbox and
// pushed to the on-stage session as a vinbox event, so stage-Vessica folds
// input in live. Transcripts mirror to _vessica/<deck>/chats/.
//
// No Telnyx involvement — this replaces broad SMS outreach where 10DLC
// registration isn't in place.
package server

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	mrand "math/rand"

	qrcode "github.com/skip2/go-qrcode"
)

const (
	maxChatSessions = 200
	maxChatTurns    = 40
)

type chatMsg struct {
	Role string `json:"role"` // user | assistant
	Text string `json:"text"`
}

type chatSession struct {
	Deck    string
	Name    string
	mu      sync.Mutex
	history []chatMsg
	turns   int
}

var (
	chatMu   sync.Mutex
	chats    = map[string]*chatSession{}
	chatSeen = 0
)

// AudienceChatRoutes registers the chat page + API + QR.
func (s *Server) AudienceChatRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /chat/{deck}", s.handleChatPage)
	mux.HandleFunc("POST /api/chat/{deck}/session", s.handleChatStart)
	mux.HandleFunc("POST /api/chat/{deck}/session/{sid}", s.handleChatMessage)
	mux.HandleFunc("GET /api/deck/{deck}/chat-qr.png", s.handleChatQR)
	mux.HandleFunc("POST /api/vessica/{deck}/ask", s.vesGate(s.handleSetAsk))
	mux.HandleFunc("GET /api/vessica/{deck}/pulse", s.vesGate(s.handlePulse))
	mux.HandleFunc("POST /api/vessica/{deck}/chat-swarm", s.vesGate(s.handleChatSwarm))
}

// ---- the current "ask" (what stage-Vessica wants to learn) ----

func (s *Server) askPath(deck string) string { return s.vesDir(deck, "ask.txt") }

func (s *Server) currentAsk(deck string) string {
	b, _ := os.ReadFile(s.askPath(deck))
	if len(b) == 0 {
		return "Learn what this audience member most wants to hear about in today's talk, and any question they'd like answered."
	}
	return string(b)
}

func (s *Server) handleSetAsk(w http.ResponseWriter, r *http.Request) {
	var req struct{ Ask string }
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&req); err != nil || strings.TrimSpace(req.Ask) == "" {
		jsonErr(w, fmt.Errorf("need ask — what should the audience chats find out?"), 400)
		return
	}
	deck := r.PathValue("deck")
	if err := os.WriteFile(s.askPath(deck), []byte(strings.TrimSpace(req.Ask)), 0o644); err != nil {
		jsonErr(w, err, 500)
		return
	}
	writeJSON(w, map[string]string{"status": "ok",
		"note": "ask set — now call show_chat_qr so the audience can scan in"})
}

// ---- QR: share-tokened link to the chat page ----

func (s *Server) handleChatQR(w http.ResponseWriter, r *http.Request) {
	deck := r.PathValue("deck")
	if !s.canView(r, deck) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	base := s.St.Config.PublicHost
	if base == "" {
		if pub := strings.TrimRight(os.Getenv("PUBLIC_URL"), "/"); pub != "" {
			base = pub
		} else {
			scheme := "http"
			if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
				scheme = "https"
			}
			base = scheme + "://" + r.Host
		}
	}
	link := strings.TrimRight(base, "/") + "/chat/" + deck + "?t=" + s.MintShare(deck, 24*time.Hour)
	png, err := qrcode.Encode(link, qrcode.Medium, 640)
	if err != nil {
		jsonErr(w, err, 500)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(png)
}

// ---- audience page ----

func (s *Server) chatAccess(w http.ResponseWriter, r *http.Request, deck string) bool {
	// a valid ?t= token grants access and sets the share cookie (QR path)
	if tok := r.URL.Query().Get("t"); tok != "" && s.shareValid(deck, tok) {
		http.SetCookie(w, &http.Cookie{Name: shareCookieName(deck), Value: tok, Path: "/",
			HttpOnly: true, Secure: r.TLS != nil || s.Mode == ModePublic, SameSite: http.SameSiteLaxMode,
			MaxAge: 60 * 60 * 24})
		return true
	}
	return s.canView(r, deck)
}

func (s *Server) handleChatPage(w http.ResponseWriter, r *http.Request) {
	deck := r.PathValue("deck")
	if !s.chatAccess(w, r, deck) {
		http.Error(w, "This chat link is invalid or has expired — scan the QR on screen again.", http.StatusForbidden)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	io.WriteString(w, strings.ReplaceAll(chatPageHTML, "{{DECK}}", deck))
}

// ---- chat sessions (per-person Vessica via the Responses API) ----

func newSID() string {
	b := make([]byte, 12)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func (s *Server) chatInstructions(deck, name string) string {
	return `You are Vessica, Matt Kropp's AI presentation copilot, in a one-on-one text chat with an audience member during his live presentation. Their name: ` + name + `.
WHAT MATT WANTS YOU TO FIND OUT: ` + s.currentAsk(deck) + `
STYLE: warm, brisk, conversational. One to two short sentences per turn, ONE question at a time. React briefly to what they say, follow up once when useful. After you have what Matt needs (2-4 exchanges), thank them by name, tell them their input is landing on the big screen, and let them go — don't drag it out. If they ask you something, answer briefly from the knowledge base and steer back.
KNOWLEDGE BASE:
` + s.kbDigestFor(deck, 12*1024)
}

// chatModel calls the Responses API with the running transcript.
func (s *Server) chatModel(cs *chatSession, deck string) (string, error) {
	input := make([]map[string]string, 0, len(cs.history))
	for _, m := range cs.history {
		input = append(input, map[string]string{"role": m.Role, "content": m.Text})
	}
	payload := map[string]any{
		"model":        toolsModel(),
		"instructions": s.chatInstructions(deck, cs.Name),
		"input":        input,
	}
	body, code, err := s.OAI.PostJSON("/responses", payload)
	if err != nil {
		return "", err
	}
	if code >= 300 {
		return "", fmt.Errorf("responses api: %s", trim(body, 300))
	}
	out := responsesText(body)
	if out == "" {
		return "", fmt.Errorf("empty model reply")
	}
	return out, nil
}

func (s *Server) handleChatStart(w http.ResponseWriter, r *http.Request) {
	deck := r.PathValue("deck")
	if !s.chatAccess(w, r, deck) {
		jsonErr(w, fmt.Errorf("access expired — rescan the QR"), http.StatusForbidden)
		return
	}
	if !s.OAI.HasKey() {
		jsonErr(w, fmt.Errorf("chat not configured (no OpenAI key)"), 501)
		return
	}
	var req struct{ Name string }
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<12)).Decode(&req); err != nil {
		jsonErr(w, err, 400)
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" || len(name) > 60 {
		jsonErr(w, fmt.Errorf("please give a name (up to 60 chars)"), 400)
		return
	}
	chatMu.Lock()
	if chatSeen >= maxChatSessions {
		chatMu.Unlock()
		jsonErr(w, fmt.Errorf("chat is at capacity"), 429)
		return
	}
	chatSeen++
	cs := &chatSession{Deck: deck, Name: name}
	sid := newSID()
	chats[sid] = cs
	chatMu.Unlock()

	cs.mu.Lock()
	defer cs.mu.Unlock()
	cs.history = append(cs.history, chatMsg{Role: "user", Text: "(" + name + " just joined the chat — greet them and start.)"})
	greeting, err := s.chatModel(cs, deck)
	if err != nil {
		jsonErr(w, err, 502)
		return
	}
	cs.history = append(cs.history, chatMsg{Role: "assistant", Text: greeting})
	s.mirrorChat(cs)
	s.appendInbox(deck, inboxMsg{Dir: "out", Channel: "chat", With: name,
		Text: greeting, Time: time.Now().Format(time.RFC3339)})
	writeJSON(w, map[string]string{"sid": sid, "reply": greeting})
}

func (s *Server) handleChatMessage(w http.ResponseWriter, r *http.Request) {
	deck := r.PathValue("deck")
	if !s.chatAccess(w, r, deck) {
		jsonErr(w, fmt.Errorf("access expired — rescan the QR"), http.StatusForbidden)
		return
	}
	chatMu.Lock()
	cs := chats[r.PathValue("sid")]
	chatMu.Unlock()
	if cs == nil || cs.Deck != deck {
		jsonErr(w, fmt.Errorf("chat session not found — reload the page"), 404)
		return
	}
	var req struct{ Text string }
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<14)).Decode(&req); err != nil {
		jsonErr(w, err, 400)
		return
	}
	text := strings.TrimSpace(req.Text)
	if text == "" {
		jsonErr(w, fmt.Errorf("empty message"), 400)
		return
	}
	cs.mu.Lock()
	defer cs.mu.Unlock()
	if cs.turns >= maxChatTurns {
		writeJSON(w, map[string]string{"reply": "Thanks " + cs.Name + " — this chat has wrapped. Enjoy the rest of the talk!"})
		return
	}
	cs.turns++
	cs.history = append(cs.history, chatMsg{Role: "user", Text: text})

	// record first — the audience's words are safe even if the per-person
	// model call hiccups. The stage session is NOT pinged per message: with
	// a room full of people that would stampede the presenter's realtime
	// session. Instead pulseNotify emits a debounced "vpulse" event and the
	// player delivers ONE aggregated update.
	s.appendInbox(deck, inboxMsg{Dir: "in", Channel: "chat", With: cs.Name,
		Text: text, Time: time.Now().Format(time.RFC3339)})
	s.pulseNotify(deck)

	reply, err := s.chatModel(cs, deck)
	if err != nil {
		jsonErr(w, err, 502)
		return
	}
	cs.history = append(cs.history, chatMsg{Role: "assistant", Text: reply})
	s.mirrorChat(cs)
	writeJSON(w, map[string]string{"reply": reply})
}

// mirrorChat writes the running transcript to _vessica/<deck>/chats/.
func (s *Server) mirrorChat(cs *chatSession) {
	var b strings.Builder
	fmt.Fprintf(&b, "# Chat with %s\n\n", cs.Name)
	for _, m := range cs.history {
		who := cs.Name
		if m.Role == "assistant" {
			who = "Vessica"
		}
		fmt.Fprintf(&b, "**%s:** %s\n\n", who, m.Text)
	}
	safe := strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' {
			return r
		}
		return '-'
	}, cs.Name)
	os.WriteFile(s.vesDir(cs.Deck, "chats", safe+".md"), []byte(b.String()), 0o600)
}

// ---- audience pulse: debounced aggregation for the stage session ----

// pulseWindow is the minimum spacing between stage notifications no matter
// how fast audience messages arrive.
const pulseWindow = 12 * time.Second

var pulseMu sync.Mutex
var pulsePending = map[string]int{}
var pulseTimer = map[string]*time.Timer{}
var pulseLast = map[string]time.Time{}

// pulseNotify coalesces chat activity into at most one vpulse broadcast per
// pulseWindow per deck (leading edge when idle, trailing edge under load).
func (s *Server) pulseNotify(deck string) {
	pulseMu.Lock()
	defer pulseMu.Unlock()
	pulsePending[deck]++
	if pulseTimer[deck] != nil {
		return // a flush is already scheduled
	}
	delay := time.Duration(0)
	if since := time.Since(pulseLast[deck]); since < pulseWindow {
		delay = pulseWindow - since
	}
	pulseTimer[deck] = time.AfterFunc(delay, func() {
		pulseMu.Lock()
		n := pulsePending[deck]
		pulsePending[deck] = 0
		pulseLast[deck] = time.Now()
		pulseTimer[deck] = nil
		pulseMu.Unlock()
		if n > 0 {
			ev, _ := json.Marshal(map[string]any{"deck": deck, "new": n})
			s.Broadcast("vpulse|" + string(ev))
		}
	})
}

// handlePulse returns the aggregate view of audience input the stage
// session works from: totals, who has participated, and recent messages.
func (s *Server) handlePulse(w http.ResponseWriter, r *http.Request) {
	deck := r.PathValue("deck")
	var msgs []inboxMsg
	if b, err := os.ReadFile(s.vesDir(deck, "inbox.json")); err == nil {
		json.Unmarshal(b, &msgs)
	}
	total := 0
	seen := map[string]bool{}
	var people []string
	var recent []inboxMsg
	for _, m := range msgs {
		if m.Channel != "chat" || m.Dir != "in" {
			continue
		}
		total++
		if !seen[m.With] {
			seen[m.With] = true
			people = append(people, m.With)
		}
		recent = append(recent, m)
	}
	if len(recent) > 30 {
		recent = recent[len(recent)-30:]
	}
	if people == nil {
		people = []string{}
	}
	if recent == nil {
		recent = []inboxMsg{}
	}
	writeJSON(w, map[string]any{"total": total, "participants": people, "recent": recent})
}

// ---- rehearsal swarm: 20 simulated audience chatters ----
//
// POST /api/vessica/{deck}/chat-swarm (presenter-gated) spins up 20 fake
// participants named Test-* who join over ~5s and chat through the REAL
// pipeline — per-person model sessions, inbox, transcripts, pulse
// debouncing — so the presenter can rehearse the full crowd experience
// (Live Display ticking, [AUDIENCE PULSE] events reaching stage Vessica)
// without a room. Roughly 60-80 Responses-API calls per run.

var swarmMu sync.Mutex
var swarmRunning bool

var swarmNames = []string{"Ava", "Ben", "Carla", "Deepak", "Elena", "Farid",
	"Grace", "Hiro", "Imani", "Jonas", "Katya", "Liam", "Mei", "Noah",
	"Olivia", "Priya", "Quinn", "Rosa", "Sam", "Tomas"}

var swarmLines = []string{
	"I want to hear more about governance and risk",
	"Can Matt show more live demos and less theory?",
	"How do we measure ROI on agent programs?",
	"What does this mean for our workforce?",
	"Curious about the cost curves he mentioned",
	"How do we pick between the agent platforms?",
	"More on security and data privacy please",
	"How do non-engineers actually start building?",
	"What failed in other rollouts? War stories!",
	"How long does a pilot really take?",
	"Interested in the change management side",
	"What should leadership do differently on Monday?",
}

func (s *Server) handleChatSwarm(w http.ResponseWriter, r *http.Request) {
	if !s.OAI.HasKey() {
		jsonErr(w, fmt.Errorf("swarm needs the OpenAI key"), 501)
		return
	}
	swarmMu.Lock()
	if swarmRunning {
		swarmMu.Unlock()
		jsonErr(w, fmt.Errorf("a test swarm is already running"), 429)
		return
	}
	swarmRunning = true
	swarmMu.Unlock()
	deck := r.PathValue("deck")
	go s.runChatSwarm(deck)
	writeJSON(w, map[string]string{"status": "ok",
		"note": "20 simulated audience members joining over the next ~30s"})
}

func (s *Server) runChatSwarm(deck string) {
	defer func() {
		swarmMu.Lock()
		swarmRunning = false
		swarmMu.Unlock()
		log.Printf("chat-swarm: %s done", deck)
	}()
	log.Printf("chat-swarm: %s starting (20 simulated chatters)", deck)
	var wg sync.WaitGroup
	for i, base := range swarmNames {
		wg.Add(1)
		go func(i int, base string) {
			defer wg.Done()
			time.Sleep(time.Duration(mrand.Intn(5000)) * time.Millisecond) // scanning the QR
			cs := &chatSession{Deck: deck, Name: "Test-" + base}
			cs.history = append(cs.history, chatMsg{Role: "user",
				Text: "(" + cs.Name + " just joined the chat — greet them and start.)"})
			greeting, err := s.chatModel(cs, deck)
			if err != nil {
				log.Printf("chat-swarm: %s greeting failed: %v", cs.Name, err)
				return
			}
			cs.history = append(cs.history, chatMsg{Role: "assistant", Text: greeting})
			s.appendInbox(deck, inboxMsg{Dir: "out", Channel: "chat", With: cs.Name,
				Text: greeting, Time: time.Now().Format(time.RFC3339)})
			turns := 2 + mrand.Intn(2)
			for t := 0; t < turns; t++ {
				time.Sleep(time.Duration(1500+mrand.Intn(6000)) * time.Millisecond) // typing
				text := swarmLines[mrand.Intn(len(swarmLines))]
				if t > 0 {
					text = "Also — " + swarmLines[mrand.Intn(len(swarmLines))]
				}
				cs.history = append(cs.history, chatMsg{Role: "user", Text: text})
				s.appendInbox(deck, inboxMsg{Dir: "in", Channel: "chat", With: cs.Name,
					Text: text, Time: time.Now().Format(time.RFC3339)})
				s.pulseNotify(deck)
				reply, err := s.chatModel(cs, deck)
				if err != nil {
					log.Printf("chat-swarm: %s turn %d failed: %v", cs.Name, t, err)
					return
				}
				cs.history = append(cs.history, chatMsg{Role: "assistant", Text: reply})
				s.mirrorChat(cs)
			}
		}(i, base)
	}
	wg.Wait()
}

// ---- the audience page (self-contained, mobile-first) ----

const chatPageHTML = `<!DOCTYPE html>
<html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1, maximum-scale=1">
<title>Chat with Vessica</title><style>
:root{--deep:#0C2B15;--mint:#20E3AC;--lime:#96F977;--off:#FCFBFA;--ink:#E3FDDB}
*{box-sizing:border-box}
body{margin:0;font-family:'Trebuchet MS',-apple-system,sans-serif;background:radial-gradient(120% 100% at 50% 0%,#0a2412 0%,#05130a 60%,#020a05 100%);color:var(--ink);min-height:100dvh;display:flex;flex-direction:column}
header{padding:18px 20px 10px;text-align:center}
header h1{font-family:Georgia,serif;font-weight:400;font-size:26px;margin:0;color:var(--off)}
header p{margin:6px 0 0;font-size:14px;color:#8fb59a}
#log{flex:1;overflow-y:auto;padding:14px 16px;display:flex;flex-direction:column;gap:10px}
.msg{max-width:85%;padding:11px 14px;border-radius:16px;font-size:16px;line-height:1.45;white-space:pre-wrap}
.v{align-self:flex-start;background:#12341d;border:1px solid #2c4a35;border-bottom-left-radius:4px}
.u{align-self:flex-end;background:var(--mint);color:#06140c;border-bottom-right-radius:4px}
.sys{align-self:center;color:#8fb59a;font-size:13px}
form{display:flex;gap:8px;padding:12px 14px calc(12px + env(safe-area-inset-bottom))}
input,button{font-size:16px;border-radius:12px;border:1px solid #2c4a35}
input{flex:1;padding:12px 14px;background:#0d2414;color:var(--ink);outline:none}
input:focus{border-color:var(--mint)}
button{padding:12px 18px;background:var(--mint);color:#06140c;font-weight:700;border:none;cursor:pointer}
button:disabled{opacity:.5}
#nameCard{margin:auto;text-align:center;padding:28px;max-width:340px}
#nameCard .dot{width:10px;height:10px;border-radius:50%;background:var(--mint);display:inline-block;margin-right:8px;animation:p 1.6s infinite}
@keyframes p{50%{opacity:.3}}
</style></head><body>
<header><h1>Vessica</h1><p id="sub">Matt&rsquo;s AI presentation copilot &middot; live</p></header>
<div id="log" style="display:none"></div>
<div id="nameCard">
  <p style="font-size:17px;line-height:1.5"><span class="dot"></span>Hi! I&rsquo;m helping Matt shape this session in real time. What&rsquo;s your first name?</p>
  <form id="nameForm" style="padding:0"><input id="nameIn" placeholder="Your name" autocomplete="given-name" maxlength="60" required>
  <button>Start</button></form>
</div>
<form id="chatForm" style="display:none"><input id="chatIn" placeholder="Type a reply&hellip;" autocomplete="off" required><button id="sendBtn">Send</button></form>
<script>
const deck='{{DECK}}';let sid=null;
const log=document.getElementById('log'),chatForm=document.getElementById('chatForm'),chatIn=document.getElementById('chatIn'),sendBtn=document.getElementById('sendBtn');
function add(cls,text){const d=document.createElement('div');d.className='msg '+cls;d.textContent=text;log.appendChild(d);log.scrollTop=log.scrollHeight;return d;}
async function api(path,body){
  const r=await fetch(path,{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(body)});
  const j=await r.json().catch(()=>({}));
  if(!r.ok)throw new Error(j.error||('HTTP '+r.status));
  return j;
}
document.getElementById('nameForm').addEventListener('submit',async e=>{
  e.preventDefault();
  const name=document.getElementById('nameIn').value.trim();if(!name)return;
  document.getElementById('nameCard').style.display='none';
  log.style.display='flex';chatForm.style.display='flex';
  const w=add('sys','connecting…');
  try{const j=await api('/api/chat/'+deck+'/session',{name});sid=j.sid;w.remove();add('v',j.reply);chatIn.focus();}
  catch(err){w.textContent='could not connect: '+err.message;}
});
chatForm.addEventListener('submit',async e=>{
  e.preventDefault();
  const text=chatIn.value.trim();if(!text||!sid)return;
  add('u',text);chatIn.value='';sendBtn.disabled=true;
  const w=add('sys','…');
  try{const j=await api('/api/chat/'+deck+'/session/'+sid,{text});w.remove();add('v',j.reply);}
  catch(err){w.textContent='send failed: '+err.message+' — try again';}
  sendBtn.disabled=false;chatIn.focus();
});
</script></body></html>`
