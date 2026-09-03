package studio

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/vessica-labs/vessica-studio/internal/cloud"
)

func TestCloudContentProjectsOnlyCanonicalFiles(t *testing.T) {
	root := t.TempDir()
	if err := Init(root); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".vstd", "secret"), []byte("x"), 0600); err == nil {
		t.Fatal("expected absent metadata directory")
	}
	if err := os.MkdirAll(filepath.Join(root, "decks", "demo", "build"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "decks", "demo", "build", "index.html"), []byte("generated"), 0644); err != nil {
		t.Fatal(err)
	}
	s, err := CloudContent(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range s.Files {
		if f.Path == "decks/demo/build/index.html" || f.Path == ".gitignore" {
			t.Fatalf("projected excluded path %q", f.Path)
		}
	}
	if s.Digest == "" || len(s.Files) == 0 {
		t.Fatal("missing deterministic projection")
	}
}

func TestCloudContentRejectsMaliciousSnapshot(t *testing.T) {
	root := t.TempDir()
	for _, files := range [][]cloud.File{
		{{Path: "../outside", Content: []byte("x")}},
		{{Path: "studio.yaml", Content: []byte("a")}, {Path: "STUDIO.YAML", Content: []byte("b")}},
		{{Path: ".vstd/cloud-workspace.json", Content: []byte("x")}},
	} {
		if err := ApplyCloudContent(root, files); err == nil {
			t.Fatalf("accepted %#v", files)
		}
	}
}
