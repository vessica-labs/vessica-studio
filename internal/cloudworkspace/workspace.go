package cloudworkspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"github.com/vessica-labs/vessica-studio/internal/cloud"
	"github.com/vessica-labs/vessica-studio/internal/studio"
)

var ErrLocalChanges = errors.New("workspace has unsynced local changes; run status and reconcile before pulling")
var idRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,255}$`)

type Cloud interface {
	Workspace(context.Context, string) (cloud.Workspace, error)
	Revision(context.Context, string, string) (cloud.Revision, error)
	Sync(context.Context, string, cloud.SyncRequest) (cloud.Revision, error)
}
type Manager struct {
	Cloud    Cloud
	Endpoint string
}
type Association struct {
	Version                int    `json:"version"`
	Endpoint               string `json:"endpoint"`
	WorkspaceID            string `json:"workspace_id"`
	BaseRevisionID         string `json:"base_revision_id"`
	BaseDigest             string `json:"base_digest"`
	ConflictHeadRevisionID string `json:"conflict_head_revision_id,omitempty"`
}
type Status struct {
	WorkspaceID         string
	BaseRevisionID      string
	CloudHeadRevisionID string
	Unsynced            bool
	Offline             bool
	Conflict            bool
}

func associationPath(root string) string { return filepath.Join(root, ".vstd", "cloud-workspace.json") }
func LoadAssociation(root string) (Association, error) {
	var a Association
	b, e := os.ReadFile(associationPath(root))
	if e != nil {
		return a, e
	}
	e = json.Unmarshal(b, &a)
	if e == nil {
		e = validateAssociation(a)
	}
	return a, e
}
func validateAssociation(a Association) error {
	if a.Version != 1 || a.Endpoint == "" || !idRE.MatchString(a.WorkspaceID) || !idRE.MatchString(a.BaseRevisionID) {
		return fmt.Errorf("invalid cloud workspace association")
	}
	return nil
}
func saveAssociation(root string, a Association) error {
	if err := validateAssociation(a); err != nil {
		return err
	}
	dir := filepath.Dir(associationPath(root))
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(a, "", "  ")
	b = append(b, '\n')
	tmp := filepath.Join(dir, "cloud-workspace.tmp")
	if err := os.WriteFile(tmp, b, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, associationPath(root))
}

func (m Manager) Connect(ctx context.Context, root, id string) error {
	if _, err := studio.Open(root); err != nil {
		return err
	}
	if !idRE.MatchString(id) {
		return fmt.Errorf("invalid workspace identifier")
	}
	w, err := m.Cloud.Workspace(ctx, id)
	if err != nil {
		return err
	}
	if w.ID != "" && w.ID != id {
		return fmt.Errorf("cloud returned a different workspace")
	}
	s, err := studio.CloudContent(root)
	if err != nil {
		return err
	}
	return saveAssociation(root, Association{Version: 1, Endpoint: m.Endpoint, WorkspaceID: id, BaseRevisionID: w.HeadRevisionID, BaseDigest: s.Digest})
}
func (m Manager) Clone(ctx context.Context, target, id string) error {
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		return fmt.Errorf("clone target must not exist")
	}
	if !idRE.MatchString(id) {
		return fmt.Errorf("invalid workspace identifier")
	}
	w, err := m.Cloud.Workspace(ctx, id)
	if err != nil {
		return err
	}
	r, err := m.Cloud.Revision(ctx, id, w.HeadRevisionID)
	if err != nil {
		return err
	}
	if r.ID != "" && r.ID != w.HeadRevisionID {
		return fmt.Errorf("cloud returned an ambiguous revision")
	}
	if err := os.Mkdir(target, 0755); err != nil {
		return err
	}
	ok := false
	defer func() {
		if !ok {
			os.RemoveAll(target)
		}
	}()
	if err := studio.ApplyCloudContent(target, r.Files); err != nil {
		return err
	}
	s, err := studio.CloudContent(target)
	if err != nil {
		return err
	}
	if err = saveAssociation(target, Association{Version: 1, Endpoint: m.Endpoint, WorkspaceID: id, BaseRevisionID: w.HeadRevisionID, BaseDigest: s.Digest}); err != nil {
		return err
	}
	ok = true
	return nil
}
func (m Manager) Status(ctx context.Context, root string) (Status, error) {
	a, err := LoadAssociation(root)
	if err != nil {
		return Status{}, err
	}
	s, err := studio.CloudContent(root)
	if err != nil {
		return Status{}, err
	}
	out := Status{WorkspaceID: a.WorkspaceID, BaseRevisionID: a.BaseRevisionID, Unsynced: s.Digest != a.BaseDigest, Conflict: a.ConflictHeadRevisionID != ""}
	w, err := m.Cloud.Workspace(ctx, a.WorkspaceID)
	if cloud.IsKind(err, cloud.ErrorOffline) {
		out.Offline = true
		return out, nil
	}
	if err != nil {
		return out, err
	}
	out.CloudHeadRevisionID = w.HeadRevisionID
	if w.HeadRevisionID != a.BaseRevisionID {
		out.Conflict = true
	}
	return out, nil
}
func (m Manager) Pull(ctx context.Context, root string) error {
	a, err := LoadAssociation(root)
	if err != nil {
		return err
	}
	before, err := studio.CloudContent(root)
	if err != nil {
		return err
	}
	if before.Digest != a.BaseDigest {
		return ErrLocalChanges
	}
	w, err := m.Cloud.Workspace(ctx, a.WorkspaceID)
	if err != nil {
		return err
	}
	if w.HeadRevisionID == a.BaseRevisionID {
		return nil
	}
	r, err := m.Cloud.Revision(ctx, a.WorkspaceID, w.HeadRevisionID)
	if err != nil {
		return err
	}
	if r.ID != "" && r.ID != w.HeadRevisionID {
		return fmt.Errorf("cloud returned an ambiguous revision")
	}
	again, err := studio.CloudContent(root)
	if err != nil {
		return err
	}
	if again.Digest != before.Digest {
		return ErrLocalChanges
	}
	if err := studio.ApplyCloudContent(root, r.Files); err != nil {
		return err
	}
	after, err := studio.CloudContent(root)
	if err != nil {
		return err
	}
	a.BaseRevisionID = w.HeadRevisionID
	a.BaseDigest = after.Digest
	a.ConflictHeadRevisionID = ""
	return saveAssociation(root, a)
}
func (m Manager) Sync(ctx context.Context, root, message string) (cloud.Revision, error) {
	a, err := LoadAssociation(root)
	if err != nil {
		return cloud.Revision{}, err
	}
	s, err := studio.CloudContent(root)
	if err != nil {
		return cloud.Revision{}, err
	}
	op := sha256.Sum256([]byte(a.WorkspaceID + "\x00" + a.BaseRevisionID + "\x00" + s.Digest))
	r, err := m.Cloud.Sync(ctx, a.WorkspaceID, cloud.SyncRequest{BaseRevisionID: a.BaseRevisionID, Files: s.Files, Message: message, OperationID: hex.EncodeToString(op[:])})
	if err != nil {
		var c *cloud.ConflictError
		if errors.As(err, &c) && idRE.MatchString(c.CloudHeadRevisionID) {
			a.ConflictHeadRevisionID = c.CloudHeadRevisionID
			_ = saveAssociation(root, a)
		}
		return cloud.Revision{}, err
	}
	if !idRE.MatchString(r.ID) {
		return cloud.Revision{}, fmt.Errorf("cloud returned an ambiguous revision")
	}
	again, err := studio.CloudContent(root)
	if err != nil {
		return cloud.Revision{}, err
	}
	if again.Digest != s.Digest {
		return cloud.Revision{}, ErrLocalChanges
	}
	a.BaseRevisionID = r.ID
	a.BaseDigest = s.Digest
	a.ConflictHeadRevisionID = ""
	if err := saveAssociation(root, a); err != nil {
		return cloud.Revision{}, err
	}
	return r, nil
}
