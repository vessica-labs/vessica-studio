package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vessica-labs/vessica-studio/internal/cloud"
	"github.com/vessica-labs/vessica-studio/internal/cloudworkspace"
	"github.com/vessica-labs/vessica-studio/internal/studio"
)

func TestCloudPresentationOpenResolvesAllPagesAndReusesCheckout(t *testing.T) {
	snapshot := cloudPresentationFixture(t, "Source deck")
	mux := http.NewServeMux()
	jsonResponse := func(w http.ResponseWriter, value any) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(value)
	}
	mux.HandleFunc("/v1/capabilities", func(w http.ResponseWriter, _ *http.Request) {
		jsonResponse(w, cloud.Capabilities{Protocol: cloud.ProtocolVersion, Capabilities: []string{cloud.CapabilityWorkspaceRead, cloud.CapabilityWorkspaceSync}})
	})
	mux.HandleFunc("/v1/workspaces", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("cursor") == "next" {
			jsonResponse(w, cloud.WorkspaceList{Workspaces: []cloud.Workspace{{ID: "ws-target", Name: "Target presentation", HeadRevisionID: "rev-stale"}}})
			return
		}
		jsonResponse(w, cloud.WorkspaceList{Workspaces: []cloud.Workspace{{ID: "ws-other", Name: "Other presentation", HeadRevisionID: "rev-1"}}, NextCursor: "next"})
	})
	mux.HandleFunc("/v1/workspaces/ws-target", func(w http.ResponseWriter, _ *http.Request) {
		jsonResponse(w, cloud.Workspace{ID: "ws-target", Name: "Target presentation", HeadRevisionID: "rev-2"})
	})
	mux.HandleFunc("/v1/workspaces/ws-target/revisions/rev-2", func(w http.ResponseWriter, _ *http.Request) {
		jsonResponse(w, cloud.Revision{ID: "rev-2", WorkspaceID: "ws-target", Files: snapshot})
	})
	server := httptest.NewServer(mux)
	defer server.Close()
	client := cloudPresentationClient(t, server)
	target := filepath.Join(t.TempDir(), "target")
	for attempt := 0; attempt < 2; attempt++ {
		var out bytes.Buffer
		if err := runCloudPresentation(context.Background(), client, server.URL, []string{"open", "target", "--root", target, "--json"}, &out); err != nil {
			t.Fatalf("open attempt %d: %v", attempt, err)
		}
		var result cloudPresentationResult
		if err := json.Unmarshal(out.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if result.ID != "ws-target" || result.Root != target || result.Revision != "rev-2" || result.Created {
			t.Fatalf("result = %#v", result)
		}
	}
	association, err := cloudworkspace.LoadAssociation(target)
	if err != nil || association.WorkspaceID != "ws-target" {
		t.Fatalf("association = %#v, %v", association, err)
	}
}

func TestCloudPresentationCreateUsesManagedCheckoutThatOpenReuses(t *testing.T) {
	config := t.TempDir()
	oldConfigDir := cloudPresentationConfigDir
	cloudPresentationConfigDir = func() (string, error) { return config, nil }
	t.Cleanup(func() { cloudPresentationConfigDir = oldConfigDir })
	var createdFiles []cloud.File
	mux := http.NewServeMux()
	jsonResponse := func(w http.ResponseWriter, value any) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(value)
	}
	mux.HandleFunc("/v1/capabilities", func(w http.ResponseWriter, _ *http.Request) {
		jsonResponse(w, cloud.Capabilities{Protocol: cloud.ProtocolVersion, Capabilities: []string{cloud.CapabilityWorkspaceCreate, cloud.CapabilityWorkspaceRead, cloud.CapabilityWorkspaceSync}})
	})
	mux.HandleFunc("/v1/account", func(w http.ResponseWriter, _ *http.Request) {
		jsonResponse(w, cloud.Account{ID: "account-1", Email: "person@example.com"})
	})
	mux.HandleFunc("/v1/workspaces", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			var input cloud.CreateWorkspaceRequest
			if json.NewDecoder(r.Body).Decode(&input) != nil || input.Title != "New client story" || len(input.Files) == 0 {
				http.Error(w, "invalid", http.StatusBadRequest)
				return
			}
			createdFiles = input.Files
			jsonResponse(w, cloud.Revision{ID: "rev-created", WorkspaceID: "created-1"})
			return
		}
		jsonResponse(w, cloud.WorkspaceList{Workspaces: []cloud.Workspace{{ID: "created-1", Name: "New client story", HeadRevisionID: "rev-created"}}})
	})
	mux.HandleFunc("/v1/workspaces/created-1", func(w http.ResponseWriter, _ *http.Request) {
		jsonResponse(w, cloud.Workspace{ID: "created-1", Name: "New client story", HeadRevisionID: "rev-created"})
	})
	mux.HandleFunc("/v1/workspaces/created-1/revisions/rev-created", func(w http.ResponseWriter, _ *http.Request) {
		jsonResponse(w, cloud.Revision{ID: "rev-created", WorkspaceID: "created-1", Files: createdFiles})
	})
	server := httptest.NewServer(mux)
	defer server.Close()
	client := cloudPresentationClient(t, server)
	var createOut bytes.Buffer
	if err := runCloudPresentation(context.Background(), client, server.URL, []string{"create", "--title", "New client story", "--json"}, &createOut); err != nil {
		t.Fatal(err)
	}
	var created cloudPresentationResult
	if err := json.Unmarshal(createOut.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if !created.Created || created.ID != "created-1" || !strings.HasPrefix(created.Root, config+string(os.PathSeparator)) {
		t.Fatalf("created = %#v", created)
	}
	st, err := studio.Open(created.Root)
	if err != nil {
		t.Fatal(err)
	}
	decks, err := st.ListDecks()
	if err != nil || len(decks) != 1 || decks[0] != "new-client-story" {
		t.Fatalf("decks = %v, %v", decks, err)
	}
	var openOut bytes.Buffer
	if err := runCloudPresentation(context.Background(), client, server.URL, []string{"open", "New client story", "--json"}, &openOut); err != nil {
		t.Fatal(err)
	}
	var opened cloudPresentationResult
	if json.Unmarshal(openOut.Bytes(), &opened) != nil || opened.Root != created.Root || opened.Created {
		t.Fatalf("opened = %#v", opened)
	}
}

func TestSelectCloudPresentationRejectsAmbiguousAndMissingTitles(t *testing.T) {
	presentations := []cloud.Workspace{{ID: "one", Name: "Client strategy"}, {ID: "two", Name: "Client strategy"}, {ID: "three", Name: "one"}}
	selected, err := selectCloudPresentation("one", presentations)
	if err != nil || selected.ID != "one" {
		t.Fatalf("ID selection = %#v, %v", selected, err)
	}
	if _, err := selectCloudPresentation("Client strategy", presentations); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("ambiguous error = %v", err)
	}
	if _, err := selectCloudPresentation("unknown", presentations); err == nil || !strings.Contains(err.Error(), "no editable") {
		t.Fatalf("missing error = %v", err)
	}
}

func TestCloudPresentationOpenRejectsKnownViewOnlyPresentationBeforeWriting(t *testing.T) {
	canEdit := false
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/capabilities", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(cloud.Capabilities{Protocol: cloud.ProtocolVersion, Capabilities: []string{cloud.CapabilityWorkspaceRead}})
	})
	mux.HandleFunc("/v1/workspaces", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(cloud.WorkspaceList{Workspaces: []cloud.Workspace{{ID: "shared", Name: "Shared deck", CanEdit: &canEdit}}})
	})
	server := httptest.NewServer(mux)
	defer server.Close()
	root := filepath.Join(t.TempDir(), "must-not-exist")
	err := runCloudPresentation(context.Background(), cloudPresentationClient(t, server), server.URL, []string{"open", "Shared deck", "--root", root}, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "view-only") {
		t.Fatalf("error = %v", err)
	}
	if _, statErr := os.Stat(root); !os.IsNotExist(statErr) {
		t.Fatalf("root should not be created, stat error = %v", statErr)
	}
}

func TestCloudPresentationCreateRejectsLongTitleBeforeWriting(t *testing.T) {
	root := filepath.Join(t.TempDir(), "must-not-exist")
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()
	client := cloudPresentationClient(t, server)
	err := runCloudPresentation(context.Background(), client, "https://studio.example", []string{"create", "--title", strings.Repeat("x", 201), "--root", root}, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "1–200 bytes") {
		t.Fatalf("error = %v", err)
	}
	if _, statErr := os.Stat(root); !os.IsNotExist(statErr) {
		t.Fatalf("root should not be created, stat error = %v", statErr)
	}
}

func TestAvailableManagedPresentationRootSkipsAbandonedDirectory(t *testing.T) {
	base := t.TempDir()
	if err := os.Mkdir(filepath.Join(base, "client-story-id"), 0700); err != nil {
		t.Fatal(err)
	}
	root, err := availableManagedPresentationRoot(base, "client-story-id")
	if err != nil || root != filepath.Join(base, "client-story-id-2") {
		t.Fatalf("root = %q, error = %v", root, err)
	}
}

func cloudPresentationFixture(t *testing.T, title string) []cloud.File {
	t.Helper()
	root := filepath.Join(t.TempDir(), "source")
	if err := studio.Init(root); err != nil {
		t.Fatal(err)
	}
	st, err := studio.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.NewDeck("source", title); err != nil {
		t.Fatal(err)
	}
	snapshot, err := studio.CloudContent(root)
	if err != nil {
		t.Fatal(err)
	}
	files := make([]cloud.File, len(snapshot.Files))
	for i, file := range snapshot.Files {
		files[i] = cloud.File{Path: file.Path, Content: file.Content, Mode: file.Mode}
	}
	return files
}

func cloudPresentationClient(t *testing.T, server *httptest.Server) *cloud.Client {
	t.Helper()
	client, err := cloud.NewClient(
		cloud.WithEndpoint(server.URL),
		cloud.WithHTTPClient(server.Client()),
		cloud.WithClientVersion(version),
		cloud.WithTokenSource(cloud.TokenSourceFunc(func(context.Context) (string, error) { return "access-token", nil })),
	)
	if err != nil {
		t.Fatal(err)
	}
	return client
}
