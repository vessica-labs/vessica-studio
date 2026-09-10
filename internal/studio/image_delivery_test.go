package studio

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"image"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestImageDeliveryPreservesSourceAndMatchesRelease(t *testing.T) {
	if _, err := exec.LookPath("cwebp"); err != nil {
		t.Skip("cwebp unavailable")
	}
	root := t.TempDir()
	source, output := filepath.Join(root, "original.png"), filepath.Join(root, "display.webp")
	var raw bytes.Buffer
	if err := png.Encode(&raw, image.NewNRGBA(image.Rect(0, 0, 32, 24))); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, raw.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	result, err := BuildImageDelivery(source, output)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := os.ReadFile(output)
	original, _ := os.ReadFile(source)
	if !bytes.Equal(original, raw.Bytes()) || result.SourceSHA256 != fmt.Sprintf("%x", sha256.Sum256(original)) || result.SHA256 != fmt.Sprintf("%x", sha256.Sum256(encoded)) || result.Bytes != int64(len(encoded)) || result.Width != 32 || result.Height != 24 || result.Recipe != "webp-v1-1920" {
		t.Fatalf("invalid derivative: %+v", result)
	}
	decoded, _, err := image.Decode(bytes.NewReader(encoded))
	if err != nil {
		t.Fatal(err)
	}
	_, _, _, alpha := decoded.At(0, 0).RGBA()
	if alpha != 0 {
		t.Fatal("lost transparency")
	}
	release := filepath.Join(root, "release.webp")
	if err := encodeReleaseWebP(source, release, 1920); err != nil {
		t.Fatal(err)
	}
	expected, _ := os.ReadFile(release)
	if !bytes.Equal(encoded, expected) {
		t.Fatal("editor and release recipes differ")
	}
	if _, err := BuildImageDelivery(source, output); err == nil {
		t.Fatal("overwrote existing output")
	}
	if _, err := BuildImageDelivery(source, source); err == nil {
		t.Fatal("overwrote source")
	}
}
func TestImageDeliveryRejectsInvalidInput(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "invalid.png")
	if err := os.WriteFile(source, []byte("invalid"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := BuildImageDelivery(source, filepath.Join(root, "out.webp")); err == nil {
		t.Fatal("accepted invalid image")
	}
}
