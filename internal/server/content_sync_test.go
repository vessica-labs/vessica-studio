package server

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func seedRemote(t *testing.T) (origin, seed string) {
	t.Helper()
	base := t.TempDir()
	origin = filepath.Join(base, "origin.git")
	runGit(t, base, "init", "--bare", origin)
	seed = filepath.Join(base, "seed")
	runGit(t, base, "clone", origin, seed)
	runGit(t, seed, "config", "user.name", "Test")
	runGit(t, seed, "config", "user.email", "test@example.com")
	files := map[string]string{
		"studio.yaml":                   "theme_default: default\n",
		"Dockerfile":                    "FROM scratch\n",
		"decks/demo/deck.yaml":          "title: Demo\ntheme: default\n",
		"decks/demo/slides/0010-a.html": "<section class=\"slide\">remote</section>\n",
		"decks/demo/slides/0010-a.md":   "# Demo\n\n## Edit requests\n\n## Log\n",
		"themes/default/theme.css":      ".slide{}\n",
		"library/manifest.json":         "{\"version\":1}\n",
	}
	for name, body := range files {
		path := filepath.Join(seed, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	runGit(t, seed, "add", ".")
	runGit(t, seed, "commit", "-m", "seed")
	runGit(t, seed, "push", "origin", "HEAD:main")
	return origin, seed
}

func TestContentSyncPushesHostedEditsAndPullsRemoteContent(t *testing.T) {
	origin, seed := seedRemote(t)
	runtime := filepath.Join(t.TempDir(), "runtime")
	if err := os.MkdirAll(runtime, 0o755); err != nil {
		t.Fatal(err)
	}
	// Represents the content baked into the Railway image before .git is
	// excluded by .dockerignore.
	for _, name := range []string{"studio.yaml", "Dockerfile", "decks", "themes", "library"} {
		runGit(t, seed, "checkout", "--", name)
		src := filepath.Join(seed, name)
		dst := filepath.Join(runtime, name)
		info, err := os.Stat(src)
		if err != nil {
			t.Fatal(err)
		}
		if info.IsDir() {
			cmd := exec.Command("cp", "-R", src, dst)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("copy fixture: %v: %s", err, out)
			}
		} else {
			b, err := os.ReadFile(src)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(dst, b, info.Mode()); err != nil {
				t.Fatal(err)
			}
		}
	}
	syncer := newContentSync(nil, contentSyncConfig{
		Root: runtime, Remote: origin, Branch: "main",
		Debounce: time.Millisecond, Poll: time.Hour,
	})
	if err := syncer.bootstrap(); err != nil {
		t.Fatal(err)
	}
	syncer.mu.Lock()
	syncer.ready = true
	syncer.state = syncStateReady
	syncer.mu.Unlock()

	slide := filepath.Join(runtime, "decks/demo/slides/0010-a.html")
	if err := os.WriteFile(slide, []byte("<section class=\"slide\">hosted edit</section>\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runtime, "Dockerfile"), []byte("LOCAL RUNTIME ONLY\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	syncer.Notify()
	if err := syncer.syncOnce(); err != nil {
		t.Fatal(err)
	}
	if got := runGit(t, runtime, "show", "origin/main:decks/demo/slides/0010-a.html"); !strings.Contains(got, "hosted edit") {
		t.Fatalf("remote slide = %q", got)
	}
	if got := runGit(t, runtime, "show", "origin/main:Dockerfile"); got != "FROM scratch" {
		t.Fatalf("out-of-scope Dockerfile was pushed: %q", got)
	}

	runGit(t, seed, "pull", "--ff-only", "origin", "main")
	if err := os.WriteFile(filepath.Join(seed, "themes/default/theme.css"), []byte(".slide{color:green}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, seed, "add", "themes/default/theme.css")
	runGit(t, seed, "commit", "-m", "remote theme")
	runGit(t, seed, "push", "origin", "HEAD:main")
	if err := syncer.syncOnce(); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(runtime, "themes/default/theme.css"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != ".slide{color:green}\n" {
		t.Fatalf("runtime theme = %q", got)
	}
}
