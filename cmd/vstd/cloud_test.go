package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vessica-labs/vessica-studio/internal/cloud"
	"github.com/vessica-labs/vessica-studio/internal/cloudauth"
	"github.com/vessica-labs/vessica-studio/internal/studio"
)

func TestCloudUsage(t *testing.T) {
	var out bytes.Buffer
	err := runCloud(nil, &out)
	if err == nil || !strings.Contains(err.Error(), "vstd cloud") {
		t.Fatalf("runCloud() error = %v", err)
	}
}

func TestCloudWorkspaceSyncPublishAndAccount(t *testing.T) {
	const secret = "sentinel-refresh-secret"
	mux := http.NewServeMux()
	head := "rev-1"
	jsonResponse := func(w http.ResponseWriter, value any) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(value)
	}
	mux.HandleFunc("/v1/auth/device", func(w http.ResponseWriter, _ *http.Request) {
		jsonResponse(w, cloud.DeviceAuthorization{DeviceCode: "device-secret", UserCode: "ABCD", VerificationURI: "https://example.invalid/activate", ExpiresIn: 60})
	})
	mux.HandleFunc("/v1/auth/token", func(w http.ResponseWriter, _ *http.Request) {
		jsonResponse(w, cloud.Token{AccessToken: "sentinel-access-secret", RefreshToken: secret, ExpiresIn: 60})
	})
	mux.HandleFunc("/v1/account", func(w http.ResponseWriter, _ *http.Request) {
		jsonResponse(w, cloud.Account{ID: "acct-1", Email: "user@example.com"})
	})
	mux.HandleFunc("/v1/workspaces/ws-1", func(w http.ResponseWriter, _ *http.Request) {
		jsonResponse(w, cloud.Workspace{ID: "ws-1", Name: "Demo", HeadRevisionID: head})
	})
	mux.HandleFunc("/v1/capabilities", func(w http.ResponseWriter, _ *http.Request) {
		jsonResponse(w, cloud.Capabilities{Protocol: cloud.ProtocolVersion, Capabilities: []string{cloud.CapabilityWorkspaceRead, cloud.CapabilityWorkspaceSync, cloud.CapabilityPublicationRead, cloud.CapabilityPublicationWrite}})
	})
	mux.HandleFunc("/v1/workspaces/ws-1/revisions", func(w http.ResponseWriter, _ *http.Request) {
		head = "rev-2"
		jsonResponse(w, cloud.Revision{ID: "rev-2", WorkspaceID: "ws-1"})
	})
	mux.HandleFunc("/v1/workspaces/ws-1/publications", func(w http.ResponseWriter, _ *http.Request) {
		jsonResponse(w, cloud.Publication{ID: "pub-1", WorkspaceID: "ws-1", RevisionID: "rev-2", Status: "published", URL: "https://example.invalid/p/demo"})
	})
	server := httptest.NewServer(mux)
	defer server.Close()
	t.Setenv("VSTD_CLOUD_ENDPOINT", server.URL)
	store := cloudauth.NewMemoryStore()
	oldStore, oldHTTP := cloudCredentialStore, cloudHTTPClient
	cloudCredentialStore = func(string) cloudauth.Store { return store }
	cloudHTTPClient = func() *http.Client { return server.Client() }
	defer func() { cloudCredentialStore, cloudHTTPClient = oldStore, oldHTTP }()

	root := filepath.Join(t.TempDir(), "studio")
	if err := studio.Init(root); err != nil {
		t.Fatal(err)
	}
	snapshot, err := studio.CloudContent(root)
	if err != nil {
		t.Fatal(err)
	}
	mux.HandleFunc("/v1/workspaces/ws-1/revisions/rev-1", func(w http.ResponseWriter, _ *http.Request) {
		files := make([]cloud.File, len(snapshot.Files))
		for i, f := range snapshot.Files {
			files[i] = cloud.File{Path: f.Path, Content: f.Content, Mode: f.Mode}
		}
		jsonResponse(w, cloud.Revision{ID: "rev-1", Files: files})
	})
	var out bytes.Buffer
	for _, args := range [][]string{{"login"}, {"account"}, {"workspace", "connect", "ws-1", "--root", root}, {"workspace", "sync", "--root", root}, {"publish", "create", "--root", root}} {
		if err := runCloud(args, &out); err != nil {
			t.Fatalf("runCloud(%v): %v", args, err)
		}
	}
	got := out.String()
	if !strings.Contains(got, "Synchronized revision rev-2") || !strings.Contains(got, "publication: pub-1") || !strings.Contains(got, "acct-1") {
		t.Fatalf("unexpected output: %s", got)
	}
	if strings.Contains(got, secret) || strings.Contains(got, "sentinel-access-secret") || strings.Contains(got, "device-secret") {
		t.Fatalf("output leaked secret: %s", got)
	}
}

func TestCloudProtocolRedaction(t *testing.T) {
	got := cloudErrorText("https://cloud.example.invalid", "sentinel-secret", "operation failed: sentinel-secret")
	if strings.Contains(got, "sentinel-secret") {
		t.Fatalf("cloudErrorText leaked a credential: %q", got)
	}
}
