package studio

import (
	"encoding/base64"
	"encoding/json"
	"github.com/vessica-labs/vessica-studio/internal/library"
	"os"
	"path/filepath"
	"strings"
)

// wrapBundleRelease keeps executable presentation content in its existing
// opaque-origin sandbox. Only the engine-authored shell receives CORS authority
// to fetch release-scoped archives. The host recognizes its verified variant.
func (s *Studio) wrapBundleRelease(root, html string, deliveryTemplate bool) (bool, error) {
	m, err := library.Load(filepath.Join(s.Root, "library"))
	if err != nil {
		return false, err
	}
	assets := map[string]any{}
	for _, a := range m.Bundles {
		if !strings.Contains(html, `data-vstd-bundle="`+a.ID+`"`) && !strings.Contains(html, `data-vstd-bundle='`+a.ID+`'`) {
			continue
		}
		file := "assets/bundle/" + a.ID + ".zip"
		u := "./" + file
		if deliveryTemplate {
			u = "vstd-asset:" + a.Hash + ":" + base64.RawURLEncoding.EncodeToString([]byte(file))
		}
		assets[a.ID] = map[string]any{"url": u, "bytes": a.Bytes, "hash": a.Hash}
	}
	if len(assets) == 0 {
		return false, nil
	}
	script, err := templates.ReadFile("templates/bundle-relay.js")
	if err != nil {
		return false, err
	}
	download, err := templates.ReadFile("templates/bundle-download.js")
	if err != nil {
		return false, err
	}
	script = append(append(download, '\n'), script...)
	metadata, _ := json.Marshal(assets)
	html = strings.Replace(html, "<script>", "<script>window.VSTDBundleRelay=true;</script><script>", 1)
	if err = writeReleaseFile(root, "presentation.html", []byte(html)); err != nil {
		return false, err
	}
	shell := `<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Presentation</title><style>html,body,iframe{margin:0;width:100%;height:100%;border:0;overflow:hidden;background:#111}</style></head><body><iframe id="deck" src="./presentation.html" sandbox="allow-scripts" title="Presentation" allow="fullscreen"></iframe><script>` + strings.Replace(string(script), "/*VSTD:RELAY_ASSETS*/{}", string(metadata), 1) + `</script></body></html>`
	return true, os.WriteFile(filepath.Join(root, "index.html"), []byte(shell), 0644)
}
