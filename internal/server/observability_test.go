package server

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCatalogMonitoringLinkIsOwnerOnlyAndAboveDocumentation(t *testing.T) {
	owner := httptest.NewRecorder()
	(&Server{}).renderCatalogPage(owner, "Owner", "owner", "csrf", true)
	body := owner.Body.String()
	monitoring := strings.Index(body, `href="/observability"`)
	documentation := strings.Index(body, `>Documentation</span>`)
	if monitoring < 0 || documentation < 0 || monitoring > documentation {
		t.Fatalf("owner monitoring link must appear above Documentation: monitoring=%d documentation=%d", monitoring, documentation)
	}

	member := httptest.NewRecorder()
	(&Server{}).renderCatalogPage(member, "Member", "member", "csrf", true)
	if strings.Contains(member.Body.String(), `href="/observability"`) {
		t.Fatal("member catalog exposes the owner-only monitoring link")
	}
}

func TestObservabilityPageHasOwnerDashboardViewsAndResponsiveDesign(t *testing.T) {
	for _, want := range []string{
		"Owner workspace", "Audience viewers", "Reliability", "OpenAI usage", "Codex tokens",
		"API requests and Codex runs", "All tracked Railway model usage",
		"codexRuns===1?'run':'runs'",
		`data-tab="viewers"`, `data-tab="team"`, `data-tab="reliability"`, `data-tab="ai"`,
		"Viewer identities come from QR chat names", "@media(max-width:760px)", "Fraunces", "bootstrap-icons",
	} {
		if !strings.Contains(observabilityPageHTML, want) {
			t.Fatalf("monitoring page missing %q", want)
		}
	}
	for _, unwanted := range []string{"Codex usage','Excluded", "Intentionally not collected", "OpenAI API only"} {
		if strings.Contains(observabilityPageHTML, unwanted) {
			t.Fatalf("monitoring page still excludes Railway Codex usage with %q", unwanted)
		}
	}
}

func TestObservabilityPathRedactsShareTokens(t *testing.T) {
	if got := observabilityPath("/v/demo/super-secret-token"); got != "/v/demo/[redacted]" {
		t.Fatalf("redacted path = %q", got)
	}
	if got := observabilityPath("/api/deck/demo/export.pdf"); got != "/api/deck/demo/export.pdf" {
		t.Fatalf("ordinary path changed = %q", got)
	}
}

func TestUsageFromRealtimeExtractsTokenDetails(t *testing.T) {
	raw := json.RawMessage(`{"input_tokens":120,"output_tokens":30,"total_tokens":150,"input_token_details":{"cached_tokens":45}}`)
	usage := usageFromRealtime(raw)
	if usage.InputTokens != 120 || usage.OutputTokens != 30 || usage.TotalTokens != 150 || usage.CachedInputTokens != 45 {
		t.Fatalf("usage = %#v", usage)
	}
}
