package chromium

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFindPrefersExplicitBinary(t *testing.T) {
	t.Setenv("VSTD_CHROMIUM", "/env/chrome")
	if got := Find("/chosen/chrome"); got != "/chosen/chrome" {
		t.Fatalf("Find explicit = %q", got)
	}
}

func TestFindUsesPathBinary(t *testing.T) {
	t.Setenv("VSTD_CHROMIUM", "")
	dir := t.TempDir()
	bin := filepath.Join(dir, "chromium")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	if got := Find(""); got != bin {
		t.Fatalf("Find PATH = %q, want %q", got, bin)
	}
}

func TestMacApplicationCandidatesIncludeChrome(t *testing.T) {
	want := "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"
	for _, candidate := range appPaths {
		if candidate == want {
			return
		}
	}
	t.Fatalf("macOS Google Chrome path missing from candidates: %#v", appPaths)
}
