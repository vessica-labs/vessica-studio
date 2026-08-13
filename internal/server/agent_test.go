package server

import (
	"context"
	"reflect"
	"testing"
)

func TestAgentCommandSupportsCodex(t *testing.T) {
	cmd := agentCommand(context.Background(), "/usr/local/bin/codex", "/studio", "do the edit")
	want := []string{
		"/usr/local/bin/codex", "exec", "--dangerously-bypass-approvals-and-sandbox",
		"--skip-git-repo-check", "--ephemeral", "-C", "/studio", "do the edit",
	}
	if !reflect.DeepEqual(cmd.Args, want) {
		t.Fatalf("args = %#v, want %#v", cmd.Args, want)
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
