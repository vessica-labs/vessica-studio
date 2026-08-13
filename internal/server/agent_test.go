package server

import (
	"context"
	"reflect"
	"strings"
	"testing"
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
