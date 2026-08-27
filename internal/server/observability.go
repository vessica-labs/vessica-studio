package server

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"regexp"
	"strings"
	"sync/atomic"
	"time"

	"github.com/vessica-labs/vessica-studio/internal/collab"
	"github.com/vessica-labs/vessica-studio/internal/oai"
	"github.com/vessica-labs/vessica-studio/internal/studio"
)

const visitorCookie = "vstd_visitor"

var visitorIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{20,80}$`)

type observabilityRecorder struct {
	store   *collab.Store
	queue   chan collab.ObservabilityEvent
	dropped atomic.Uint64
}

func newObservabilityRecorder(store *collab.Store) *observabilityRecorder {
	r := &observabilityRecorder{store: store, queue: make(chan collab.ObservabilityEvent, 2048)}
	go r.loop()
	return r
}

func (r *observabilityRecorder) enqueue(event collab.ObservabilityEvent) {
	if event.OccurredAt.IsZero() {
		event.OccurredAt = time.Now().UTC()
	}
	select {
	case r.queue <- event:
	default:
		r.dropped.Add(1)
	}
}

func (r *observabilityRecorder) loop() {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	batch := make([]collab.ObservabilityEvent, 0, 64)
	flush := func() {
		if len(batch) == 0 {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err := r.store.RecordObservability(ctx, batch)
		cancel()
		if err != nil {
			r.dropped.Add(uint64(len(batch)))
			log.Printf("observability: background write failed: %v", err)
		}
		batch = batch[:0]
	}
	for {
		select {
		case event := <-r.queue:
			batch = append(batch, event)
			if len(batch) >= cap(batch) {
				flush()
			}
		case <-ticker.C:
			flush()
		}
	}
}

func (s *Server) recordObservability(event collab.ObservabilityEvent) {
	if s.Collab == nil {
		return
	}
	s.mu.Lock()
	if s.observability == nil {
		s.observability = newObservabilityRecorder(s.Collab)
	}
	recorder := s.observability
	s.mu.Unlock()
	recorder.enqueue(event)
}

func (s *Server) observeOpenAI(observation oai.Observation) {
	total := observation.TotalTokens
	if total == 0 {
		total = observation.InputTokens + observation.OutputTokens
	}
	source := strings.Trim(strings.ReplaceAll(observation.Path, "/", "."), ".")
	if source == "" {
		source = "api"
	}
	s.recordObservability(collab.ObservabilityEvent{
		Kind: collab.EventOpenAIUsage, Source: source, Model: observation.Model, StatusCode: observation.StatusCode,
		InputTokens: observation.InputTokens, OutputTokens: observation.OutputTokens,
		CachedInputTokens: observation.CachedInputTokens, TotalTokens: total, DurationMS: observation.DurationMS,
	})
}

func (s *Server) observabilityDropped() uint64 {
	s.mu.Lock()
	recorder := s.observability
	s.mu.Unlock()
	if recorder == nil {
		return 0
	}
	return recorder.dropped.Load()
}

func (s *Server) visitorID(w http.ResponseWriter, r *http.Request, create bool) string {
	if cookie, err := r.Cookie(visitorCookie); err == nil && visitorIDPattern.MatchString(cookie.Value) {
		return cookie.Value
	}
	if !create {
		return ""
	}
	id := randToken(24)
	http.SetCookie(w, &http.Cookie{Name: visitorCookie, Value: id, Path: "/", HttpOnly: true,
		Secure: r.TLS != nil || s.Mode == ModePublic, SameSite: http.SameSiteLaxMode,
		MaxAge: 180 * 24 * 60 * 60, Expires: time.Now().Add(180 * 24 * time.Hour)})
	return id
}

func (s *Server) recordQRScan(w http.ResponseWriter, r *http.Request, deck, source string) {
	if s.Collab == nil {
		return
	}
	s.recordObservability(collab.ObservabilityEvent{Kind: collab.EventAudienceQRScan,
		VisitorID: s.visitorID(w, r, true), DeckStorageKey: deck, Source: source})
}

func (s *Server) handleObservabilityView(w http.ResponseWriter, r *http.Request) {
	if s.Collab == nil {
		http.NotFound(w, r)
		return
	}
	var request struct {
		Deck  string `json:"deck"`
		Slide string `json:"slide"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<12)).Decode(&request); err != nil || !studio.ValidDeckName(request.Deck) || (request.Slide != "" && !studio.ValidSlideID(request.Slide)) {
		jsonErr(w, fmt.Errorf("invalid view event"), http.StatusBadRequest)
		return
	}
	if ps, ok := s.playerSessionForDeck(r, request.Deck); ok && s.Collab.Can(r.Context(), ps.User.ID, ps.Deck, "view") {
		kind := collab.EventTeamDeckView
		if request.Slide != "" {
			kind = collab.EventTeamSlideView
		}
		s.recordObservability(collab.ObservabilityEvent{Kind: kind, ActorUserID: ps.User.ID,
			DeckID: ps.Deck.ID, DeckStorageKey: request.Deck, SlideID: request.Slide, Source: ps.Mode})
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if !s.hasShare(r, request.Deck) {
		jsonErr(w, fmt.Errorf("audience access required"), http.StatusForbidden)
		return
	}
	kind := collab.EventAudienceDeckView
	if request.Slide != "" {
		kind = collab.EventAudienceSlideView
	}
	s.recordObservability(collab.ObservabilityEvent{Kind: kind, VisitorID: s.visitorID(w, r, true),
		DeckStorageKey: request.Deck, SlideID: request.Slide, Source: audienceSource(r)})
	w.WriteHeader(http.StatusNoContent)
}

func audienceSource(r *http.Request) string {
	if strings.HasPrefix(r.Referer(), "/chat/") || strings.Contains(r.Referer(), "/chat/") {
		return "chat_qr"
	}
	if r.URL.Query().Get("follow") == "1" || strings.Contains(r.Referer(), "follow=1") {
		return "follow_qr"
	}
	return "share_qr"
}

type reportedUsage struct {
	Deck              string `json:"deck"`
	Source            string `json:"source"`
	Model             string `json:"model"`
	ResponseID        string `json:"response_id"`
	InputTokens       int64  `json:"input_tokens"`
	OutputTokens      int64  `json:"output_tokens"`
	CachedInputTokens int64  `json:"cached_input_tokens"`
	TotalTokens       int64  `json:"total_tokens"`
}

func (s *Server) handleReportedOpenAIUsage(w http.ResponseWriter, r *http.Request) {
	if s.Collab == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	var usage reportedUsage
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<14)).Decode(&usage); err != nil || !studio.ValidDeckName(usage.Deck) {
		jsonErr(w, fmt.Errorf("invalid usage event"), http.StatusBadRequest)
		return
	}
	ps, ok := s.playerSessionForDeck(r, usage.Deck)
	if !ok || (ps.Mode != "present" && ps.Mode != "edit") {
		jsonErr(w, fmt.Errorf("presenter access required"), http.StatusForbidden)
		return
	}
	if len(usage.Model) > 100 || len(usage.Source) > 40 || len(usage.ResponseID) > 160 || usage.InputTokens < 0 || usage.OutputTokens < 0 || usage.CachedInputTokens < 0 || usage.TotalTokens < 0 || usage.TotalTokens > 1_000_000_000 {
		jsonErr(w, fmt.Errorf("invalid usage event"), http.StatusBadRequest)
		return
	}
	if usage.TotalTokens == 0 {
		usage.TotalTokens = usage.InputTokens + usage.OutputTokens
	}
	if usage.Source != "dictation" {
		usage.Source = "realtime"
	}
	dedupe := ""
	if usage.ResponseID != "" {
		dedupe = "realtime:" + ps.User.ID + ":" + usage.ResponseID
	}
	s.recordObservability(collab.ObservabilityEvent{Kind: collab.EventOpenAIUsage,
		ActorUserID: ps.User.ID, DeckID: ps.Deck.ID, DeckStorageKey: usage.Deck, Source: usage.Source,
		Model: usage.Model, InputTokens: usage.InputTokens, OutputTokens: usage.OutputTokens,
		CachedInputTokens: usage.CachedInputTokens, TotalTokens: usage.TotalTokens, DedupeKey: dedupe})
	w.WriteHeader(http.StatusAccepted)
}

func usageFromRealtime(raw json.RawMessage) reportedUsage {
	var usage struct {
		InputTokens       int64 `json:"input_tokens"`
		OutputTokens      int64 `json:"output_tokens"`
		TotalTokens       int64 `json:"total_tokens"`
		InputTokenDetails struct {
			CachedTokens int64 `json:"cached_tokens"`
		} `json:"input_token_details"`
		InputTokensDetails struct {
			CachedTokens int64 `json:"cached_tokens"`
		} `json:"input_tokens_details"`
	}
	_ = json.Unmarshal(raw, &usage)
	cached := usage.InputTokenDetails.CachedTokens
	if usage.InputTokensDetails.CachedTokens > cached {
		cached = usage.InputTokensDetails.CachedTokens
	}
	return reportedUsage{InputTokens: usage.InputTokens, OutputTokens: usage.OutputTokens,
		CachedInputTokens: cached, TotalTokens: usage.TotalTokens}
}

func (s *Server) recordRealtimeUsage(raw json.RawMessage, deck, source, model, actorID, dedupe string) {
	usage := usageFromRealtime(raw)
	if usage.TotalTokens == 0 {
		usage.TotalTokens = usage.InputTokens + usage.OutputTokens
	}
	if usage.TotalTokens == 0 && usage.InputTokens == 0 && usage.OutputTokens == 0 {
		return
	}
	s.recordObservability(collab.ObservabilityEvent{Kind: collab.EventOpenAIUsage, ActorUserID: actorID,
		DeckStorageKey: deck, Source: source, Model: model, InputTokens: usage.InputTokens,
		OutputTokens: usage.OutputTokens, CachedInputTokens: usage.CachedInputTokens,
		TotalTokens: usage.TotalTokens, DedupeKey: dedupe})
}

func (s *Server) handleObservabilityPage(w http.ResponseWriter, r *http.Request) {
	if s.Collab == nil {
		http.NotFound(w, r)
		return
	}
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
	io.WriteString(w, observabilityPageHTML)
}

func (s *Server) handleObservabilityData(w http.ResponseWriter, r *http.Request) {
	if s.Collab == nil {
		http.NotFound(w, r)
		return
	}
	sess, ok := s.requireAccount(w, r, false, true)
	if !ok {
		return
	}
	days := 30
	_, _ = fmt.Sscanf(r.URL.Query().Get("days"), "%d", &days)
	dashboard, err := s.Collab.ObservabilityDashboard(r.Context(), sess.User.ID, days, s.observabilityDropped())
	if err != nil {
		jsonErr(w, err, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, dashboard)
}

type observabilityResponseWriter struct {
	http.ResponseWriter
	status int
}

func (w *observabilityResponseWriter) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *observabilityResponseWriter) Write(body []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(body)
}

func (w *observabilityResponseWriter) Flush() {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	_ = http.NewResponseController(w.ResponseWriter).Flush()
}

func (w *observabilityResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return http.NewResponseController(w.ResponseWriter).Hijack()
}

func (w *observabilityResponseWriter) Push(target string, options *http.PushOptions) error {
	if pusher, ok := w.ResponseWriter.(http.Pusher); ok {
		return pusher.Push(target, options)
	}
	return http.ErrNotSupported
}

func (w *observabilityResponseWriter) ReadFrom(reader io.Reader) (int64, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	if rf, ok := w.ResponseWriter.(io.ReaderFrom); ok {
		return rf.ReadFrom(reader)
	}
	return io.Copy(struct{ io.Writer }{w.ResponseWriter}, reader)
}

func (s *Server) observeHTTP(next http.Handler) http.Handler {
	if s.Collab == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capture := &observabilityResponseWriter{ResponseWriter: w}
		defer func() {
			if recovered := recover(); recovered != nil {
				s.recordHTTPError(r, http.StatusInternalServerError)
				panic(recovered)
			}
		}()
		next.ServeHTTP(capture, r)
		if capture.status < http.StatusInternalServerError {
			return
		}
		s.recordHTTPError(r, capture.status)
	})
}

func (s *Server) recordHTTPError(r *http.Request, status int) {
	event := collab.ObservabilityEvent{Kind: collab.EventServerError, Method: r.Method,
		Path: observabilityPath(r.URL.Path), StatusCode: status, DeckStorageKey: r.PathValue("deck")}
	if cookie, err := r.Cookie(visitorCookie); err == nil && visitorIDPattern.MatchString(cookie.Value) {
		event.VisitorID = cookie.Value
	}
	s.recordObservability(event)
}

func observabilityPath(path string) string {
	if strings.HasPrefix(path, "/v/") {
		parts := strings.Split(path, "/")
		if len(parts) >= 4 {
			return "/v/" + parts[2] + "/[redacted]"
		}
	}
	if len(path) > 240 {
		return path[:240]
	}
	return path
}
