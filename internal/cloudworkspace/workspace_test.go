package cloudworkspace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/vessica-labs/vessica-studio/internal/cloud"
	"github.com/vessica-labs/vessica-studio/internal/studio"
)

type fakeCloud struct {
	ws      cloud.Workspace
	rev     cloud.Revision
	syncErr error
	synced  cloud.SyncRequest
}

func (f *fakeCloud) Workspace(context.Context, string) (cloud.Workspace, error) { return f.ws, nil }
func (f *fakeCloud) Revision(context.Context, string, string) (cloud.Revision, error) {
	return f.rev, nil
}
func (f *fakeCloud) Sync(_ context.Context, _ string, r cloud.SyncRequest) (cloud.Revision, error) {
	f.synced = r
	if f.syncErr != nil {
		return cloud.Revision{}, f.syncErr
	}
	return cloud.Revision{ID: "r2"}, nil
}

func localStudio(t *testing.T) string {
	t.Helper()
	r := t.TempDir()
	if err := studio.Init(r); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestConnectStatusSync(t *testing.T) {
	ctx := context.Background()
	root := localStudio(t)
	api := &fakeCloud{ws: cloud.Workspace{ID: "ws1", HeadRevisionID: "r1"}}
	m := Manager{Cloud: api, Endpoint: "https://cloud.example"}
	if err := m.Connect(ctx, root, "ws1"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "studio.yaml"), []byte("port: 4401\n"), 0644); err != nil {
		t.Fatal(err)
	}
	st, err := m.Status(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	if !st.Unsynced || st.BaseRevisionID != "r1" {
		t.Fatalf("bad status %#v", st)
	}
	if _, err = m.Sync(ctx, root, "edit"); err != nil {
		t.Fatal(err)
	}
	if api.synced.BaseRevisionID != "r1" || api.synced.OperationID == "" {
		t.Fatalf("bad sync %#v", api.synced)
	}
	st, err = m.Status(ctx, root)
	if err != nil || st.Unsynced || st.BaseRevisionID != "r2" {
		t.Fatalf("bad final status %#v %v", st, err)
	}
}

func TestConflictPreservesLocal(t *testing.T) {
	ctx := context.Background()
	root := localStudio(t)
	api := &fakeCloud{ws: cloud.Workspace{ID: "ws1", HeadRevisionID: "r1"}}
	m := Manager{Cloud: api, Endpoint: "https://cloud.example"}
	if err := m.Connect(ctx, root, "ws1"); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(root, "studio.yaml")
	before := []byte("port: 9999\n")
	os.WriteFile(p, before, 0644)
	api.syncErr = &cloud.ConflictError{CloudHeadRevisionID: "r3"}
	if _, err := m.Sync(ctx, root, "edit"); err == nil {
		t.Fatal("expected conflict")
	}
	got, _ := os.ReadFile(p)
	if string(got) != string(before) {
		t.Fatal("local content changed")
	}
	a, err := LoadAssociation(root)
	if err != nil || a.ConflictHeadRevisionID != "r3" {
		t.Fatalf("conflict not recorded %#v %v", a, err)
	}
}

func TestOfflineStatus(t *testing.T) {
	root := localStudio(t)
	api := &fakeCloud{ws: cloud.Workspace{ID: "ws1", HeadRevisionID: "r1"}}
	m := Manager{Cloud: api, Endpoint: "https://cloud.example"}
	if err := m.Connect(context.Background(), root, "ws1"); err != nil {
		t.Fatal(err)
	}
	api.ws = cloud.Workspace{}
	api.syncErr = &cloud.Error{Kind: cloud.ErrorOffline}
	// Status network failure is represented by an implementation whose Workspace fails.
	m.Cloud = offlineCloud{api}
	st, err := m.Status(context.Background(), root)
	if err != nil || !st.Offline {
		t.Fatalf("expected offline status %#v %v", st, err)
	}
}

type offlineCloud struct{ *fakeCloud }

func (offlineCloud) Workspace(context.Context, string) (cloud.Workspace, error) {
	return cloud.Workspace{}, &cloud.Error{Kind: cloud.ErrorOffline}
}

func TestClonePull(t *testing.T) {
	ctx := context.Background()
	target := filepath.Join(t.TempDir(), "clone")
	api := &fakeCloud{ws: cloud.Workspace{ID: "ws1", HeadRevisionID: "r1"}, rev: cloud.Revision{ID: "r1", Files: []cloud.File{{Path: "studio.yaml", Content: []byte("port: 4400\n")}}}}
	m := Manager{Cloud: api, Endpoint: "https://cloud.example"}
	if err := m.Clone(ctx, target, "ws1"); err != nil {
		t.Fatal(err)
	}
	if _, err := studio.Open(target); err != nil {
		t.Fatal(err)
	}
	api.ws.HeadRevisionID = "r2"
	api.rev = cloud.Revision{ID: "r2", Files: []cloud.File{{Path: "studio.yaml", Content: []byte("port: 4402\n")}}}
	if err := m.Pull(ctx, target); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(target, "studio.yaml"))
	if string(b) != "port: 4402\n" {
		t.Fatalf("pull failed %q", b)
	}
}

func TestPullRejectsConcurrentLocalEdit(t *testing.T) {
	root := localStudio(t)
	api := &fakeCloud{ws: cloud.Workspace{ID: "ws1", HeadRevisionID: "r1"}}
	m := Manager{Cloud: api, Endpoint: "https://cloud.example"}
	if err := m.Connect(context.Background(), root, "ws1"); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(root, "studio.yaml"), []byte("port: 4499\n"), 0644)
	api.ws.HeadRevisionID = "r2"
	api.rev = cloud.Revision{ID: "r2", Files: []cloud.File{{Path: "studio.yaml", Content: []byte("remote\n")}}}
	if err := m.Pull(context.Background(), root); !errors.Is(err, ErrLocalChanges) {
		t.Fatalf("got %v", err)
	}
}
