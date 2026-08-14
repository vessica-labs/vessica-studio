package server

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"
)

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
