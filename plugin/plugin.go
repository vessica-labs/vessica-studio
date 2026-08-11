// Package plugin embeds the agent skill definitions so the vstd binary can
// serve them to any agent runtime (`vstd skill <name>`). The same files are
// read natively by Claude Code/Cowork via the .claude-plugin manifest; Codex
// reaches them through the thin prompt launchers in codex/prompts. One
// source of truth, no drift.
package plugin

import (
	"embed"
	"fmt"
	"sort"
	"strings"
)

//go:embed skills/*/SKILL.md docs/*.md
var FS embed.FS

// Names returns the available skill names, sorted.
func Names() []string {
	ents, err := FS.ReadDir("skills")
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range ents {
		if e.IsDir() {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out
}

// Skill returns the SKILL.md body for name.
func Skill(name string) (string, error) {
	b, err := FS.ReadFile("skills/" + name + "/SKILL.md")
	if err != nil {
		return "", fmt.Errorf("unknown skill %q — available: %s", name, strings.Join(Names(), ", "))
	}
	return string(b), nil
}

// Conventions returns the shared authoring conventions document.
func Conventions() (string, error) {
	b, err := FS.ReadFile("docs/conventions.md")
	return string(b), err
}
