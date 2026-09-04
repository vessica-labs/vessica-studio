package studio

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
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

func TestCloudSnapshotRequiresPairedSlides(t *testing.T) {
	for _, ext := range []string{"html", "md"} {
		files := []ContentFile{{Path: "studio.yaml", Content: []byte("port: 4400\n")}, {Path: "decks/demo/deck.yaml", Content: []byte("title: Demo\n")}, {Path: "decks/demo/slides/0010-intro." + ext, Content: []byte("content")}}
		if err := ValidateCloudContent(files); err == nil {
			t.Fatalf("accepted unpaired %s", ext)
		}
	}
}

func TestInterruptedCloudPullRequiresRecovery(t *testing.T) {
	root := t.TempDir()
	if err := Init(root); err != nil {
		t.Fatal(err)
	}
	before, err := CloudContent(root)
	if err != nil {
		t.Fatal(err)
	}
	after := []ContentFile{{Path: "studio.yaml", Content: []byte("port: 9999\n")}}
	if err := beginCloudTransaction(root, before.Files, after); err != nil {
		t.Fatal(err)
	}
	// Simulate termination after the first file replacement, without defers.
	if err := os.WriteFile(filepath.Join(root, "studio.yaml"), after[0].Content, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(root); !errors.Is(err, ErrCloudRecovery) {
		t.Fatalf("opened partial content: %v", err)
	}
	if err := RecoverCloudContent(root); err != nil {
		t.Fatal(err)
	}
	restored, err := CloudContent(root)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Digest != before.Digest {
		t.Fatal("failed to restore original projection")
	}
}

func TestCloudSnapshotRejectsCredentialCommands(t *testing.T) {
	if err := ValidateCloudContent([]ContentFile{{Path: "studio.yaml", Content: []byte("share_secret_cmd: malicious-command\n")}}); err == nil {
		t.Fatal("accepted executable credential configuration")
	}
}

func TestCloudContentRejectsMaliciousSnapshot(t *testing.T) {
	root := t.TempDir()
	for _, files := range [][]ContentFile{
		{{Path: "../outside", Content: []byte("x")}},
		{{Path: "studio.yaml", Content: []byte("a")}, {Path: "STUDIO.YAML", Content: []byte("b")}},
		{{Path: ".vstd/cloud-workspace.json", Content: []byte("x")}},
	} {
		if err := ApplyCloudContent(root, files); err == nil {
			t.Fatalf("accepted %#v", files)
		}
	}
}

func TestCloudApplyRejectsSymlinkParentWithoutChangingContent(t *testing.T) {
	root := t.TempDir()
	if err := Init(root); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "themes", "external")); err != nil {
		t.Skip(err)
	}
	before, _ := os.ReadFile(filepath.Join(root, "studio.yaml"))
	files := []ContentFile{{Path: "studio.yaml", Content: []byte("port: 9999\n")}, {Path: "themes/external/style.css", Content: []byte("overwrite")}}
	if err := ApplyCloudContent(root, files); err == nil {
		t.Fatal("accepted symlink parent")
	}
	if _, err := os.Stat(filepath.Join(outside, "style.css")); !os.IsNotExist(err) {
		t.Fatal("wrote outside studio")
	}
	after, _ := os.ReadFile(filepath.Join(root, "studio.yaml"))
	if string(before) != string(after) {
		t.Fatal("changed existing content")
	}
}

func TestCloudApplyRejectsInvalidExistingTree(t *testing.T) {
	root := t.TempDir()
	if err := Init(root); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "studio.yaml")
	before := []byte("invalid: [yaml")
	if err := os.WriteFile(path, before, 0644); err != nil {
		t.Fatal(err)
	}
	if err := ApplyCloudContent(root, []ContentFile{{Path: "studio.yaml", Content: []byte("port: 9999\n")}}); err == nil {
		t.Fatal("overwrote unreadable tree")
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("lost existing content")
	}
}
