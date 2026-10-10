// Package avatar manages optional LiveAvatar renderers. It contains no tenant,
// subscription or presentation policy; callers supply an authenticated owner.
package avatar

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"
)

// Alessandra in Black Suit, portrait green-screen studio look.
const DefaultAvatar = "de82b2cc-7314-40a1-b41f-cc456c66601c"

type Provider struct {
	BaseURL, Key string
	Client       *http.Client
}
type Room struct {
	WSURL string `json:"ws_url"`
	URL   string `json:"livekit_url"`
	Token string `json:"livekit_client_token"`
}

func (p Provider) request(route string, body any, token string, out any) error {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	data, _ := json.Marshal(body)
	base := p.BaseURL
	if base == "" {
		base = "https://api.liveavatar.com/v1"
	}
	req, err := http.NewRequestWithContext(ctx, "POST", base+route, bytes.NewReader(data))
	if err != nil {
		return errors.New("invalid avatar provider")
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	} else {
		req.Header.Set("X-API-KEY", p.Key)
	}
	client := p.Client
	if client == nil {
		client = &http.Client{CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	}
	res, err := client.Do(req)
	if err != nil {
		return errors.New("avatar provider unavailable")
	}
	defer res.Body.Close()
	if route == "/sessions/stop" && (res.StatusCode == http.StatusNotFound || res.StatusCode == http.StatusGone) {
		return nil
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return fmt.Errorf("avatar provider %s failed (HTTP %d)", route, res.StatusCode)
	}
	if out == nil {
		return nil
	}
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&envelope) != nil || json.Unmarshal(envelope.Data, out) != nil {
		return errors.New("invalid avatar provider response")
	}
	return nil
}
func (p Provider) start(allocated func(string)) (Room, error) {
	if p.Key == "" {
		return Room{}, errors.New("LiveAvatar is not configured")
	}
	var token struct {
		ID    string `json:"session_id"`
		Token string `json:"session_token"`
	}
	err := p.request("/sessions/token", map[string]any{"mode": "LITE", "avatar_id": DefaultAvatar, "max_session_duration": 120, "video_settings": map[string]string{"quality": "high", "encoding": "H264"}}, "", &token)
	// Preserve an allocated ID even if the token response was incomplete.
	if token.ID != "" {
		allocated(token.ID)
	}
	if err != nil {
		return Room{}, err
	}
	if token.ID == "" || token.Token == "" {
		return Room{}, errors.New("incomplete avatar token")
	}
	var room Room
	if err = p.request("/sessions/start", map[string]any{}, token.Token, &room); err != nil {
		return Room{}, err
	}
	for _, raw := range []string{room.WSURL, room.URL} {
		u, e := url.Parse(raw)
		if e != nil || u.Scheme != "wss" || u.Host == "" || u.User != nil {
			return Room{}, errors.New("invalid avatar media connection")
		}
	}
	if room.Token == "" {
		return Room{}, errors.New("missing avatar room token")
	}
	return room, nil
}

type session struct {
	cleanupDeadline     time.Time
	owner, providerID   string
	cancelled, starting bool
	timer               *time.Timer
}
type Manager struct {
	mu       sync.Mutex
	provider Provider
	lease    time.Duration
	sessions map[string]*session
}

func New(provider Provider, lease time.Duration) *Manager {
	return &Manager{provider: provider, lease: lease, sessions: map[string]*session{}}
}
func (m *Manager) Start(owner, id string) (Room, error) {
	m.mu.Lock()
	if _, ok := m.sessions[id]; ok {
		m.mu.Unlock()
		return Room{}, errors.New("avatar request already used")
	}
	// Bound memory and concurrent billable sessions, including startup.
	active := 0
	for _, s := range m.sessions {
		if !s.cancelled {
			active++
		}
	}
	if active >= 4 || len(m.sessions) >= 1000 {
		m.mu.Unlock()
		return Room{}, errors.New("avatar session limit reached")
	}
	s := &session{owner: owner, starting: true, cleanupDeadline: time.Now().Add(3 * time.Minute)}
	m.sessions[id] = s
	s.timer = time.AfterFunc(m.lease, func() { _ = m.Stop(owner, id) })
	m.mu.Unlock()
	room, err := m.provider.start(func(providerID string) { m.mu.Lock(); s.providerID = providerID; m.mu.Unlock() })
	m.mu.Lock()
	s.starting = false
	cancelled := s.cancelled
	m.mu.Unlock()
	if err != nil || cancelled {
		_ = m.Stop(owner, id)
		if err != nil {
			return Room{}, err
		}
		return Room{}, errors.New("avatar request cancelled")
	}
	return room, nil
}
func (m *Manager) KeepAlive(owner, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.sessions[id]
	if s == nil || s.owner != owner || s.cancelled {
		return errors.New("avatar session unavailable")
	}
	s.timer.Reset(m.lease)
	return nil
}
func (m *Manager) Stop(owner, id string) error {
	m.mu.Lock()
	s := m.sessions[id]
	if s == nil {
		// Tombstone handles a sleep arriving before its start request.
		s = &session{owner: owner, cancelled: true}
		m.sessions[id] = s
		time.AfterFunc(3*time.Minute, func() {
			m.mu.Lock()
			if m.sessions[id] == s {
				delete(m.sessions, id)
			}
			m.mu.Unlock()
		})
		m.mu.Unlock()
		return nil
	}
	if s.owner != owner {
		m.mu.Unlock()
		return errors.New("avatar session unavailable")
	}
	s.cancelled = true
	if s.timer != nil {
		s.timer.Stop()
	}
	providerID := s.providerID
	if s.starting {
		m.mu.Unlock()
		return nil
	}
	s.providerID = ""
	m.mu.Unlock()
	if providerID != "" {
		var err error
		for attempt := 0; attempt < 3; attempt++ {
			err = m.provider.request("/sessions/stop", map[string]string{"session_id": providerID}, "", nil)
			if err == nil {
				break
			}
		}
		if err != nil {
			m.mu.Lock()
			s.providerID = providerID
			m.mu.Unlock()
			if time.Now().Before(s.cleanupDeadline) {
				time.AfterFunc(5*time.Second, func() { _ = m.Stop(owner, id) })
			} else {
				m.mu.Lock()
				delete(m.sessions, id)
				m.mu.Unlock()
			}
			return err
		}
	}
	time.AfterFunc(3*time.Minute, func() {
		m.mu.Lock()
		if m.sessions[id] == s {
			delete(m.sessions, id)
		}
		m.mu.Unlock()
	})
	return nil
}
