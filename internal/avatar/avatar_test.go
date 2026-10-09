package avatar

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestSessionLifecycleAndCancelDuringStart(t *testing.T) {
	for _, cancelEarly := range []bool{false, true} {
		t.Run(map[bool]string{false: "normal", true: "cancel-start"}[cancelEarly], func(t *testing.T) {
			entered, release, stopped := make(chan struct{}), make(chan struct{}), make(chan struct{}, 1)
			var mu sync.Mutex
			var tokenBody map[string]any
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("X-API-KEY") != "private-key" && r.Header.Get("Authorization") != "Bearer scoped-token" {
					t.Error("missing provider auth")
				}
				switch r.URL.Path {
				case "/sessions/token":
					json.NewDecoder(r.Body).Decode(&tokenBody)
					json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"session_id": "provider-id", "session_token": "scoped-token"}})
				case "/sessions/start":
					close(entered)
					<-release
					json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"ws_url": "wss://media.example/ws", "livekit_url": "wss://room.example", "livekit_client_token": "room-token"}})
				case "/sessions/stop":
					mu.Lock()
					stopped <- struct{}{}
					mu.Unlock()
					w.Write([]byte(`{"data":{}}`))
				default:
					t.Errorf("unexpected route %s", r.URL.Path)
				}
			}))
			defer api.Close()
			manager := New(Provider{BaseURL: api.URL, Key: "private-key"}, time.Second)
			done := make(chan error, 1)
			go func() { _, err := manager.Start("owner", "browser-id"); done <- err }()
			<-entered
			if cancelEarly {
				if err := manager.Stop("owner", "browser-id"); err != nil {
					t.Fatal(err)
				}
			}
			close(release)
			err := <-done
			if cancelEarly && err == nil {
				t.Fatal("cancelled start exposed credentials")
			}
			if !cancelEarly {
				if err != nil {
					t.Fatal(err)
				}
				if err := manager.Stop("other", "browser-id"); err == nil {
					t.Fatal("cross-owner stop accepted")
				}
				if err := manager.Stop("owner", "browser-id"); err != nil {
					t.Fatal(err)
				}
			}
			select {
			case <-stopped:
			case <-time.After(time.Second):
				t.Fatal("provider not stopped")
			}
			if tokenBody["mode"] != "LITE" || tokenBody["avatar_id"] != DefaultAvatar {
				t.Fatal(tokenBody)
			}
			if err := manager.KeepAlive("owner", "browser-id"); err == nil {
				t.Fatal("sleep resurrected session")
			}
		})
	}
}

func TestLeaseExpiryAndInvalidStartCleanup(t *testing.T) {
	for _, invalid := range []bool{false, true} {
		t.Run(map[bool]string{false: "expiry", true: "invalid-start"}[invalid], func(t *testing.T) {
			stopped := make(chan struct{}, 1)
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/sessions/token":
					w.Write([]byte(`{"data":{"session_id":"p","session_token":"s"}}`))
				case "/sessions/start":
					if invalid {
						w.WriteHeader(500)
						w.Write([]byte("private-key must not leak"))
					} else {
						w.Write([]byte(`{"data":{"ws_url":"wss://media.example","livekit_url":"wss://room.example","livekit_client_token":"r"}}`))
					}
				case "/sessions/stop":
					stopped <- struct{}{}
					w.Write([]byte(`{"data":{}}`))
				}
			}))
			defer api.Close()
			manager := New(Provider{BaseURL: api.URL, Key: "private-key"}, 30*time.Millisecond)
			_, err := manager.Start("owner", "id")
			if invalid && err == nil {
				t.Fatal("expected failure")
			}
			select {
			case <-stopped:
			case <-time.After(time.Second):
				t.Fatal("leaked paid session")
			}
		})
	}
}

func TestStopBeforeStartAndDuplicateStart(t *testing.T) {
	m := New(Provider{}, time.Second)
	if err := m.Stop("owner", "id"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Start("owner", "id"); err == nil {
		t.Fatal("late start revived sleep")
	}
	if _, err := m.Start("owner", "new-id"); err == nil {
		t.Fatal("missing credential accepted")
	}
}

func TestStopAlreadyExpiredProvider(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNotFound) }))
	defer api.Close()
	p := Provider{BaseURL: api.URL, Key: "test"}
	if err := p.request("/sessions/stop", map[string]string{"session_id": "expired"}, "", nil); err != nil {
		t.Fatal(err)
	}
}
