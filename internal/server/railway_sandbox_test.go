package server

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestSandboxDispatcherEnvironmentIsAllowlisted(t *testing.T) {
	for key, value := range map[string]string{
		"PATH": "/usr/bin", "RAILWAY_TOKEN": "project-token", "RAILWAY_ENVIRONMENT_ID": "environment-id",
		"RAILWAY_API_TOKEN": "account-token",
		"OPENAI_API_KEY":    "openai-secret", "DATABASE_URL": "postgres://secret", "TELNYX_API_KEY": "telnyx-secret",
		"RESEND_API_KEY": "resend-secret", "AWS_SECRET_ACCESS_KEY": "s3-secret", "SESSION_SECRET": "session-secret",
	} {
		t.Setenv(key, value)
	}
	env := sandboxDispatcherEnv()
	for _, forbidden := range []string{"OPENAI_API_KEY", "DATABASE_URL", "TELNYX_API_KEY", "RESEND_API_KEY", "AWS_SECRET_ACCESS_KEY", "SESSION_SECRET", "RAILWAY_API_TOKEN"} {
		if hasEnvKey(env, forbidden) {
			t.Fatalf("dispatcher environment leaked %s", forbidden)
		}
	}
	for _, required := range []string{"PATH", "RAILWAY_TOKEN", "RAILWAY_ENVIRONMENT_ID", "VSTD_RAILWAY_SDK"} {
		if !hasEnvKey(env, required) {
			t.Fatalf("dispatcher environment omitted %s", required)
		}
	}
}

func TestSandboxCodexCredentialUsesRailwayReference(t *testing.T) {
	t.Setenv("RAILWAY_SERVICE_NAME", "Studio API")
	t.Setenv("VSTD_AGENT_SANDBOX_SECRET_SERVICE", "")
	t.Setenv("VSTD_AGENT_SANDBOX_SECRET_VARIABLE", "")
	t.Setenv("OPENAI_API_KEY", "must-not-appear")
	if got, want := sandboxCodexKeyReference(), "${{Studio API.OPENAI_API_KEY}}"; got != want {
		t.Fatalf("reference = %q, want %q", got, want)
	}
}

func TestPublicCodexWorkerRequiresRailwaySandbox(t *testing.T) {
	w := &agentWorker{s: New(testStudio(t), ModePublic), bin: "codex"}
	if err := w.validateExecutionBackend(); err == nil || !strings.Contains(err.Error(), "VSTD_AGENT_SANDBOX=railway") {
		t.Fatalf("public in-process Codex was not refused: %v", err)
	}
}

func TestCollectRailwaySandboxInputsScopesDeckAndAssets(t *testing.T) {
	st := testStudio(t)
	writeTestFile(t, filepath.Join(st.Root, "decks/demo/slides/0010-a.html"), `<section class="slide"><img src="../../../library/images/used.png"></section>`)
	writeTestFile(t, filepath.Join(st.Root, "decks/other/deck.yaml"), "title: Other\ntheme: default\n")
	writeTestFile(t, filepath.Join(st.Root, "decks/other/slides/0010-b.html"), `<section class="slide">private</section>`)
	writeTestFile(t, filepath.Join(st.Root, "decks/other/slides/0010-b.md"), "# Private\n")
	writeTestFile(t, filepath.Join(st.Root, "library/images/used.png"), "used")
	writeTestFile(t, filepath.Join(st.Root, "library/images/unused.png"), "unused")
	writeTestFile(t, filepath.Join(st.Root, "requests/match.yaml"), "deck: demo\nslide: 0010-a\n")
	writeTestFile(t, filepath.Join(st.Root, "requests/other.yaml"), "deck: other\nslide: 0010-b\n")

	inputs, _, err := collectRailwaySandboxInputs(st.Root, "demo", "0010-a", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, input := range inputs {
		got[input.Relative] = true
	}
	for _, required := range []string{"studio.yaml", "decks/demo/deck.yaml", "decks/demo/slides/0010-a.html", "themes/default/theme.css", "library/manifest.json", "library/images/used.png", "requests/match.yaml"} {
		if !got[required] {
			t.Errorf("missing scoped input %s", required)
		}
	}
	for _, forbidden := range []string{"decks/other/deck.yaml", "decks/other/slides/0010-b.html", "library/images/unused.png", "requests/other.yaml"} {
		if got[forbidden] {
			t.Errorf("included out-of-scope input %s", forbidden)
		}
	}
}

func TestCollectRailwaySandboxInputsRejectsLibraryPathEscape(t *testing.T) {
	st := testStudio(t)
	writeTestFile(t, filepath.Join(st.Root, "decks/demo/slides/0010-a.html"), `<section class="slide"><img src="../../../library/images/../../decks/other/slides/0010-b.html"></section>`)
	writeTestFile(t, filepath.Join(st.Root, "decks/other/slides/0010-b.html"), `<section class="slide">private</section>`)

	_, _, err := collectRailwaySandboxInputs(st.Root, "demo", "0010-a", nil, "")
	if err == nil || !strings.Contains(err.Error(), "escapes library") {
		t.Fatalf("library path escape was not rejected: %v", err)
	}
}

func TestCollectRailwaySandboxInputsRejectsSymlinkedContent(t *testing.T) {
	st := testStudio(t)
	outside := filepath.Join(t.TempDir(), "outside-secret")
	writeTestFile(t, outside, "secret")
	link := filepath.Join(st.Root, "decks/demo/slides/leak.txt")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	if _, _, err := collectRailwaySandboxInputs(st.Root, "demo", "0010-a", nil, ""); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("symlinked input was not rejected: %v", err)
	}
}

func TestApplyRailwaySandboxChangesRejectsEscapeAndConcurrentEdit(t *testing.T) {
	st := testStudio(t)
	inputs, _, err := collectRailwaySandboxInputs(st.Root, "demo", "0010-a", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := applyRailwaySandboxChanges(st.Root, "demo", t.TempDir(), inputs, []sandboxChange{{Path: "../secret", SHA256: "bad", Size: 1}}); err == nil {
		t.Fatal("path traversal result was accepted")
	}
	writeTestFile(t, st.SlidePath("demo", "0010-a", ".html"), "human concurrent edit")
	resultDir := t.TempDir()
	resultPath := filepath.Join(resultDir, "files/decks/demo/slides/0010-a.html")
	writeTestFile(t, resultPath, "sandbox edit")
	hash, _, err := hashFileIfExists(resultPath)
	if err != nil {
		t.Fatal(err)
	}
	err = applyRailwaySandboxChanges(st.Root, "demo", resultDir, inputs, []sandboxChange{{Path: "decks/demo/slides/0010-a.html", SHA256: hash, Size: 12}})
	if err == nil || !strings.Contains(err.Error(), "changed during sandbox run") {
		t.Fatalf("concurrent edit was not protected: %v", err)
	}
}

func TestRailwaySandboxOutputRejectsGeneratedDeckBuild(t *testing.T) {
	if sandboxOutputAllowed("demo", "decks/demo/build/index.html") {
		t.Fatal("generated deck build was accepted as sandbox output")
	}
	if !sandboxOutputAllowed("demo", "decks/demo/slides/0010-a.html") {
		t.Fatal("slide content was rejected as sandbox output")
	}
}

func TestApplyRailwaySandboxChangesWritesScopedResult(t *testing.T) {
	st := testStudio(t)
	inputs, _, err := collectRailwaySandboxInputs(st.Root, "demo", "0010-a", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	resultDir := t.TempDir()
	resultPath := filepath.Join(resultDir, "files/decks/demo/slides/0010-a.html")
	writeTestFile(t, resultPath, "sandbox edit")
	hash, _, err := hashFileIfExists(resultPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := applyRailwaySandboxChanges(st.Root, "demo", resultDir, inputs, []sandboxChange{{Path: "decks/demo/slides/0010-a.html", SHA256: hash, Size: 12}}); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(st.SlidePath("demo", "0010-a", ".html"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "sandbox edit" {
		t.Fatalf("applied content = %q", got)
	}
}

func TestEmbeddedRailwayRunnerEnforcesIsolationAndTeardown(t *testing.T) {
	script := string(railwaySandboxRunnerJS)
	for _, required := range []string{`networkIsolation: "ISOLATED"`, "forbidden sandbox environment", "input.remoteImages ?? []", "isGeneratedOutput(relative)", `process.on(signal`, "await sandbox.destroy()", "finally"} {
		if !strings.Contains(script, required) {
			t.Errorf("embedded dispatcher missing %q", required)
		}
	}
	if strings.Contains(script, "process.env.OPENAI_API_KEY") {
		t.Fatal("dispatcher reads the API service OpenAI secret")
	}
}

func TestRunSandboxDispatcherAllowsCleanupAfterCancellation(t *testing.T) {
	if os.Getenv("VSTD_TEST_SANDBOX_DISPATCHER_HELPER") == "1" {
		runSandboxDispatcherCleanupHelper()
		return
	}
	dir := t.TempDir()
	ready := filepath.Join(dir, "ready")
	cleaned := filepath.Join(dir, "cleaned")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := exec.Command(os.Args[0], "-test.run=TestRunSandboxDispatcherAllowsCleanupAfterCancellation")
	cmd.Env = append(os.Environ(), "VSTD_TEST_SANDBOX_DISPATCHER_HELPER=1", "VSTD_TEST_SANDBOX_READY="+ready, "VSTD_TEST_SANDBOX_CLEANED="+cleaned)
	done := make(chan error, 1)
	go func() {
		done <- runSandboxDispatcher(ctx, cmd, 2*time.Second)
	}()
	waitForTestFile(t, ready)
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("dispatcher cancellation error = %v, want context.Canceled", err)
	}
	if _, err := os.Stat(cleaned); err != nil {
		t.Fatalf("dispatcher did not finish cleanup after cancellation: %v", err)
	}
}

func runSandboxDispatcherCleanupHelper() {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	if err := os.WriteFile(os.Getenv("VSTD_TEST_SANDBOX_READY"), []byte("ready"), 0o600); err != nil {
		os.Exit(2)
	}
	<-signals
	if err := os.WriteFile(os.Getenv("VSTD_TEST_SANDBOX_CLEANED"), []byte("cleaned"), 0o600); err != nil {
		os.Exit(3)
	}
	os.Exit(0)
}

func waitForTestFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", path)
}

func writeTestFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
