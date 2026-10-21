package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vessica-labs/vessica-studio/internal/oai"
)

func TestDeckBriefPersistenceClearAndStaleWrites(t *testing.T) {
	st := testStudio(t)
	s := New(st, ModeStudio)
	calls := 0
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var input map[string]any
		json.NewDecoder(r.Body).Decode(&input)
		if input["store"] != false {
			t.Error("provider must not store conversation")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"output":[{"type":"message","content":[{"type":"output_text","text":"# Meeting brief\n\nDiscuss the Acme pilot."}]}]}`))
	}))
	defer provider.Close()
	s.OAI = &oai.Client{BaseURL: provider.URL, Key: "test", HTTP: provider.Client()}
	request := func(method, body string) *httptest.ResponseRecorder {
		r := httptest.NewRecorder()
		s.Routes().ServeHTTP(r, httptest.NewRequest(method, "/api/vessica/demo/brief", strings.NewReader(body)))
		return r
	}
	first := request("GET", "")
	if first.Code != 200 {
		t.Fatalf("GET: %d %s", first.Code, first.Body)
	}
	var state map[string]any
	json.Unmarshal(first.Body.Bytes(), &state)
	state["text"] = "Let's discuss an Acme pilot"
	payload, _ := json.Marshal(state)
	saved := request("POST", string(payload))
	if saved.Code != 200 {
		t.Fatalf("POST: %d %s", saved.Code, saved.Body)
	}
	if !strings.Contains(saved.Body.String(), "Acme pilot") {
		t.Fatal(saved.Body)
	}
	// A new server reads the same durable brief, but another deck starts empty.
	again := New(st, ModeStudio)
	rr := httptest.NewRecorder()
	again.Routes().ServeHTTP(rr, httptest.NewRequest("GET", "/api/vessica/demo/brief", nil))
	if !strings.Contains(rr.Body.String(), "Acme pilot") {
		t.Fatal(rr.Body)
	}
	foreign := httptest.NewRecorder()
	again.Routes().ServeHTTP(foreign, httptest.NewRequest("GET", "/api/vessica/other/brief", nil))
	if foreign.Code != 404 {
		t.Fatalf("missing deck: %d", foreign.Code)
	}
	cleared := request("DELETE", "")
	if cleared.Code != 200 {
		t.Fatal(cleared.Body)
	}
	stale := request("POST", string(payload))
	if stale.Code != 409 || calls != 1 {
		t.Fatalf("stale: %d calls=%d", stale.Code, calls)
	}
	if strings.Contains(request("GET", "").Body.String(), "Acme") {
		t.Fatal("clear restored old context")
	}
	if first.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("brief must not cache")
	}
	filepath.WalkDir(filepath.Join(st.Root, "_vessica"), func(path string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			b, _ := os.ReadFile(path)
			if strings.Contains(string(b), "Let's discuss") {
				t.Error("transcript persisted")
			}
		}
		return err
	})
}

func TestBriefRequiresPresenterAndRejectsSymlinks(t *testing.T) {
	s := New(testStudio(t), ModePublic)
	s.secret = "test"
	s.allowed = map[string]bool{"matt-kropp": true}
	for _, method := range []string{"GET", "POST", "DELETE"} {
		r := httptest.NewRecorder()
		s.Routes().ServeHTTP(r, httptest.NewRequest(method, "/api/vessica/demo/brief", strings.NewReader(`{}`)))
		if r.Code != 401 {
			t.Fatalf("%s: %d", method, r.Code)
		}
	}
	outside := t.TempDir()
	os.Symlink(outside, filepath.Join(s.St.Root, "_vessica"))
	r := httptest.NewRecorder()
	s.Routes().ServeHTTP(r, presenterRequest(s, "GET", "/api/vessica/demo/brief", ""))
	if r.Code < 400 {
		t.Fatal("symlink accepted")
	}
}

func TestClearBriefDiscardsProviderResultInFlight(t *testing.T) {
	s := New(testStudio(t), ModeStudio)
	started, release := make(chan struct{}), make(chan struct{})
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-release
		w.Write([]byte(`{"output":[{"type":"message","content":[{"type":"output_text","text":"OLD CONTEXT"}]}]}`))
	}))
	defer provider.Close()
	s.OAI = &oai.Client{BaseURL: provider.URL, Key: "test", HTTP: provider.Client()}
	h := s.Routes()
	first := httptest.NewRecorder()
	h.ServeHTTP(first, httptest.NewRequest("GET", "/api/vessica/demo/brief", nil))
	var state map[string]any
	json.Unmarshal(first.Body.Bytes(), &state)
	state["text"] = "old context"
	payload, _ := json.Marshal(state)
	result := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, httptest.NewRequest("POST", "/api/vessica/demo/brief", strings.NewReader(string(payload))))
		result <- rr
	}()
	<-started
	cleared := httptest.NewRecorder()
	h.ServeHTTP(cleared, httptest.NewRequest("DELETE", "/api/vessica/demo/brief", nil))
	if cleared.Code != 200 {
		t.Fatal(cleared.Body)
	}
	close(release)
	if rr := <-result; rr.Code != 409 {
		t.Fatalf("stale provider result: %d %s", rr.Code, rr.Body)
	}
	final := httptest.NewRecorder()
	h.ServeHTTP(final, httptest.NewRequest("GET", "/api/vessica/demo/brief", nil))
	if strings.Contains(final.Body.String(), "OLD CONTEXT") {
		t.Fatal(final.Body)
	}
}
