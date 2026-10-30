package oai

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestPostJSONReportsUsageWithoutPayloadOrResponseContent(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"usage":{"input_tokens":80,"output_tokens":20,"total_tokens":100,"input_tokens_details":{"cached_tokens":25}},"output_text":"private"}`))
	}))
	defer upstream.Close()

	client := &Client{BaseURL: upstream.URL, Key: "secret", HTTP: upstream.Client()}
	var got Observation
	client.Observe = func(observation Observation) { got = observation }
	if _, code, err := client.PostJSON("/responses", map[string]any{"model": "gpt-test", "input": "private prompt"}); err != nil || code != http.StatusOK {
		t.Fatalf("PostJSON code=%d err=%v", code, err)
	}
	if got.Path != "/responses" || got.Model != "gpt-test" || got.InputTokens != 80 || got.OutputTokens != 20 || got.CachedInputTokens != 25 || got.TotalTokens != 100 {
		t.Fatalf("observation = %#v", got)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestGenerateAssetAllowsSlowImageWithoutExtendingOtherRequests(t *testing.T) {
	client := New("https://images.example", "")
	client.Key = "fixture"
	client.HTTP.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		deadline, ok := r.Context().Deadline()
		if !ok || time.Until(deadline) < 11*time.Minute {
			t.Errorf("image request deadline too short: %v", deadline)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"data":[{"b64_json":"aW1hZ2U="}]}`)), Header: make(http.Header)}, nil
	})
	if _, err := client.GenerateAsset(t.TempDir(), "fixture", "cover", "", "1024x1024", "cover", nil, false); err != nil {
		t.Fatal(err)
	}
	if client.HTTP.Timeout != 180*time.Second {
		t.Fatal("changed non-image timeout")
	}
}

type incompleteBody struct{}

func (incompleteBody) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }
func (incompleteBody) Close() error             { return nil }
func TestPostJSONReportsIncompleteResponse(t *testing.T) {
	client := &Client{BaseURL: "https://images.example", HTTP: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: incompleteBody{}, Header: make(http.Header)}, nil
	})}}
	if _, code, err := client.PostJSON("/responses", map[string]any{}); code != 200 || err != io.ErrUnexpectedEOF {
		t.Fatalf("code=%d err=%v", code, err)
	}
}
