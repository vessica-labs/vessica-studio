package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vessica-labs/vessica-studio/internal/collab"
)

func TestCollaborationStartupFailsClosedBeforeListening(t *testing.T) {
	t.Setenv("VSTD_COLLABORATION", "1")
	if err := New(testStudio(t), ModeStudio).StartCollaboration(context.Background()); err == nil || !strings.Contains(err.Error(), "public") {
		t.Fatalf("studio-mode collaboration error=%v", err)
	}

	s := New(testStudio(t), ModePublic)
	t.Setenv("VSTD_APP_ORIGIN", "https://same.example.test")
	t.Setenv("VSTD_PLAYER_ORIGIN", "https://same.example.test")
	if err := s.StartCollaboration(context.Background()); err == nil || !strings.Contains(err.Error(), "separate") {
		t.Fatalf("same-origin collaboration error=%v", err)
	}
}

func collaborationHostServer(t *testing.T) (*Server, http.Handler) {
	t.Helper()
	s := New(testStudio(t), ModePublic)
	s.Collab = &collab.Store{}
	s.appOrigin = "https://app.example.test"
	s.playerOrigin = "https://present.example.test"
	s.ContentSync = &ContentSync{ready: true, state: syncStateReady}
	return s, s.Routes()
}

func hostRequest(method, target, host string) *http.Request {
	r := httptest.NewRequest(method, target, nil)
	r.Host = host
	return r
}

func TestCollaborationHostDispatchSeparatesAppAndPlayer(t *testing.T) {
	_, h := collaborationHostServer(t)
	for _, tc := range []struct {
		name, host, path string
		want             int
	}{
		{"health on app", "app.example.test", "/healthz", http.StatusOK},
		{"health on player", "present.example.test", "/healthz", http.StatusOK},
		{"deck absent on app", "app.example.test", "/d/demo/", http.StatusNotFound},
		{"player identity absent on app", "app.example.test", "/api/me", http.StatusNotFound},
		{"catalog absent on player", "present.example.test", "/presentations", http.StatusNotFound},
		{"login absent on player", "present.example.test", "/auth/login", http.StatusNotFound},
		{"app API absent on player", "present.example.test", "/api/app/decks", http.StatusNotFound},
		{"unknown host rejected", "elsewhere.example.test", "/presentations", http.StatusMisdirectedRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, hostRequest(http.MethodGet, tc.path, tc.host))
			if rr.Code != tc.want {
				t.Fatalf("status=%d, want %d; body=%s", rr.Code, tc.want, rr.Body.String())
			}
		})
	}
}

func TestAccountCookieIsNotPlayerAuthorization(t *testing.T) {
	_, h := collaborationHostServer(t)
	req := hostRequest(http.MethodGet, "/api/me", "present.example.test")
	req.AddCookie(&http.Cookie{Name: accountSessionCookie, Value: "account-secret"})
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"presenter":false`) {
		t.Fatalf("account cookie became player authority: status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestLoopbackRendererBypassesExternalHostDispatchOnly(t *testing.T) {
	_, h := collaborationHostServer(t)
	req := hostRequest(http.MethodGet, "/api/me", "127.0.0.1:4400")
	req.RemoteAddr = "127.0.0.1:54321"
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"presenter":false`) {
		t.Fatalf("loopback renderer path did not reach authorization handler: status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestLegacyAudiencePathRedirectsToPlayerOrigin(t *testing.T) {
	_, h := collaborationHostServer(t)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, hostRequest(http.MethodGet, "/v/demo/secret?follow=1", "app.example.test"))
	if rr.Code != http.StatusFound || rr.Header().Get("Location") != "https://present.example.test/v/demo/secret?follow=1" {
		t.Fatalf("redirect=%d %q", rr.Code, rr.Header().Get("Location"))
	}
}

func TestOriginAndEventDeckValidation(t *testing.T) {
	for _, tc := range []struct {
		origin string
		mode   Mode
		ok     bool
	}{
		{"https://app.example.test", ModePublic, true},
		{"http://app.example.test", ModePublic, false},
		{"https://app.example.test/path", ModePublic, false},
		{"javascript:alert(1)", ModePublic, false},
		{"http://localhost:9000", ModeStudio, true},
		{"http://localhost:9000", ModePublic, true},
	} {
		if got := validateOrigin(tc.origin, tc.mode) == nil; got != tc.ok {
			t.Errorf("validateOrigin(%q) ok=%v, want %v", tc.origin, got, tc.ok)
		}
	}

	for _, tc := range []struct {
		event string
		want  bool
	}{
		{"reload", true},
		{"follow|demo|3", true},
		{"follow|other|3", false},
		{"vdisplay|demo", true},
		{"vdisplay|other", false},
		{`vpulse|{"deck":"demo","total":2}`, true},
		{`vpulse|{"deck":"other","total":2}`, false},
	} {
		if got := eventVisibleToDeck(tc.event, "demo"); got != tc.want {
			t.Errorf("eventVisibleToDeck(%q)=%v, want %v", tc.event, got, tc.want)
		}
	}
}
