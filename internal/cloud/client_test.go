package cloud

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func testClient(t *testing.T, handler http.Handler, opts ...Option) *Client {
	t.Helper()
	s := httptest.NewServer(handler)
	t.Cleanup(s.Close)
	base := []Option{WithEndpoint(s.URL), WithHTTPClient(s.Client()), WithClientVersion("1.2.0")}
	c, err := NewClient(append(base, opts...)...)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestCapabilityFailurePreventsMutation(t *testing.T) {
	var mutations atomic.Int32
	c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/capabilities" {
			writeJSON(w, `{"protocol":"1","minimum_client_version":"9.0.0","capabilities":["workspace.sync"]}`)
			return
		}
		mutations.Add(1)
		w.WriteHeader(http.StatusCreated)
	}))
	_, err := c.Sync(context.Background(), "ws", SyncRequest{BaseRevisionID: "r1"})
	var incompatible *IncompatibleError
	if !errors.As(err, &incompatible) || incompatible.MinimumClientVersion != "9.0.0" {
		t.Fatalf("error = %#v", err)
	}
	if mutations.Load() != 0 {
		t.Fatalf("mutation count = %d", mutations.Load())
	}
}

func TestClientMutationIsNotReplayed(t *testing.T) {
	var calls atomic.Int32
	c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/capabilities" {
			writeJSON(w, `{"protocol":"1","capabilities":["workspace.sync"]}`)
			return
		}
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":"try later"}`))
	}))
	_, err := c.Sync(context.Background(), "ws", SyncRequest{BaseRevisionID: "r1"})
	if err == nil || calls.Load() != 1 {
		t.Fatalf("err=%v calls=%d", err, calls.Load())
	}
}

func TestClientResponseLimitAndSecretSafeErrors(t *testing.T) {
	secret := "very-secret-token"
	c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(strings.Repeat(secret, 100)))
	}), WithTokenSource(TokenSourceFunc(func(context.Context) (string, error) { return secret, nil })), WithResponseLimit(64))
	_, err := c.Account(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("secret leaked: %v", err)
	}
}

func TestProtocolTypedConflict(t *testing.T) {
	c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/capabilities" {
			writeJSON(w, `{"protocol":"1","capabilities":["workspace.sync"]}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		writeJSON(w, `{"code":"stale_base","message":"base is stale","cloud_head_revision_id":"r2"}`)
	}))
	_, err := c.Sync(context.Background(), "ws", SyncRequest{BaseRevisionID: "r1"})
	var conflict *ConflictError
	if !errors.As(err, &conflict) || conflict.CloudHeadRevisionID != "r2" {
		t.Fatalf("error = %#v", err)
	}
}

func writeJSON(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(body))
}

func TestEndpointRejectsCredentialsAndRoutingSuffixes(t *testing.T) {
	for _, endpoint := range []string{"https://user:secret@example.com", "https://example.com?key=secret", "https://example.com#fragment"} {
		if _, err := NewClient(WithEndpoint(endpoint), WithClientVersion("1.0.0")); err == nil {
			t.Fatalf("accepted unsafe endpoint %s", endpoint)
		}
	}
}

func TestRemoteErrorCodeCannotEchoSecret(t *testing.T) {
	c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(401)
		writeJSON(w, `{"code":"sentinel-secret"}`)
	}))
	_, err := c.Account(context.Background())
	if err == nil || strings.Contains(err.Error(), "sentinel-secret") {
		t.Fatalf("unsafe error: %v", err)
	}
}

func TestWorkspaceReadRequiresNegotiation(t *testing.T) {
	var reads atomic.Int32
	c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/capabilities" {
			writeJSON(w, `{"protocol":"2","capabilities":["workspace.read"]}`)
			return
		}
		reads.Add(1)
		writeJSON(w, `{}`)
	}))
	_, err := c.Revision(context.Background(), "ws", "r1")
	if err == nil || reads.Load() != 0 {
		t.Fatalf("read from incompatible service: %v", err)
	}
}
