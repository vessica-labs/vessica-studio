package studio

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/vessica-labs/vessica-studio/internal/library"
)

func TestBuildReleaseIsDeterministicAndSelfContained(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "studio.yaml"), "theme_default: default\n")
	writeFile(t, filepath.Join(root, "themes", "default", "theme.css"), ".slide{background:#fff}")
	writeFile(t, filepath.Join(root, "decks", "demo", "deck.yaml"), "title: Demo\ntheme: default\n")
	writeFile(t, filepath.Join(root, "decks", "demo", "slides", "0010-a.html"),
		`<section class="slide" style="background:url(/library/img/example.png)"><img src="/library/img/example.png" alt="Example"></section>`)
	writeFile(t, filepath.Join(root, "decks", "demo", "slides", "0010-a.md"), "# Private companion\n")
	writeFile(t, filepath.Join(root, "library", "img", "example.png"), "image-bytes")

	studio, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	identity := ReleaseEngineIdentity{
		Name: "vstd", Version: "0.4.0", Revision: strings.Repeat("a", 40),
	}
	firstDir := filepath.Join(t.TempDir(), "first")
	secondDir := filepath.Join(t.TempDir(), "second")
	first, err := studio.BuildRelease("demo", firstDir, identity)
	if err != nil {
		t.Fatal(err)
	}
	second, err := studio.BuildRelease("demo", secondDir, identity)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("release manifests differ:\nfirst=%+v\nsecond=%+v", first, second)
	}
	if first.Entrypoint != "index.html" || first.Theme != "default" {
		t.Fatalf("unexpected release identity: %+v", first)
	}
	if !reflect.DeepEqual(first.Assets, []string{"library/img/example.png"}) {
		t.Fatalf("assets = %v", first.Assets)
	}
	if len(first.Artifacts) != 2 {
		t.Fatalf("artifacts = %v", first.Artifacts)
	}
	firstHTML, err := os.ReadFile(filepath.Join(firstDir, "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(firstHTML, []byte(`src="./library/img/example.png"`)) {
		t.Fatal("release HTML did not scope the library URL to the release")
	}
	if !bytes.Contains(firstHTML, []byte(`url(./library/img/example.png)`)) {
		t.Fatal("release HTML did not scope an unquoted library URL to the release")
	}
	info, err := os.Stat(firstDir)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("release directory mode = %v; want 0755", info.Mode().Perm())
	}
	if bytes.Contains(firstHTML, []byte("Private companion")) {
		t.Fatal("release leaked companion content")
	}
	if got, err := os.ReadFile(filepath.Join(firstDir, "library", "img", "example.png")); err != nil || string(got) != "image-bytes" {
		t.Fatalf("copied image = %q, %v", got, err)
	}
	raw, err := os.ReadFile(filepath.Join(firstDir, "release-manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var parsed ReleaseManifest
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed.ManifestChecksum != first.ManifestChecksum {
		t.Fatalf("persisted checksum = %q, want %q", parsed.ManifestChecksum, first.ManifestChecksum)
	}
	unsigned := unsignedReleaseManifest{
		SchemaVersion: parsed.SchemaVersion,
		Engine:        parsed.Engine,
		Entrypoint:    parsed.Entrypoint,
		Theme:         parsed.Theme,
		Assets:        parsed.Assets,
		Artifacts:     parsed.Artifacts,
	}
	checksum, err := canonicalChecksum(unsigned)
	if err != nil {
		t.Fatal(err)
	}
	if checksum != parsed.ManifestChecksum {
		t.Fatalf("manifest checksum = %q, recomputed %q", parsed.ManifestChecksum, checksum)
	}
}

func TestBuildReleaseRejectsVideoIntegrityAndUnsupportedContainers(t *testing.T) {
	root := releaseVideoStudio(t)
	studio, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	identity := ReleaseEngineIdentity{Name: "vstd", Version: "0.4.0", Revision: strings.Repeat("c", 40)}
	manifest, err := library.Load(filepath.Join(root, "library"))
	if err != nil {
		t.Fatal(err)
	}
	manifest.Videos[0].Hash = strings.Repeat("0", 64)
	if err := manifest.Save(filepath.Join(root, "library")); err != nil {
		t.Fatal(err)
	}
	if _, err := studio.BuildRelease("demo", filepath.Join(t.TempDir(), "corrupt"), identity); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("corrupt video error = %v", err)
	}

	manifest.Videos[0].Hash = videoDigest([]byte("video-bytes"))
	manifest.Videos[0].File = "video/clip.webm"
	writeFile(t, filepath.Join(root, "library", "video", "clip.webm"), "video-bytes")
	if err := manifest.Save(filepath.Join(root, "library")); err != nil {
		t.Fatal(err)
	}
	if _, err := studio.BuildRelease("demo", filepath.Join(t.TempDir(), "webm"), identity); err == nil || !strings.Contains(err.Error(), "unsupported non-MP4") {
		t.Fatalf("non-MP4 video error = %v", err)
	}
}

func TestBuildReleaseRejectsOutputThroughSymlinkedAncestor(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink behavior differs on Windows")
	}
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "studio.yaml"), "theme_default: default\n")
	writeFile(t, filepath.Join(root, "themes", "default", "theme.css"), ".slide{}")
	writeFile(t, filepath.Join(root, "decks", "demo", "deck.yaml"), "title: Demo\ntheme: default\n")
	writeFile(t, filepath.Join(root, "decks", "demo", "slides", "0010-a.html"), `<section class="slide"></section>`)
	studio, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "studio-link")
	if err := os.Symlink(root, link); err != nil {
		t.Fatal(err)
	}
	identity := ReleaseEngineIdentity{Name: "vstd", Version: "0.4.0", Revision: strings.Repeat("d", 40)}
	if _, err := studio.BuildRelease("demo", filepath.Join(link, "generated", "release"), identity); err == nil || !strings.Contains(err.Error(), "outside") {
		t.Fatalf("symlinked output error = %v", err)
	}
}

func releaseVideoStudio(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "studio.yaml"), "theme_default: default\n")
	writeFile(t, filepath.Join(root, "themes", "default", "theme.css"), ".slide{}")
	writeFile(t, filepath.Join(root, "decks", "demo", "deck.yaml"), "title: Demo\ntheme: default\n")
	writeFile(t, filepath.Join(root, "decks", "demo", "slides", "0010-a.html"), `<section class="slide"><video data-vstd-video="clip"></video></section>`)
	contents := []byte("video-bytes")
	writeFile(t, filepath.Join(root, "library", "video", "clip.mp4"), string(contents))
	manifest := &library.Manifest{
		Version:       1,
		StyleFamilies: map[string]library.StyleFamily{},
		Videos: []library.VideoAsset{{
			ID: "clip", File: "video/clip.mp4", Hash: videoDigest(contents), Bytes: int64(len(contents)),
		}},
	}
	if err := manifest.Save(filepath.Join(root, "library")); err != nil {
		t.Fatal(err)
	}
	return root
}

func videoDigest(contents []byte) string {
	digest := sha256.Sum256(contents)
	return hex.EncodeToString(digest[:])
}

func TestBuildReleaseRejectsUnsafeOutputAndMissingAssets(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "studio.yaml"), "theme_default: default\n")
	writeFile(t, filepath.Join(root, "themes", "default", "theme.css"), ".slide{}")
	writeFile(t, filepath.Join(root, "decks", "demo", "deck.yaml"), "title: Demo\ntheme: default\n")
	writeFile(t, filepath.Join(root, "decks", "demo", "slides", "0010-a.html"),
		`<section class="slide"><img src="/library/img/missing.png"></section>`)
	studio, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	identity := ReleaseEngineIdentity{Name: "vstd", Version: "0.4.0", Revision: strings.Repeat("b", 40)}
	if _, err := studio.BuildRelease("demo", filepath.Join(root, "release"), identity); err == nil {
		t.Fatal("release output inside studio root unexpectedly accepted")
	}
	out := filepath.Join(t.TempDir(), "release")
	if _, err := studio.BuildRelease("demo", out, identity); err == nil || !strings.Contains(err.Error(), "unavailable") {
		t.Fatalf("missing asset error = %v", err)
	}
}
