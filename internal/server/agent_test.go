package server

import (
	"context"
	"encoding/json"
	"github.com/vessica-labs/vessica-studio/internal/studio"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestAgentRecoversInterruptedPassForRetry(t *testing.T) {
	st := testStudio(t)
	path := st.SlidePath("demo", "0010-a", ".md")
	body := "# Before\n\n## Edit requests\n- (in progress — cloud agent — 40%)\n- add the share QR code\n\n## Log\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	w := &agentWorker{s: New(st, ModeStudio)}
	if got := w.recoverInterruptedPasses(); got != 1 {
		t.Fatalf("recovered = %d, want 1", got)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(got), "(in progress") {
		t.Fatalf("stale marker was not cleared:\n%s", got)
	}
	if !strings.Contains(string(got), "- add the share QR code") {
		t.Fatalf("pending request was lost:\n%s", got)
	}
	if queued := w.nextAll(); len(queued) != 1 || queued[0] != [2]string{"demo", "0010-a"} {
		t.Fatalf("queue after recovery = %#v", queued)
	}
}

func TestAgentCommandSupportsCodex(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test-openai-key")
	t.Setenv("CODEX_API_KEY", "")
	cmd := agentCommand(context.Background(), "/usr/local/bin/codex", "/studio", "do the edit")
	want := []string{
		"/usr/local/bin/codex", "exec", "--dangerously-bypass-approvals-and-sandbox",
		"--skip-git-repo-check", "--ephemeral", "-C", "/studio", "do the edit",
	}
	if !reflect.DeepEqual(cmd.Args, want) {
		t.Fatalf("args = %#v, want %#v", cmd.Args, want)
	}
	found := false
	for _, entry := range cmd.Env {
		if entry == "CODEX_API_KEY=test-openai-key" {
			found = true
		}
		if strings.HasPrefix(entry, "CODEX_API_KEY=") && entry != "CODEX_API_KEY=test-openai-key" {
			t.Fatalf("unexpected Codex credential entry in command environment")
		}
	}
	if !found {
		t.Fatal("Codex command did not map OPENAI_API_KEY to CODEX_API_KEY")
	}
}

func TestAgentPassTimeoutAllowsComplexRedesigns(t *testing.T) {
	t.Setenv("VSTD_AGENT_TIMEOUT", "")
	if got := agentPassTimeout(); got != 30*time.Minute {
		t.Fatalf("default timeout = %s, want 30m", got)
	}
	t.Setenv("VSTD_AGENT_TIMEOUT", "45m")
	if got := agentPassTimeout(); got != 45*time.Minute {
		t.Fatalf("configured timeout = %s, want 45m", got)
	}
}

func TestAgentCriticTimeoutAllowsVisualReview(t *testing.T) {
	t.Setenv("VSTD_AGENT_CRITIC_TIMEOUT", "")
	if got := agentCriticTimeout(); got != 20*time.Minute {
		t.Fatalf("default critic timeout = %s, want 20m", got)
	}
	t.Setenv("VSTD_AGENT_CRITIC_TIMEOUT", "25m")
	if got := agentCriticTimeout(); got != 25*time.Minute {
		t.Fatalf("configured critic timeout = %s, want 25m", got)
	}
}

func TestAgentCommandKeepsClaudeInvocation(t *testing.T) {
	cmd := agentCommand(context.Background(), "claude", "/studio", "do the edit")
	want := []string{
		"claude", "--dangerously-skip-permissions", "--allowedTools",
		"Edit,Write,MultiEdit,NotebookEdit,Read,Glob,Grep,Bash", "-p", "do the edit",
	}
	if !reflect.DeepEqual(cmd.Args, want) {
		t.Fatalf("args = %#v, want %#v", cmd.Args, want)
	}
}

func TestAgentCommandAddsCriticImagesForCodex(t *testing.T) {
	t.Setenv("CODEX_API_KEY", "test-key")
	cmd := agentCommandWithImages(context.Background(), "codex", "/studio", "compare them", []string{"/tmp/current.png", "/tmp/source.png"})
	want := []string{
		"codex", "exec", "--dangerously-bypass-approvals-and-sandbox",
		"--skip-git-repo-check", "--ephemeral", "-C", "/studio",
		"-i", "/tmp/current.png", "-i", "/tmp/source.png", "compare them",
	}
	if !reflect.DeepEqual(cmd.Args, want) {
		t.Fatalf("args = %#v, want %#v", cmd.Args, want)
	}
}

func TestParseCodexUsageFromHeadlessRunLog(t *testing.T) {
	out := []byte(`OpenAI Codex v0.150.0
--------
model: gpt-5.6-sol
provider: openai
session id: 01a043c1-1716-7bd1-b8e8-a2755f4225a5
--------
codex
Updated the slide.

tokens used
33,453
Updated the slide.
`)
	usage, ok := parseCodexUsage(out)
	if !ok {
		t.Fatal("Codex usage was not detected")
	}
	if usage.Model != "gpt-5.6-sol" || usage.SessionID != "01a043c1-1716-7bd1-b8e8-a2755f4225a5" || usage.TotalTokens != 33453 {
		t.Fatalf("usage = %#v", usage)
	}
}

func TestParseCodexUsageRequiresReportedTokens(t *testing.T) {
	if usage, ok := parseCodexUsage([]byte("model: gpt-5.6-sol\nsession id: run-123\n")); ok {
		t.Fatalf("unexpected usage without token summary: %#v", usage)
	}
}

func TestRedesignDescriptorsFollowActionableQueue(t *testing.T) {
	st := testStudio(t)
	file := st.SlidePath("demo", "0010-a", ".md")
	put := func(body string) {
		t.Helper()
		if err := os.WriteFile(file, []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	put("# Title\n\n## Edit requests\n- simplify chart\n\n## Log\n")
	srv := New(st, ModeStudio)
	got := srv.RedesignRequests("demo")
	if len(got) != 1 || got[0].Slide != "0010-a" || len(got[0].Hash) != 64 {
		t.Fatalf("descriptors %#v", got)
	}
	put("# Retitled\n\n## Edit requests\n- simplify chart\n\n## Log\n- human edit\n")
	if next := srv.RedesignRequests("demo"); len(next) != 1 || next[0].Hash != got[0].Hash {
		t.Fatal("unrelated edits changed dispatch identity")
	}
	put("# Title\n\n## Edit requests\n- simplify chart\n- change colors\n\n## Log\n")
	if srv.RedesignRequests("demo")[0].Hash == got[0].Hash {
		t.Fatal("changed request reused identity")
	}
	for _, section := range []string{"- resolved: simplify chart", "- (worker error: failed)\n- simplify chart", "- (in progress — 60%)\n- simplify chart"} {
		put("# Title\n\n## Edit requests\n" + section + "\n\n## Log\n")
		if len(srv.RedesignRequests("demo")) != 0 {
			t.Fatalf("non-actionable %q dispatched", section)
		}
	}
	if len(srv.RedesignRequests("foreign")) != 0 {
		t.Fatal("cross-deck selector leaked")
	}
}

func TestSelectedAgentSweepNeverStartsOtherSlides(t *testing.T) {
	st := testStudio(t)
	selected := st.SlidePath("demo", "0010-a", ".md")
	other := st.SlidePath("demo", "0020-b", ".md")
	body := []byte("# Before\n\n## Edit requests\n- simplify chart\n\n## Log\n")
	for _, p := range []string{selected, other} {
		if err := os.WriteFile(p, body, 0644); err != nil {
			t.Fatal(err)
		}
	}
	bin := filepath.Join(t.TempDir(), "claude")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nexit 1\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VSTD_AGENT_CMD", bin)
	t.Setenv("VSTD_AGENT_SANDBOX", "")
	if n := New(st, ModeStudio).RunAgentSelected("demo", "0010-a"); n != 1 {
		t.Fatalf("passes=%d", n)
	}
	got, _ := os.ReadFile(other)
	if string(got) != string(body) {
		t.Fatal("unselected slide changed")
	}
	got, _ = os.ReadFile(selected)
	if !strings.Contains(string(got), "worker error") {
		t.Fatal("selected slide not executed")
	}
}
func TestEditorTransformExposesCompanionDispatchDescriptors(t *testing.T) {
	st := testStudio(t)
	if err := os.WriteFile(st.SlidePath("demo", "0010-a", ".md"), []byte("# Before\n\n## Edit requests\n- simplify chart\n\n## Log\n"), 0644); err != nil {
		t.Fatal(err)
	}
	files, err := studio.CloudContent(st.Root)
	if err != nil {
		t.Fatal(err)
	}
	result, err := TransformEditor(context.Background(), EditorTransformInput{Deck: "demo", Method: "GET", Path: "/api/deck/demo/status", Files: files.Files})
	if err != nil {
		t.Fatal(err)
	}
	var value struct {
		Requests []RedesignRequest `json:"redesignRequests"`
	}
	if err = json.Unmarshal(result.Body, &value); err != nil || len(value.Requests) != 1 || value.Requests[0].Slide != "0010-a" {
		t.Fatalf("status=%s err=%v", result.Body, err)
	}
	if strings.Contains(string(result.Body), "simplify chart") {
		t.Fatal("status exposed private request text")
	}
}
