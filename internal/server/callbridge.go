// Phase 2 of the Vessica demo tools: real two-way AI phone calls.
//
// The stage session calls call_person -> POST /api/vessica/{deck}/call. The
// engine dials out via Telnyx Call Control with bidirectional media
// streaming pointed back at wss://<PUBLIC_URL>/api/telnyx/media. When the
// callee answers, Telnyx opens that WebSocket and streams G.711 mu-law
// (PCMU, 8 kHz) frames; the engine opens a DEDICATED OpenAI realtime session
// configured for audio/pcmu on both directions and bridges base64 payloads
// with no transcoding. The phone persona gets its own instructions (who
// she's calling, on whose behalf, the goal, KB digest) and a finish_call
// tool; on finish_call (or hangup) the engine hangs up, writes the
// transcript to _vessica/<deck>/calls/, appends the outcome to the inbox,
// and broadcasts a "vcall" SSE event that the player pushes into the
// on-stage session as a [CALL REPORT].
//
// Env: TELNYX_API_KEY, TELNYX_FROM_NUMBER, TELNYX_CONNECTION_ID (a Call
// Control application id), PUBLIC_URL (https://... — used to derive the
// wss:// media URL). Calls are capped at 2 concurrent / 6 minutes each.
package server

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	maxConcurrentCalls = 2
	maxCallDuration    = 6 * time.Minute
)

type callSession struct {
	CallControlID string
	Deck          string
	ToName        string
	ToNumber      string
	Goal          string

	mu         sync.Mutex
	telnyx     *websocket.Conn // write-guarded by mu
	oai        *websocket.Conn // write-guarded by oaiMu
	oaiMu      sync.Mutex
	transcript []string
	summary    string
	done       chan struct{}
	closed     bool
	started    time.Time
}

func (cs *callSession) log(line string) {
	cs.mu.Lock()
	cs.transcript = append(cs.transcript, line)
	cs.mu.Unlock()
}

// calls maps call_control_id -> session (registered at dial time).
var (
	callsMu sync.Mutex
	calls   = map[string]*callSession{}
)

func activeCalls() int {
	callsMu.Lock()
	defer callsMu.Unlock()
	return len(calls)
}

// ---- dial ----

func (s *Server) handleCall(w http.ResponseWriter, r *http.Request) {
	key, from, conn := os.Getenv("TELNYX_API_KEY"), os.Getenv("TELNYX_FROM_NUMBER"), os.Getenv("TELNYX_CONNECTION_ID")
	pub := strings.TrimRight(os.Getenv("PUBLIC_URL"), "/")
	if key == "" || from == "" || conn == "" || pub == "" {
		jsonErr(w, fmt.Errorf("calling not configured (need TELNYX_API_KEY, TELNYX_FROM_NUMBER, TELNYX_CONNECTION_ID, PUBLIC_URL)"), 501)
		return
	}
	if !s.OAI.HasKey() {
		jsonErr(w, fmt.Errorf("calling not configured (no OpenAI key for the phone session)"), 501)
		return
	}
	if activeCalls() >= maxConcurrentCalls {
		jsonErr(w, fmt.Errorf("call limit: %d calls already in progress", maxConcurrentCalls), 429)
		return
	}
	var req struct{ To, ToName, Goal string }
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonErr(w, err, 400)
		return
	}
	req.To = normalizeE164(req.To)
	if req.To == "" {
		jsonErr(w, fmt.Errorf("to is not a valid phone number — use E.164 like +15551234567 (US formats like (415) 555-2671 are auto-normalized)"), 400)
		return
	}
	if strings.TrimSpace(req.Goal) == "" {
		jsonErr(w, fmt.Errorf("need goal — what should the call accomplish?"), 400)
		return
	}
	deck := r.PathValue("deck")
	wssURL := "wss" + strings.TrimPrefix(strings.TrimPrefix(pub, "https"), "http") + "/api/telnyx/media"
	payload := map[string]any{
		"connection_id":              conn,
		"to":                         req.To,
		"from":                       from,
		"timeout_secs":               30,
		"stream_url":                 wssURL,
		"stream_track":               "inbound_track",
		"stream_bidirectional_mode":  "rtp",
		"stream_bidirectional_codec": "PCMU",
		"client_state":               base64.StdEncoding.EncodeToString([]byte(deck)),
	}
	body, code, err := postJSON(telnyxBase()+"/v2/calls", key, payload)
	if err != nil {
		jsonErr(w, err, 502)
		return
	}
	if code >= 300 {
		jsonErr(w, fmt.Errorf("telnyx dial: %s", trim(body, 400)), 502)
		return
	}
	var res struct {
		Data struct {
			CallControlID string `json:"call_control_id"`
		} `json:"data"`
	}
	if json.Unmarshal(body, &res) != nil || res.Data.CallControlID == "" {
		jsonErr(w, fmt.Errorf("telnyx dial: no call_control_id in response"), 502)
		return
	}
	cs := &callSession{CallControlID: res.Data.CallControlID, Deck: deck,
		ToName: req.ToName, ToNumber: req.To, Goal: req.Goal,
		done: make(chan struct{}), started: time.Now()}
	callsMu.Lock()
	calls[cs.CallControlID] = cs
	callsMu.Unlock()
	s.rememberSent(deck, req.To, req.ToName)
	log.Printf("call: dialing %s (%s) — goal: %s", req.ToName, req.To, trim([]byte(req.Goal), 120))
	// hard stop: duration cap + orphan cleanup
	go func() {
		select {
		case <-cs.done:
		case <-time.After(maxCallDuration):
			log.Printf("call %s: duration cap reached — hanging up", cs.CallControlID)
			s.hangup(cs, "duration cap reached")
		}
	}()
	writeJSON(w, map[string]string{"status": "dialing", "to": req.To,
		"note": "the outcome will arrive as a [CALL REPORT] event when the call ends"})
}

func (s *Server) hangup(cs *callSession, why string) {
	key := os.Getenv("TELNYX_API_KEY")
	postJSON(telnyxBase()+"/v2/calls/"+cs.CallControlID+"/actions/hangup", key, map[string]any{})
	s.endCall(cs, why)
}

// endCall finalizes exactly once: closes sockets, persists transcript,
// notifies the stage session.
func (s *Server) endCall(cs *callSession, why string) {
	cs.mu.Lock()
	if cs.closed {
		cs.mu.Unlock()
		return
	}
	cs.closed = true
	tx := append([]string(nil), cs.transcript...)
	summary := cs.summary
	cs.mu.Unlock()
	close(cs.done)
	callsMu.Lock()
	delete(calls, cs.CallControlID)
	callsMu.Unlock()
	if cs.telnyx != nil {
		cs.telnyx.Close()
	}
	if cs.oai != nil {
		cs.oai.Close()
	}
	name := cs.ToName
	if name == "" {
		name = cs.ToNumber
	}
	if summary == "" {
		summary = "call ended (" + why + ") without a wrap-up"
		if n := len(tx); n > 0 {
			last := tx
			if n > 6 {
				last = tx[n-6:]
			}
			summary += "; last exchanges:\n" + strings.Join(last, "\n")
		} else {
			summary += " — likely no answer or voicemail"
		}
	}
	// durable transcript
	ts := time.Now().Format("0102-1504")
	body := "# Call with " + name + " (" + cs.ToNumber + ") — " + ts + "\nGoal: " + cs.Goal +
		"\nOutcome: " + summary + "\n\n## Transcript\n" + strings.Join(tx, "\n") + "\n"
	os.WriteFile(s.vesDir(cs.Deck, "calls", ts+"-"+strings.ReplaceAll(name, " ", "-")+".md"), []byte(body), 0o600)
	s.appendInbox(cs.Deck, inboxMsg{Dir: "in", Channel: "call", With: name,
		Number: cs.ToNumber, Text: "CALL REPORT — " + summary, Time: time.Now().Format(time.RFC3339)})
	ev, _ := json.Marshal(map[string]string{"deck": cs.Deck, "from": name,
		"number": cs.ToNumber, "summary": summary})
	s.Broadcast("vcall|" + string(ev))
	log.Printf("call %s ended (%s): %s", cs.CallControlID, why, trim([]byte(summary), 200))
}

// ---- Telnyx call-event webhooks (extends the messaging webhook) ----

// handleCallEvent is invoked from handleTelnyxWebhook for call.* events.
func (s *Server) handleCallEvent(eventType, callControlID string) {
	callsMu.Lock()
	cs := calls[callControlID]
	callsMu.Unlock()
	if cs == nil {
		return
	}
	switch eventType {
	case "call.answered":
		cs.log("(answered)")
		log.Printf("call %s: answered", callControlID)
	case "call.hangup":
		s.endCall(cs, "callee hung up")
	case "call.machine.detection.ended":
		// left disabled at dial; ignore
	}
}

// ---- media bridge: Telnyx WSS <-> OpenAI realtime WSS ----

var wsUpgrader = websocket.Upgrader{
	ReadBufferSize: 4096, WriteBufferSize: 4096,
	CheckOrigin: func(r *http.Request) bool { return true }, // Telnyx, not a browser
}

func (s *Server) handleTelnyxMedia(w http.ResponseWriter, r *http.Request) {
	if os.Getenv("TELNYX_API_KEY") == "" {
		http.Error(w, "not configured", 501)
		return
	}
	tc, err := wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	var cs *callSession
	defer func() {
		if cs != nil {
			s.endCall(cs, "media stream closed")
		} else {
			tc.Close()
		}
	}()
	// Telnyx sends "connected" then "start" (carrying call_control_id).
	for {
		_, msg, err := tc.ReadMessage()
		if err != nil {
			return
		}
		var f struct {
			Event string `json:"event"`
			Start struct {
				CallControlID string `json:"call_control_id"`
			} `json:"start"`
			Media struct {
				Track   string `json:"track"`
				Payload string `json:"payload"`
			} `json:"media"`
		}
		if json.Unmarshal(msg, &f) != nil {
			continue
		}
		switch f.Event {
		case "start":
			callsMu.Lock()
			cs = calls[f.Start.CallControlID]
			callsMu.Unlock()
			if cs == nil {
				log.Printf("media: unknown call %s — closing", f.Start.CallControlID)
				return
			}
			cs.mu.Lock()
			cs.telnyx = tc
			cs.mu.Unlock()
			if err := s.startPhoneSession(cs); err != nil {
				log.Printf("call %s: OpenAI session failed: %v", cs.CallControlID, err)
				s.hangup(cs, "AI session failed: "+err.Error())
				return
			}
		case "media":
			if cs == nil || f.Media.Track == "outbound" {
				continue
			}
			cs.oaiMu.Lock()
			if cs.oai != nil {
				cs.oai.WriteJSON(map[string]string{"type": "input_audio_buffer.append", "audio": f.Media.Payload})
			}
			cs.oaiMu.Unlock()
		case "stop":
			return
		}
	}
}

// startPhoneSession opens the dedicated realtime session for one call and
// starts the pump from OpenAI back to the phone.
func (s *Server) startPhoneSession(cs *callSession) error {
	base := s.St.Config.OpenAI.BaseURL // e.g. https://api.openai.com/v1
	wsBase := strings.Replace(strings.Replace(base, "https://", "wss://", 1), "http://", "ws://", 1)
	wsBase = strings.TrimSuffix(wsBase, "/")
	if !strings.HasSuffix(wsBase, "/v1") {
		wsBase += "/v1"
	}
	model := s.St.Config.OpenAI.RealtimeModel
	if model == "" {
		model = "gpt-realtime-2"
	}
	hdr := http.Header{"Authorization": {"Bearer " + s.OAI.Key}}
	oc, _, err := websocket.DefaultDialer.Dial(wsBase+"/realtime?model="+model, hdr)
	if err != nil {
		return err
	}
	cs.oaiMu.Lock()
	cs.oai = oc
	cs.oaiMu.Unlock()

	sendOAI := func(v any) {
		cs.oaiMu.Lock()
		defer cs.oaiMu.Unlock()
		if cs.oai != nil {
			cs.oai.WriteJSON(v)
		}
	}
	sendTelnyx := func(v any) {
		cs.mu.Lock()
		defer cs.mu.Unlock()
		if cs.telnyx != nil {
			cs.telnyx.WriteJSON(v)
		}
	}

	kbDigest := s.kbDigestFor(cs.Deck, 6*1024)
	name := cs.ToName
	if name == "" {
		name = "the person answering"
	}
	instr := `You are Vessica, Matt Kropp's AI presentation copilot, ON A REAL PHONE CALL that you placed on Matt's behalf during a live presentation. You are speaking with ` + name + `.
YOUR GOAL FOR THIS CALL: ` + cs.Goal + `
CONDUCT: open by introducing yourself in one short sentence ("Hi — this is Vessica, Matt Kropp's AI copilot calling from his presentation") and confirm you have the right person. Be warm, brisk and natural — this is a quick call, not an interview. Ask one thing at a time, listen, follow up once if useful. Keep the whole call under three minutes. If you reach voicemail or the wrong person, leave one graceful sentence and call finish_call. When you have what you need (or the person wants to go), thank them, say Matt will see their input on screen, and call finish_call with a crisp summary of what they said.
KNOWLEDGE BASE (context on the company and audience):
` + kbDigest
	sendOAI(map[string]any{"type": "session.update", "session": map[string]any{
		"type":         "realtime",
		"instructions": instr,
		"audio": map[string]any{
			"input": map[string]any{
				"format":         map[string]any{"type": "audio/pcmu"},
				"turn_detection": map[string]any{"type": "semantic_vad"},
				"transcription":  map[string]any{"model": "whisper-1"},
			},
			"output": map[string]any{"format": map[string]any{"type": "audio/pcmu"}},
		},
		"tools": []map[string]any{{
			"type": "function", "name": "finish_call",
			"description": "Call when the conversation is complete (or unreachable/voicemail). summary = 2-4 sentences: who you reached, what they said relative to the goal, anything notable. The call hangs up after this.",
			"parameters": map[string]any{"type": "object",
				"properties": map[string]any{"summary": map[string]any{"type": "string"}},
				"required":   []string{"summary"}},
		}},
		"tool_choice": "auto",
	}})
	// speak first once the callee is on the line
	sendOAI(map[string]any{"type": "response.create"})

	go func() {
		defer s.endCall(cs, "AI session closed")
		for {
			_, msg, err := oc.ReadMessage()
			if err != nil {
				return
			}
			var ev struct {
				Type       string `json:"type"`
				Delta      string `json:"delta"`
				Transcript string `json:"transcript"`
				Name       string `json:"name"`
				Arguments  string `json:"arguments"`
				CallID     string `json:"call_id"`
				Error      any    `json:"error"`
			}
			if json.Unmarshal(msg, &ev) != nil {
				continue
			}
			switch ev.Type {
			case "response.output_audio.delta":
				sendTelnyx(map[string]any{"event": "media", "media": map[string]string{"payload": ev.Delta}})
			case "input_audio_buffer.speech_started":
				// barge-in: flush queued phone audio + cancel the response
				sendTelnyx(map[string]string{"event": "clear"})
				sendOAI(map[string]string{"type": "response.cancel"})
			case "conversation.item.input_audio_transcription.completed":
				if t := strings.TrimSpace(ev.Transcript); t != "" {
					cs.log(name + ": " + t)
				}
			case "response.output_audio_transcript.done":
				if t := strings.TrimSpace(ev.Transcript); t != "" {
					cs.log("Vessica: " + t)
				}
			case "response.function_call_arguments.done":
				if ev.Name == "finish_call" {
					var a struct{ Summary string }
					json.Unmarshal([]byte(ev.Arguments), &a)
					cs.mu.Lock()
					cs.summary = a.Summary
					cs.mu.Unlock()
					sendOAI(map[string]any{"type": "conversation.item.create",
						"item": map[string]any{"type": "function_call_output",
							"call_id": ev.CallID, "output": "noted — hanging up"}})
					// give the goodbye audio a moment to flush, then hang up
					go func() {
						time.Sleep(2500 * time.Millisecond)
						s.hangup(cs, "finished")
					}()
				}
			case "error":
				b, _ := json.Marshal(ev.Error)
				log.Printf("call %s: realtime error: %s", cs.CallControlID, trim(b, 300))
			}
		}
	}()
	return nil
}

// kbDigestFor renders the same digest the stage session gets, capped to n bytes.
func (s *Server) kbDigestFor(deck string, n int) string {
	dir := s.St.Root + "/_vessica/" + deck + "/kb"
	ents, err := os.ReadDir(dir)
	if err != nil {
		return "(no knowledge base uploaded)"
	}
	var b strings.Builder
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		c, err := os.ReadFile(dir + "/" + e.Name())
		if err != nil {
			continue
		}
		if b.Len() > n {
			break
		}
		b.WriteString("\n===== " + e.Name() + " =====\n")
		b.Write(c)
	}
	out := b.String()
	if len(out) > n {
		out = out[:n] + "\n…(truncated)"
	}
	if out == "" {
		return "(no knowledge base uploaded)"
	}
	return out
}

// telnyxBase allows overriding the Telnyx API host in tests.
func telnyxBase() string {
	if b := os.Getenv("TELNYX_API_BASE"); b != "" {
		return strings.TrimRight(b, "/")
	}
	return "https://api.telnyx.com"
}
