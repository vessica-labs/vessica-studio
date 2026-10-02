package cloud

import (
	"archive/zip"
	"bytes"
	"context"
	"github.com/vessica-labs/vessica-studio/internal/bundle"
	"net/http"
	"testing"
)

func TestEnsureBundleUsesSeparateVerifiedIdempotentTransport(t *testing.T) {
	var packed bytes.Buffer
	w := zip.NewWriter(&packed)
	f, _ := w.Create("index.html")
	f.Write([]byte("<p>Demo</p>"))
	w.Close()
	asset, e := bundle.Inspect(packed.Bytes(), "index.html")
	if e != nil {
		t.Fatal(e)
	}
	asset.ID = "demo"
	present := false
	puts := 0
	c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/capabilities" {
			writeJSON(w, `{"protocol":"1","capabilities":["asset.bundle.write"]}`)
			return
		}
		if r.URL.Path != "/v1/workspaces/selected/bundles/"+asset.Hash || r.Header.Get("Authorization") != "Bearer test-bundle-token" {
			t.Errorf("unscoped request %s", r.URL.Path)
			w.WriteHeader(404)
			return
		}
		if r.Method == "GET" {
			if present {
				writeJSON(w, `{"present":true}`)
			} else {
				writeJSON(w, `{"present":false}`)
			}
			return
		}
		if r.Method != "PUT" || r.Header.Get("Content-Type") != "application/vnd.vstd.bundle+zip" {
			t.Error("wrong upload contract")
		}
		puts++
		present = true
		writeJSON(w, `{"stored":true}`)
	}), WithTokenSource(TokenSourceFunc(func(context.Context) (string, error) { return "test-bundle-token", nil })))
	for i := 0; i < 2; i++ {
		if e = c.EnsureBundle(context.Background(), "selected", asset, packed.Bytes()); e != nil {
			t.Fatal(e)
		}
	}
	if puts != 1 {
		t.Fatalf("uploads=%d", puts)
	}
	if c.EnsureBundle(context.Background(), "selected", asset, []byte("corrupt")) == nil {
		t.Fatal("accepted corrupt archive")
	}
	if puts != 1 {
		t.Fatal("mutated after failed integrity")
	}
}
