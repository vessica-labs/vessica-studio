package server

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestLocalPresenterRelaysPositionToHostedServer(t *testing.T) {
	const secret = "shared-follow-secret"
	hosted := New(testStudio(t), ModePublic)
	hosted.secret = secret
	hostedHTTP := httptest.NewServer(hosted.Routes())
	defer hostedHTTP.Close()

	localStudio := testStudio(t)
	localStudio.Config.PublicHost = hostedHTTP.URL
	local := New(localStudio, ModeStudio)
	local.secret = secret
	rr := httptest.NewRecorder()
	local.Routes().ServeHTTP(rr, httptest.NewRequest(http.MethodPost,
		"/api/deck/demo/presenting", strings.NewReader(`{"index":0}`)))
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"status":"ok"`) {
		t.Fatalf("local presenting status=%d body=%s", rr.Code, rr.Body.String())
	}
	state, ok := hosted.presentingFor("demo")
	if !ok || state.Index != 0 || state.Seq == 0 {
		t.Fatalf("hosted position = %#v, %v", state, ok)
	}
}

func TestLoopbackPublicPreviewAlsoRelaysPosition(t *testing.T) {
	const secret = "shared-follow-secret"
	hosted := New(testStudio(t), ModePublic)
	hosted.secret = secret
	hostedHTTP := httptest.NewServer(hosted.Routes())
	defer hostedHTTP.Close()

	previewStudio := testStudio(t)
	previewStudio.Config.PublicHost = hostedHTTP.URL
	preview := New(previewStudio, ModePublic)
	preview.secret = secret
	preview.allowed = map[string]bool{"matt-kropp": true}
	req := presenterRequest(preview, http.MethodPost, "/api/deck/demo/presenting", `{"index":0}`)
	req.RemoteAddr = "127.0.0.1:54321"
	rr := httptest.NewRecorder()
	preview.Routes().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"status":"ok"`) {
		t.Fatalf("preview presenting status=%d body=%s", rr.Code, rr.Body.String())
	}
	if state, ok := hosted.presentingFor("demo"); !ok || state.Index != 0 {
		t.Fatalf("hosted position = %#v, %v", state, ok)
	}
}

func TestPresentingRelayRejectsUnsignedAndStaleUpdates(t *testing.T) {
	s := New(testStudio(t), ModePublic)
	s.secret = "shared-follow-secret"
	h := s.Routes()
	for _, tc := range []struct {
		name string
		seq  int64
		sig  string
	}{
		{name: "unsigned", seq: time.Now().UnixNano()},
		{name: "stale", seq: time.Now().Add(-3 * time.Minute).UnixNano(), sig: "signed-below"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := fmt.Sprintf(`{"index":0,"seq":%d}`, tc.seq)
			req := httptest.NewRequest(http.MethodPost, "/api/deck/demo/presenting-relay", strings.NewReader(body))
			if tc.sig != "" {
				req.Header.Set("X-VSTD-Presenting-Signature", s.sign(fmt.Sprintf("presenting|demo|0|%d", tc.seq)))
			}
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, req)
			if rr.Code != http.StatusUnauthorized {
				t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
			}
		})
	}
}

func TestPresentingStateRejectsOutOfOrderDelivery(t *testing.T) {
	s := New(testStudio(t), ModeStudio)
	now := time.Now().UnixNano()
	if !s.setPresenting("demo", 0, now) {
		t.Fatal("initial position was not accepted")
	}
	if s.setPresenting("demo", 7, now-1) {
		t.Fatal("older position unexpectedly replaced current state")
	}
	state, _ := s.presentingFor("demo")
	if state.Index != 0 || state.Seq != now {
		t.Fatalf("position after out-of-order update = %#v", state)
	}
}

type followRecorder struct {
	header http.Header
	body   bytes.Buffer
	cancel context.CancelFunc
}

func (r *followRecorder) Header() http.Header { return r.header }
func (r *followRecorder) WriteHeader(int)     {}
func (r *followRecorder) Flush()              {}
func (r *followRecorder) Write(p []byte) (int, error) {
	n, err := r.body.Write(p)
	if bytes.Contains(r.body.Bytes(), []byte("event: follow")) {
		r.cancel()
	}
	return n, err
}

func TestDeckEventStreamReplaysCurrentPosition(t *testing.T) {
	s := New(testStudio(t), ModeStudio)
	s.setPresenting("demo", 0, time.Now().UnixNano())
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodGet, "/api/events?deck=demo", nil).WithContext(ctx)
	rr := &followRecorder{header: http.Header{}, cancel: cancel}
	s.handleEvents(rr, req)
	if got := rr.body.String(); !strings.Contains(got, "event: follow\ndata: demo|0") {
		t.Fatalf("event stream did not replay position:\n%s", got)
	}
}

func TestPublicDeckEventStreamRequiresDeckAccess(t *testing.T) {
	s := New(testStudio(t), ModePublic)
	s.secret = "shared-follow-secret"
	rr := httptest.NewRecorder()
	s.Routes().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/events?deck=demo", nil))
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status=%d, want 403; body=%s", rr.Code, rr.Body.String())
	}
}
