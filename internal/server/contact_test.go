package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPublicRootServesOptionalLandingPage(t *testing.T) {
	st := testStudio(t)
	if err := os.MkdirAll(filepath.Join(st.Root, "site"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(st.Root, "site", "index.html"), []byte("<!doctype html><title>Matt Kropp</title>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(st.Root, "site", "site.css"), []byte("body{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := New(st, ModePublic)
	s.secret = "test-secret"
	s.allowed = map[string]bool{"matt-kropp": true}
	h := s.Routes()

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", nil))
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "Matt Kropp") {
		t.Fatalf("anonymous root status=%d body=%q", rr.Code, rr.Body.String())
	}

	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/site/site.css", nil))
	if rr.Code != http.StatusOK || rr.Body.String() != "body{}" {
		t.Fatalf("site asset status=%d body=%q", rr.Code, rr.Body.String())
	}

	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, presenterRequest(s, http.MethodGet, "/", ""))
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "Vessica Studio") {
		t.Fatalf("presenter root status=%d body=%q", rr.Code, rr.Body.String())
	}
}

func TestPublicRootWithoutLandingRetainsLoginRedirect(t *testing.T) {
	s := New(testStudio(t), ModePublic)
	rr := httptest.NewRecorder()
	s.Routes().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", nil))
	if rr.Code != http.StatusFound || rr.Header().Get("Location") != "/auth/login" {
		t.Fatalf("status=%d location=%q", rr.Code, rr.Header().Get("Location"))
	}
}

func TestContactFormSendsThroughConfiguredResendAccount(t *testing.T) {
	t.Setenv("RESEND_API_KEY", "test-key")
	t.Setenv("RESEND_FROM", "Matt Kropp <contact@example.com>")
	t.Setenv("VSTD_CONTACT_TO", "matt@example.com")

	var got map[string]any
	resend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("authorization = %q", r.Header.Get("Authorization"))
		}
		if r.Header.Get("User-Agent") != "Vessica-Studio/1.0" {
			t.Errorf("user agent = %q", r.Header.Get("User-Agent"))
		}
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &got); err != nil {
			t.Fatal(err)
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"email-1"}`))
	}))
	defer resend.Close()
	originalURL := resendSendURL
	resendSendURL = resend.URL
	defer func() { resendSendURL = originalURL }()

	s := New(testStudio(t), ModePublic)
	body := `{"name":"Jamie Rivera","email":"jamie@example.com","organization":"Acme","inquiry_type":"Keynote or conference","message":"We are planning an executive AI summit in October.","website":""}`
	rr := httptest.NewRecorder()
	s.Routes().ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/contact", strings.NewReader(body)))
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%q", rr.Code, rr.Body.String())
	}
	if got["reply_to"] != "jamie@example.com" || got["subject"] != "Speaking inquiry from Jamie Rivera" {
		t.Fatalf("payload=%#v", got)
	}
}

func TestContactFormRejectsInvalidAndSilentlyDropsHoneypot(t *testing.T) {
	t.Setenv("RESEND_API_KEY", "test-key")
	t.Setenv("RESEND_FROM", "contact@example.com")
	t.Setenv("VSTD_CONTACT_TO", "matt@example.com")
	s := New(testStudio(t), ModePublic)
	h := s.Routes()

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/contact", strings.NewReader(`{"name":"J","email":"bad","inquiry_type":"Other","message":"short"}`)))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("invalid status=%d body=%q", rr.Code, rr.Body.String())
	}

	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/contact", strings.NewReader(`{"website":"spam.example"}`)))
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"sent"`) {
		t.Fatalf("honeypot status=%d body=%q", rr.Code, rr.Body.String())
	}
}
