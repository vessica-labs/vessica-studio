package oai

import (
	"net/http"
	"net/http/httptest"
	"testing"
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
