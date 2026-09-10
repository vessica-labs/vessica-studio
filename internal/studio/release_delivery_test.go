package studio

import (
	"bytes"
	"image"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestDeliveryTemplateScopesStaticAndDynamicAssets(t *testing.T) {
	digest := strings.Repeat("a", 64)
	html := `<img src="./library/img/a.png"><div style="background:url(./library/img/a.png)"></div><script>const srcFor=id=>'./assets/video/'+id+'.mp4';const posterFor=id=>'./assets/video-posters/'+id+'.jpg';</script>`
	rendered, err := releaseDeliveryTemplate(html, []ReleaseArtifact{
		{Path: "library/img/a.png", SHA256: digest}, {Path: "assets/video/demo.mp4", SHA256: strings.Repeat("b", 64)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(rendered, `./library/img/a.png`) || strings.Contains(rendered, `=>'./assets/video/'`) {
		t.Fatal("local media route retained", rendered)
	}
	if !strings.Contains(rendered, `vstd-asset:`+digest) || !strings.Contains(rendered, `"demo":"vstd-asset:`) {
		t.Fatal("missing typed asset placeholders", rendered)
	}
}

func TestSanitizeDeliverySVG(t *testing.T) {
	for _, source := range []string{
		`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`,
		`<svg xmlns="http://www.w3.org/2000/svg"><image href="https://evil.test/track"/></svg>`,
		`<svg xmlns="http://www.w3.org/2000/svg" onload="alert(1)"></svg>`,
		`<!DOCTYPE svg [<!ENTITY x SYSTEM "file:///etc/passwd">]><svg>&x;</svg>`,
	} {
		if _, err := sanitizeDeliverySVG([]byte(source)); err == nil {
			t.Fatal("unsafe SVG accepted", source)
		}
	}
	clean, err := sanitizeDeliverySVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 10 10"><metadata>private</metadata><path d="M0 0L10 10"/></svg>`))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(clean), "private") || !strings.Contains(string(clean), "path") {
		t.Fatal(string(clean))
	}
}

func TestOptimizedReleasePreservesOriginalAndTransparency(t *testing.T) {
	if _, err := exec.LookPath("cwebp"); err != nil {
		t.Skip("cwebp unavailable")
	}
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "studio.yaml"), "theme_default: default\n")
	writeFile(t, filepath.Join(root, "themes/default/theme.css"), ".slide{background:#fff}")
	writeFile(t, filepath.Join(root, "decks/demo/deck.yaml"), "title: Demo\ntheme: default\n")
	writeFile(t, filepath.Join(root, "decks/demo/slides/0010-a.html"), `<section class="slide"><img src="/library/img/example.png"></section>`)
	writeFile(t, filepath.Join(root, "decks/demo/slides/0010-a.md"), "# Source\n")
	imagePath := filepath.Join(root, "library/img/example.png")
	if err := os.MkdirAll(filepath.Dir(imagePath), 0o755); err != nil {
		t.Fatal(err)
	}
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, image.NewNRGBA(image.Rect(0, 0, 24, 18))); err != nil {
		t.Fatal(err)
	}
	original := buffer.Bytes()
	if err := os.WriteFile(imagePath, original, 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(t.TempDir(), "release")
	manifest, err := st.BuildReleaseWithOptions("demo", output, ReleaseEngineIdentity{Name: "vstd", Version: "test", Revision: strings.Repeat("a", 40)}, ReleaseOptions{Optimize: true, DeliveryTemplate: true})
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Delivery == nil || !manifest.Delivery.Template {
		t.Fatal("missing settings")
	}
	for _, artifact := range manifest.Artifacts {
		if artifact.MediaType == "image/webp" {
			encoded, err := os.Open(filepath.Join(output, artifact.Path))
			if err != nil {
				t.Fatal(err)
			}
			decoded, _, err := image.Decode(encoded)
			_ = encoded.Close()
			if err != nil {
				t.Fatal(err)
			}
			_, _, _, alpha := decoded.At(0, 0).RGBA()
			if alpha != 0 {
				t.Fatal("transparency lost")
			}
		}
		if artifact.MediaType == "image/webp" && (artifact.Width != 24 || artifact.Height != 18) {
			t.Fatalf("image enlarged: %+v", artifact)
		}
	}
	after, _ := os.ReadFile(imagePath)
	if !bytes.Equal(original, after) {
		t.Fatal("editable original changed")
	}
	html, _ := os.ReadFile(filepath.Join(output, "index.html"))
	if !bytes.Contains(html, []byte("vstd-asset:")) {
		t.Fatal("missing delivery URLs")
	}
	second := filepath.Join(t.TempDir(), "release")
	again, err := st.BuildReleaseWithOptions("demo", second, ReleaseEngineIdentity{Name: "vstd", Version: "test", Revision: strings.Repeat("a", 40)}, ReleaseOptions{Optimize: true, DeliveryTemplate: true})
	if err != nil {
		t.Fatal(err)
	}
	if manifest.ManifestChecksum != again.ManifestChecksum {
		t.Fatal("optimized release is nondeterministic")
	}
}

func TestOptimizedVideoIsCappedFastStartWithWebPPoster(t *testing.T) {
	for _, tool := range []string{"ffmpeg", "ffprobe", "cwebp"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip(tool + " unavailable")
		}
	}
	root := t.TempDir()
	path := "assets/video/demo.mp4"
	source := filepath.Join(root, path)
	if err := os.MkdirAll(filepath.Dir(source), 0o755); err != nil {
		t.Fatal(err)
	}
	if raw, err := exec.Command("ffmpeg", "-nostdin", "-v", "error", "-y", "-f", "lavfi", "-i", "color=c=blue:s=2560x1440:d=0.1", "-c:v", "libx264", "-threads", "1", source).CombinedOutput(); err != nil {
		t.Fatalf("video fixture: %s %v", raw, err)
	}
	assets := map[string]struct{}{path: {}}
	if _, err := optimizeReleaseDelivery(root, `const posterFor=id=>'./assets/video-posters/'+id+'.jpg';`, assets); err != nil {
		t.Fatal(err)
	}
	raw, err := exec.Command("ffprobe", "-v", "error", "-select_streams", "v:0", "-show_entries", "stream=width,height,codec_name", "-of", "csv=p=0", source).Output()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "h264,1920,1080") {
		t.Fatalf("unexpected video: %s", raw)
	}
	data, _ := os.ReadFile(source)
	moov, mdat := bytes.Index(data, []byte("moov")), bytes.Index(data, []byte("mdat"))
	if moov < 0 || mdat < 0 || moov > mdat {
		t.Fatal("video is not fast-start")
	}
	if _, ok := assets["assets/video-posters/demo.webp"]; !ok {
		t.Fatal("missing poster")
	}
}

func TestPlatformResourcesAreOnlyCompiledInCSS(t *testing.T) {
	root := t.TempDir()
	resources, err := PlatformDeliveryResources()
	if err != nil {
		t.Fatal(err)
	}
	if len(resources) < 2 {
		t.Fatal("missing engine and theme resources")
	}
	output := filepath.Join(root, "trusted")
	if err = ExportPlatformDeliveryResources(output); err != nil {
		t.Fatal(err)
	}
	for path, expected := range resources {
		actual, err := os.ReadFile(filepath.Join(output, filepath.Base(path)))
		if err != nil || !bytes.Equal(actual, expected) {
			t.Fatal("resource mismatch")
		}
	}
	assets := map[string]struct{}{}
	html := `<style>body{background:url(/library/private.png)}</style>`
	result, err := extractPlatformDelivery(root, html, assets)
	if err != nil || result != html || len(assets) != 0 {
		t.Fatal("private CSS was extracted")
	}
}
