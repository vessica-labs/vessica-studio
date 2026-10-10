package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAvatarRoutesRequirePresenterAndSameOrigin(t *testing.T) {
	t.Setenv("LIVEAVATAR_API_KEY", "")
	s := New(testStudio(t), ModePublic)
	s.secret = "secret"
	s.allowed = map[string]bool{"matt-kropp": true}
	for _, action := range []string{"start", "stop", "keepalive"} {
		path := "/api/avatar/" + action
		rr := httptest.NewRecorder()
		s.Routes().ServeHTTP(rr, httptest.NewRequest("POST", path, strings.NewReader(`{"id":"12345678-1234-1234-1234-123456789012"}`)))
		if rr.Code != http.StatusUnauthorized {
			t.Fatalf("%s anonymous: %d", action, rr.Code)
		}
		req := presenterRequest(s, "POST", path, `{"id":"12345678-1234-1234-1234-123456789012"}`)
		req.Header.Set("Origin", "https://attacker.example")
		rr = httptest.NewRecorder()
		s.Routes().ServeHTTP(rr, req)
		if rr.Code != http.StatusForbidden {
			t.Fatalf("%s cross-origin: %d", action, rr.Code)
		}
	}
	rr := httptest.NewRecorder()
	s.Routes().ServeHTTP(rr, presenterRequest(s, "POST", "/api/avatar/start", `{"id":"12345678-1234-1234-1234-123456789012"}`))
	if rr.Code != 503 || strings.Contains(rr.Body.String(), "private-key") {
		t.Fatalf("missing config: %d %s", rr.Code, rr.Body.String())
	}
}
