package cloudworkspace

import (
	"github.com/vessica-labs/vessica-studio/internal/studio"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIsolatedBranchReconcilesWithoutTouchingHumanEdits(t *testing.T) {
	root := localStudio(t)
	st, err := studio.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.NewDeck("demo", "Demo"); err != nil {
		t.Fatal(err)
	}
	id := "0090-work"
	if err := st.NewSlide("demo", id, "Base", `<section class="slide"><h1 id="title" style="color:black">Base</h1></section>`); err != nil {
		t.Fatal(err)
	}
	branch, err := BeginBranch(root)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join("decks", "demo", "slides", id+".html")
	write := func(root, text string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, path), []byte(text), 0644); err != nil {
			t.Fatal(err)
		}
	}
	write(branch.Root, `<section class="slide"><h1 id="title" style="color:blue">Agent</h1></section>`)
	before, _ := os.ReadFile(filepath.Join(root, path))
	if strings.Contains(string(before), "Agent") {
		t.Fatal("worktree leaked into parent")
	}
	write(root, `<section class="slide"><h1 id="title" style="color:black">Human</h1></section>`)
	if err := FinishBranch(branch.Root); err != nil {
		t.Fatal(err)
	}
	saved, _ := os.ReadFile(filepath.Join(root, path))
	if !strings.Contains(string(saved), "Human") || !strings.Contains(string(saved), "color:blue") {
		t.Fatalf("merge: %s", saved)
	}
	if err := FinishBranch(branch.Root); err != nil {
		t.Fatal(err)
	}
	replay, _ := os.ReadFile(filepath.Join(root, path))
	if string(replay) != string(saved) {
		t.Fatal("finish retry changed result")
	}
	files, err := os.ReadDir(filepath.Join(root, ".vstd", "checkpoints"))
	if err != nil || len(files) < 3 {
		t.Fatalf("missing retained inputs: %v", err)
	}
}
