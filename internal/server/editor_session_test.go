package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestEditorSessionBoundary(t *testing.T) {
	st := testStudio(t)
	token := strings.Repeat("a", 64)
	h, err := NewEditorSession(st, EditorSessionOptions{Deck: "demo", Token: token, ExpiresAt: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	request := func(method, path, body, credential string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		if credential != "" {
			r.Header.Set("Authorization", "Bearer "+credential)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	for _, path := range []string{"/d/demo/", "/api/me", "/api/editor/snapshot"} {
		if w := request("GET", path, "", ""); w.Code != 401 {
			t.Fatalf("anonymous %s: %d", path, w.Code)
		}
	}
	for _, path := range []string{"/api/app/team", "/api/deck/foreign/slide/a", "/auth/login", "/api/realtime/token", "/api/deck/demo/share", "/site/../studio.yaml"} {
		if w := request("GET", path, "", token); w.Code != 404 {
			t.Fatalf("excluded %s: %d", path, w.Code)
		}
	}
	var me struct {
		Editable     bool `json:"editable"`
		StartEditing bool `json:"start_editing"`
	}
	w := request("GET", "/api/me", "", token)
	if err := json.Unmarshal(w.Body.Bytes(), &me); err != nil || !me.Editable || !me.StartEditing {
		t.Fatalf("editor bootstrap: %s", w.Body.String())
	}
	w = request("PUT", "/api/deck/demo/slide/0010-a/fragment", `<section class="slide"><h1>Cloud edit</h1></section>`, token)
	if w.Code != http.StatusOK {
		t.Fatalf("edit %d: %s", w.Code, w.Body.String())
	}
	if err := os.MkdirAll(filepath.Join(st.Root, ".vstd"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(st.Root, ".vstd", "credentials"), []byte("never-export"), 0600); err != nil {
		t.Fatal(err)
	}
	w = request("GET", "/api/editor/snapshot", "", token)
	if w.Code != 200 || strings.Contains(w.Body.String(), "never-export") {
		t.Fatalf("snapshot %d", w.Code)
	}
	var snapshot struct {
		Protocol int    `json:"protocol"`
		Digest   string `json:"digest"`
		Files    []struct {
			Path    string `json:"path"`
			Content []byte `json:"content"`
		} `json:"files"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &snapshot); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, f := range snapshot.Files {
		if f.Path == "decks/demo/slides/0010-a.html" && strings.Contains(string(f.Content), "Cloud edit") {
			found = true
		}
	}
	if snapshot.Protocol != 1 || len(snapshot.Digest) != 64 || !found {
		t.Fatal("snapshot must contain real edited canonical files")
	}
}
func TestEditorSessionFailsClosed(t *testing.T) {
	st := testStudio(t)
	for _, options := range []EditorSessionOptions{
		{Deck: "../demo", Token: strings.Repeat("a", 64), ExpiresAt: time.Now().Add(time.Minute)},
		{Deck: "demo", Token: "short", ExpiresAt: time.Now().Add(time.Minute)},
		{Deck: "demo", Token: strings.Repeat("a", 64), ExpiresAt: time.Now().Add(-time.Minute)},
	} {
		if _, err := NewEditorSession(st, options); err == nil {
			t.Fatal("invalid session accepted")
		}
	}
}
