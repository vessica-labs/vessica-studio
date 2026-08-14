package server

import (
	"context"
	"os"
	"reflect"
	"strings"
	"testing"
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
