package plugin

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

type skillFrontmatter struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
}

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
		frontmatter, err := parseSkillFrontmatter(body)
		if err != nil {
			t.Errorf("Skill(%q) frontmatter: %v", name, err)
		} else {
			if frontmatter.Name != name {
				t.Errorf("Skill(%q) frontmatter name = %q", name, frontmatter.Name)
			}
			if strings.TrimSpace(frontmatter.Description) == "" {
				t.Errorf("Skill(%q) has an empty frontmatter description", name)
			}
		}
		if !strings.Contains(body, "../../docs/conventions.md") {
			t.Errorf("Skill(%q) does not reference the canonical conventions path", name)
		}
		conventionsPath := filepath.Join(repoRoot, "plugin", "skills", name, "../../docs/conventions.md")
		if _, err := os.Stat(conventionsPath); err != nil {
			t.Errorf("Skill(%q) canonical conventions reference: %v", name, err)
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
	for _, want := range []string{"hybrid editable chart", "vstd chart promote-text", "data-chart-group", "chart-label"} {
		if !strings.Contains(conventions, want) {
			t.Errorf("conventions missing chart-authoring contract %q", want)
		}
	}
	for _, want := range []string{
		"vstd new", "vstd list", "vstd fork", "vstd diff-upstream", "vstd build", "vstd serve",
		"vstd asset gen", "vstd asset find", "vstd asset add-video", "vstd chart promote-text",
		"vstd key check", "vstd skill",
	} {
		if !strings.Contains(conventions, want) {
			t.Errorf("conventions missing CLI command %q", want)
		}
	}
	for _, want := range []string{
		"vstd cloud workspace status", "vstd cloud workspace pull", "vstd cloud workspace sync",
		"vstd cloud publish", "offline", "unsynced", "conflict",
	} {
		if !strings.Contains(conventions, want) {
			t.Errorf("conventions missing cloud workspace contract %q", want)
		}
	}
	for _, name := range []string{"deck-new", "deck-review", "slide-add", "slide-edit"} {
		body, err := Skill(name)
		if err != nil {
			t.Errorf("Skill(%q) error = %v", name, err)
			continue
		}
		if !strings.Contains(body, "vstd cloud workspace status") {
			t.Errorf("Skill(%q) does not detect cloud workspace state through vstd", name)
		}
	}

	codexGuide, err := os.ReadFile(filepath.Join(repoRoot, "codex", "AGENTS.md"))
	if err != nil {
		t.Fatalf("read codex/AGENTS.md: %v", err)
	}
	guideText := string(codexGuide)
	sectionStart := strings.Index(guideText, "For a specific workflow")
	sectionEnd := strings.Index(guideText, "\n- Preview")
	if sectionStart == -1 || sectionEnd <= sectionStart {
		t.Fatal("codex/AGENTS.md is missing its specific-workflow skill list")
	}
	guideSkills := make(map[string]bool)
	for _, match := range regexp.MustCompile("`([a-z0-9]+(-[a-z0-9]+)*)`").FindAllStringSubmatch(guideText[sectionStart:sectionEnd], -1) {
		guideSkills[match[1]] = true
	}
	for name := range packaged {
		if !guideSkills[name] {
			t.Errorf("codex/AGENTS.md does not list packaged skill %q", name)
		}
	}
	for name := range guideSkills {
		if !packaged[name] {
			t.Errorf("codex/AGENTS.md lists unknown skill %q", name)
		}
	}
}

func parseSkillFrontmatter(body string) (skillFrontmatter, error) {
	var frontmatter skillFrontmatter
	parts := strings.SplitN(body, "---", 3)
	if len(parts) != 3 || strings.TrimSpace(parts[0]) != "" {
		return frontmatter, os.ErrInvalid
	}
	if err := yaml.Unmarshal([]byte(parts[1]), &frontmatter); err != nil {
		return frontmatter, err
	}
	if frontmatter.Name == "" || frontmatter.Description == "" {
		return frontmatter, os.ErrInvalid
	}
	return frontmatter, nil
}
