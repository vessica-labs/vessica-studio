package cloud

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestIncrementalSyncSkipsUnchangedAssetAndRetainsServerEdits(t *testing.T) {
	asset := File{Path: "library/image/large.png", Content: []byte(strings.Repeat("a", 8<<20))}
	input := []File{asset, {Path: "slide.md", Content: []byte("local edit")}, {Path: "empty.md", Content: []byte{}}}
	var uploaded int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/capabilities":
			writeJSON(w, `{"protocol":"1","capabilities":["workspace.sync","workspace.sync.incremental"]}`)
		case "/v1/workspaces/ws/revisions/base/manifest":
			refs := transferFiles([]File{asset, {Path: "old.md", Content: []byte("delete")}}, nil)
			for i := range refs {
				refs[i].Content = nil
			}
			json.NewEncoder(w).Encode(transferRevision{ID: "base", WorkspaceID: "ws", Files: refs})
		case "/v1/workspaces/ws/revisions/incremental":
			var body incrementalSync
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
				return
			}
			b, _ := json.Marshal(body)
			uploaded = len(b)
			if len(body.Files) != 3 || body.Files[0].Content != nil || body.Files[1].Content == nil || body.Files[2].Content == nil {
				t.Errorf("incorrect transfer: %d files", len(body.Files))
			}
			if body.BaseRevisionID != "base" || body.OperationID != "op" {
				t.Error("lost reconciliation/replay identity")
			}
			// Canonical result can contain another author's server-side edit.
			known := transferFiles(input, nil)
			result := append(append([]File{}, input...), File{Path: "server.md", Content: []byte("online edit")})
			json.NewEncoder(w).Encode(transferRevision{ID: "result", WorkspaceID: "ws", Files: transferFiles(result, known)})
		default:
			t.Errorf("unexpected full transfer %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	c, err := NewClient(WithEndpoint(server.URL), WithClientVersion("0.7.48"))
	if err != nil {
		t.Fatal(err)
	}
	out, err := c.Sync(context.Background(), "ws", SyncRequest{BaseRevisionID: "base", Files: input, OperationID: "op"})
	if err != nil {
		t.Fatal(err)
	}
	if uploaded > 2048 {
		t.Fatalf("unchanged asset sent: %d bytes", uploaded)
	}
	if len(out.Files) != 4 || string(out.Files[3].Content) != "online edit" || len(out.Files[0].Content) != 8<<20 {
		t.Fatal("lost local asset or server edits")
	}
}
func TestTransferIntegrityAndEmptyFile(t *testing.T) {
	f := File{Path: "empty", Content: []byte{}}
	refs := transferFiles([]File{f}, nil)
	if refs[0].Content == nil {
		t.Fatal("empty file became a reference")
	}
	r := transferRevision{ID: "r", WorkspaceID: "ws", Files: refs}
	if _, err := resolveTransfer(r, nil, "ws", "r"); err != nil {
		t.Fatal(err)
	}
	refs[0].Content = nil
	if _, err := resolveTransfer(r, nil, "ws", "r"); err == nil {
		t.Fatal("accepted missing reference")
	}
	refs[0].SHA256 = strings.Repeat("0", 64)
	if _, err := resolveTransfer(r, []File{f}, "ws", "r"); err == nil {
		t.Fatal("accepted corrupt reference")
	}
}
func TestIncrementalRevisionDownloadReusesCapturedBytes(t *testing.T) {
	known := []File{{Path: "image", Content: []byte(strings.Repeat("x", 8<<20))}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/capabilities" {
			writeJSON(w, `{"protocol":"1","capabilities":["workspace.sync.incremental"]}`)
			return
		}
		if r.URL.Path != "/v1/workspaces/ws/revisions/r/transfer" {
			t.Fatal("unexpected full download")
		}
		var body struct {
			Files []TransferFile `json:"files"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		if len(body.Files) != 1 || body.Files[0].Content != nil {
			t.Fatal("sent download cache bytes")
		}
		out := append(append([]File{}, known...), File{Path: "slide", Content: []byte("server change")})
		b, _ := json.Marshal(transferRevision{ID: "r", WorkspaceID: "ws", Files: transferFiles(out, body.Files)})
		if len(b) > 1024 {
			t.Fatal("downloaded unchanged asset")
		}
		w.Write(b)
	}))
	defer server.Close()
	c, _ := NewClient(WithEndpoint(server.URL), WithClientVersion("0.7.48"))
	out, err := c.RevisionCached(context.Background(), "ws", "r", known)
	if err != nil || len(out.Files) != 2 {
		t.Fatalf("%+v %v", out, err)
	}
}

// Run against the private adapter's external-client fixture without importing
// either implementation into the other repository.
func TestIncrementalExternalCloudContract(t *testing.T) {
	endpoint := os.Getenv("VSTD_INCREMENTAL_CONTRACT_ENDPOINT")
	if endpoint == "" {
		t.Skip("external Cloud contract endpoint not configured")
	}
	c, err := NewClient(WithEndpoint(endpoint), WithClientVersion("0.7.48"))
	if err != nil {
		t.Fatal(err)
	}
	files := []File{{Path: "image", Content: []byte(strings.Repeat("x", 8<<20)), Mode: 0644}, {Path: "slide", Content: []byte("local edit"), Mode: 0644}}
	out, err := c.Sync(context.Background(), "deck", SyncRequest{BaseRevisionID: "base", OperationID: "op", Files: files})
	if err != nil {
		t.Fatal(err)
	}
	if out.ID != "result" || len(out.Files) != 3 || string(out.Files[2].Content) != "online edit" {
		t.Fatal("lost reconciled result")
	}
	out, err = c.RevisionCached(context.Background(), "deck", "result", out.Files)
	if err != nil || len(out.Files) != 3 {
		t.Fatalf("cached transfer failed: %v", err)
	}
}

func TestIncrementalFallbackRetainsLegacyFullSnapshot(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/capabilities" {
			writeJSON(w, `{"protocol":"1","capabilities":["workspace.sync"]}`)
			return
		}
		if r.URL.Path != "/v1/workspaces/ws/revisions" {
			t.Error("used unadvertised incremental endpoint")
		}
		var body SyncRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		called = true
		if len(body.Files) != 1 || string(body.Files[0].Content) != "asset bytes" {
			t.Error("legacy asset omitted")
		}
		writeJSON(w, `{"id":"r","workspace_id":"ws"}`)
	}))
	defer server.Close()
	c, err := NewClient(WithEndpoint(server.URL), WithClientVersion("0.7.48"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Sync(context.Background(), "ws", SyncRequest{BaseRevisionID: "base", OperationID: "op", Files: []File{{Path: "image", Content: []byte("asset bytes")}}})
	if err != nil || !called {
		t.Fatalf("legacy fallback failed: %v", err)
	}
}

func TestSyncManifestDoesNotDownloadCloudOnlyMedia(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/capabilities":
			writeJSON(w, `{"protocol":"1","capabilities":["workspace.sync.incremental"]}`)
		case "/v1/workspaces/ws/revisions/base/manifest":
			json.NewEncoder(w).Encode(transferRevision{ID: "base", WorkspaceID: "ws"})
		case "/v1/workspaces/ws/revisions/incremental":
			var body struct {
				ResponseMode string         `json:"response_mode"`
				Files        []TransferFile `json:"files"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if body.ResponseMode != "manifest" || len(body.Files) != 1 || body.Files[0].Content == nil {
				t.Error("wrong manifest-only request")
			}
			json.NewEncoder(w).Encode(transferRevision{ID: "r", WorkspaceID: "ws", Files: []TransferFile{{Path: "Cloud-only.png", SHA256: strings.Repeat("a", 64)}}})
		default:
			t.Errorf("unexpected download %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	c, err := NewClient(WithEndpoint(server.URL), WithClientVersion("0.7.48"))
	if err != nil {
		t.Fatal(err)
	}
	out, err := c.SyncManifest(context.Background(), "ws", SyncRequest{BaseRevisionID: "base", Files: []File{{Path: "slide.md", Content: []byte("edit")}}, OperationID: "op"})
	if err != nil || out.ID != "r" || out.Files[0].Content != nil {
		t.Fatalf("%+v %v", out, err)
	}
}
