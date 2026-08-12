package plugin

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestPackagedWorkflowsMatchCodexLaunchers(t *testing.T) {
	names := Names()
	if len(names) == 0 {
		t.Fatal("Names() returned no embedded workflows")
	}
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller could not resolve the repository path")
	}
	repoRoot := filepath.Dir(filepath.Dir(file))
	packaged := make(map[string]bool, len(names))

	for _, name := range names {
		packaged[name] = true
		body, err := Skill(name)
		if err != nil {
			t.Errorf("Skill(%q) error = %v", name, err)
			continue
		}
		if strings.TrimSpace(body) == "" {
			t.Errorf("Skill(%q) returned an empty workflow", name)
		}
		launcher := filepath.Join(repoRoot, "codex", "prompts", "vstd-"+name+".md")
		if _, err := os.Stat(launcher); err != nil {
			t.Errorf("Codex launcher for %q: %v", name, err)
		}
	}
	launchers, err := filepath.Glob(filepath.Join(repoRoot, "codex", "prompts", "vstd-*.md"))
	if err != nil {
		t.Fatalf("glob Codex launchers: %v", err)
	}
	for _, launcher := range launchers {
		name := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(launcher), "vstd-"), ".md")
		if !packaged[name] {
			t.Errorf("Codex launcher %q has no embedded workflow", filepath.Base(launcher))
		}
	}

	conventions, err := Conventions()
	if err != nil {
		t.Fatalf("Conventions() error = %v", err)
	}
	if strings.TrimSpace(conventions) == "" {
		t.Fatal("Conventions() returned an empty document")
	}
}
