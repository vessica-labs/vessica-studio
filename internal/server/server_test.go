package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vessica-labs/vessica-studio/internal/studio"
)

func testStudio(t *testing.T) *studio.Studio {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"studio.yaml":                   "theme_default: default\n",
		"decks/demo/deck.yaml":          "title: Demo\ntheme: default\n",
		"decks/demo/slides/0010-a.html": `<section class="slide"><div class="s-title">Before</div></section>` + "\n",
		"decks/demo/slides/0010-a.md":   "# Before\n\n## Edit requests\n\n## Log\n",
		"themes/default/theme.css":      ".slide{}\n",
		"library/manifest.json":         "{\"version\":1}\n",
		"requests/.gitkeep":             "",
	}
	for name, body := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	st, err := studio.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func presenterRequest(s *Server, method, target, body string) *http.Request {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.AddCookie(&http.Cookie{
		Name:  sessionCookie,
		Value: s.sessionValue("matt-kropp", time.Now().Add(time.Hour)),
	})
	return req
}

func TestPublicEditsRequireAuthenticatedPresenterAndReadySync(t *testing.T) {
	st := testStudio(t)
	s := New(st, ModePublic)
	s.secret = "test-secret"
	s.allowed = map[string]bool{"matt-kropp": true}
	s.ContentSync = &ContentSync{ready: true, state: syncStateReady}
	h := s.Routes()
	target := "/api/deck/demo/slide/0010-a/fragment"
	body := `<section class="slide"><div class="s-title">After</div></section>`

	for _, tc := range []struct {
		name string
		req  *http.Request
		want int
	}{
		{name: "anonymous", req: httptest.NewRequest(http.MethodPut, target, strings.NewReader(body)), want: http.StatusForbidden},
		{name: "presenter", req: presenterRequest(s, http.MethodPut, target, body), want: http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, tc.req)
			if rr.Code != tc.want {
				t.Fatalf("status = %d, want %d; body=%s", rr.Code, tc.want, rr.Body.String())
			}
		})
	}

	got, err := os.ReadFile(st.SlidePath("demo", "0010-a", ".html"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "After") {
		t.Fatalf("presenter edit was not written: %s", got)
	}
	if !s.ContentSync.dirty {
		t.Fatal("successful hosted edit did not queue content sync")
	}

	note := "- 2026-08-12 (sticky note): simplify the curve"
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, presenterRequest(s, http.MethodPut,
		"/api/deck/demo/slide/0010-a/companion/Edit%20requests", note))
	if rr.Code != http.StatusOK {
		t.Fatalf("presenter sticky status = %d; body=%s", rr.Code, rr.Body.String())
	}
	companion, err := os.ReadFile(st.SlidePath("demo", "0010-a", ".md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(companion), "simplify the curve") {
		t.Fatalf("presenter sticky was not written: %s", companion)
	}

	s.ContentSync.mu.Lock()
	s.ContentSync.ready = false
	s.ContentSync.mu.Unlock()
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, presenterRequest(s, http.MethodPut, target, body))
	if rr.Code != http.StatusForbidden {
		t.Fatalf("edit with unavailable sync status = %d, want 403", rr.Code)
	}
}

func TestContentSyncConfigurationFailsClosedWithoutCredential(t *testing.T) {
	t.Setenv("VSTD_CONTENT_SYNC", "1")
	t.Setenv("VSTD_GIT_REPO", "vessica-labs/example")
	t.Setenv("VSTD_GIT_TOKEN", "")
	s := New(testStudio(t), ModePublic)
	if err := s.StartContentSync(); err == nil || !strings.Contains(err.Error(), "VSTD_GIT_TOKEN") {
		t.Fatalf("StartContentSync error = %v, want missing token error", err)
	}
	if s.ContentSync != nil {
		t.Fatal("failed content sync unexpectedly enabled hosted editing")
	}
}

func TestMeReportsHostedEditCapability(t *testing.T) {
	s := New(testStudio(t), ModePublic)
	s.secret = "test-secret"
	s.allowed = map[string]bool{"matt-kropp": true}
	s.ContentSync = &ContentSync{ready: true, state: syncStateReady}

	for _, tc := range []struct {
		name     string
		req      *http.Request
		editable string
	}{
		{name: "anonymous", req: httptest.NewRequest(http.MethodGet, "/api/me", nil), editable: `"editable":false`},
		{name: "presenter", req: presenterRequest(s, http.MethodGet, "/api/me", ""), editable: `"editable":true`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			s.Routes().ServeHTTP(rr, tc.req)
			body, _ := io.ReadAll(rr.Result().Body)
			if !strings.Contains(string(body), tc.editable) {
				t.Fatalf("response %s does not contain %s", body, tc.editable)
			}
		})
	}
}
