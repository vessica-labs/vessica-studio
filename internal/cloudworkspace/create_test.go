package cloudworkspace

import (
	"context"
	"errors"
	"github.com/vessica-labs/vessica-studio/internal/cloud"
	"github.com/vessica-labs/vessica-studio/internal/studio"
	"os"
	"path/filepath"
	"testing"
)

type createCloud struct {
	fakeCloud
	inputs []cloud.CreateWorkspaceRequest
	fail   bool
}

func (f *createCloud) CreateWorkspace(_ context.Context, in cloud.CreateWorkspaceRequest) (cloud.Revision, error) {
	f.inputs = append(f.inputs, in)
	if f.fail {
		return cloud.Revision{}, errors.New("response lost")
	}
	return cloud.Revision{ID: "r1", WorkspaceID: "created"}, nil
}
func TestCreatePreservesIdempotentRetryAndAssociation(t *testing.T) {
	root := localStudio(t)
	st, _ := studio.Open(root)
	if err := st.NewDeck("demo", "Demo"); err != nil {
		t.Fatal(err)
	}
	remote := &createCloud{fail: true}
	m := Manager{Cloud: remote, Endpoint: "https://studio.example"}
	if _, err := m.Create(context.Background(), root, "Demo"); err == nil {
		t.Fatal("expected lost response")
	}
	remote.fail = false
	revision, err := m.Create(context.Background(), root, "Demo")
	if err != nil {
		t.Fatal(err)
	}
	if revision.WorkspaceID != "created" || len(remote.inputs) != 2 || remote.inputs[0].OperationID != remote.inputs[1].OperationID {
		t.Fatal("retry duplicated operation")
	}
	association, err := m.Association(root)
	if err != nil || association.BaseRevisionID != "r1" {
		t.Fatalf("association: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, ".vstd/cloud-create.json")); !os.IsNotExist(err) {
		t.Fatal("journal not cleaned")
	}
	if _, err := m.Create(context.Background(), root, "Demo"); err == nil {
		t.Fatal("connected workspace recreated")
	}
}
func TestCreateRejectsChangedPendingRequest(t *testing.T) {
	root := localStudio(t)
	st, _ := studio.Open(root)
	if err := st.NewDeck("demo", "Demo"); err != nil {
		t.Fatal(err)
	}
	remote := &createCloud{fail: true}
	m := Manager{Cloud: remote, Endpoint: "https://studio.example"}
	_, _ = m.Create(context.Background(), root, "Demo")
	if _, err := m.Create(context.Background(), root, "Changed title"); err == nil {
		t.Fatal("changed operation accepted")
	}
	if len(remote.inputs) != 1 {
		t.Fatal("changed request was uploaded")
	}
}
