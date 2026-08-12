package library

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadMissingManifestReturnsInitializedCatalog(t *testing.T) {
	manifest, err := Load(t.TempDir())
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if manifest.Version != 1 {
		t.Fatalf("Version = %d, want 1", manifest.Version)
	}
	if manifest.StyleFamilies == nil {
		t.Fatal("StyleFamilies is nil, want initialized map")
	}
}

func TestLoadReadFailurePreservesLegacyEmptyCatalog(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "manifest.json"), 0o755); err != nil {
		t.Fatal(err)
	}

	manifest, err := Load(dir)
	if err != nil {
		t.Fatalf("Load() error = %v, want legacy empty-catalog fallback", err)
	}
	if manifest.Version != 1 || manifest.StyleFamilies == nil {
		t.Fatalf("Load() = %#v, want initialized version-1 catalog", manifest)
	}
}

func TestLoadPreservesImageVideoAndStyleFamilyData(t *testing.T) {
	dir := t.TempDir()
	input := `{
  "version": 1,
  "styleFamilies": {"editorial": {"promptPrefix": "paper collage"}},
  "assets": [{"id":"hero","file":"img/hero.png","prompt":"a stage","model":"gpt-image-2","size":"1024x1024","created":"2026-08-11","hash":"abc"}],
  "videos": [{"id":"intro","file":"video/def.mp4","hash":"def","bytes":42,"created":"2026-08-11"}]
}`
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(input), 0o644); err != nil {
		t.Fatal(err)
	}

	manifest, err := Load(dir)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got := manifest.StyleFamilies["editorial"].PromptPrefix; got != "paper collage" {
		t.Fatalf("PromptPrefix = %q, want %q", got, "paper collage")
	}
	if len(manifest.Assets) != 1 || manifest.Assets[0].ID != "hero" {
		t.Fatalf("Assets = %#v, want hero image", manifest.Assets)
	}
	if len(manifest.Videos) != 1 || manifest.Videos[0].ID != "intro" || manifest.Videos[0].Bytes != 42 {
		t.Fatalf("Videos = %#v, want intro video", manifest.Videos)
	}
}

func TestLoadMalformedManifestReturnsContextualError(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(`{"version":`), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := Load(dir)
	if err == nil || !strings.Contains(err.Error(), "library manifest") {
		t.Fatalf("Load() error = %v, want contextual library manifest error", err)
	}
}

func TestManifestSaveProducesReadableRoundTrip(t *testing.T) {
	dir := t.TempDir()
	want := &Manifest{
		Version:       1,
		StyleFamilies: map[string]StyleFamily{"line": {PromptPrefix: "single line"}},
		Assets:        []Asset{{ID: "diagram", File: "img/diagram.png", Model: "gpt-image-2"}},
		Videos:        []VideoAsset{{ID: "demo", File: "video/demo.mp4", Hash: "123", Bytes: 99}},
	}
	if err := want.Save(dir); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "\n  \"version\"") {
		t.Fatalf("manifest is not indented JSON:\n%s", raw)
	}

	got, err := Load(dir)
	if err != nil {
		t.Fatalf("Load() after Save() error = %v", err)
	}
	if got.Assets[0].ID != "diagram" || got.Videos[0].ID != "demo" || got.StyleFamilies["line"].PromptPrefix != "single line" {
		t.Fatalf("round trip = %#v, want saved catalog", got)
	}
}
