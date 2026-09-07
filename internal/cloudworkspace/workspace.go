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
	"runtime"
	"strings"

	"github.com/vessica-labs/vessica-studio/internal/cloud"
	"github.com/vessica-labs/vessica-studio/internal/reconcile"
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
	PreferCurrent bool
	Cloud         Cloud
	Endpoint      string
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
	WorkspaceID            string
	BaseRevisionID         string
	CloudHeadRevisionID    string
	Unsynced               bool
	Offline                bool
	Conflict               bool
	ConflictHeadRevisionID string
}

func associationPath(root string) string { return filepath.Join(root, ".vstd", "cloud-workspace.json") }
func LoadAssociation(root string) (Association, error) {
	var a Association
	if err := studio.CheckContentPath(root, ".vstd/cloud-workspace.json"); err != nil {
		return a, err
	}
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
	if a.Version != 1 || a.Endpoint == "" || !idRE.MatchString(a.WorkspaceID) || !idRE.MatchString(a.BaseRevisionID) || len(a.BaseDigest) != 64 {
		return fmt.Errorf("invalid cloud workspace association")
	}
	if _, err := hex.DecodeString(a.BaseDigest); err != nil {
		return fmt.Errorf("invalid cloud workspace digest")
	}
	return nil
}

func (m Manager) Association(root string) (Association, error) {
	a, err := LoadAssociation(root)
	if err != nil {
		return a, err
	}
	if strings.TrimRight(a.Endpoint, "/") != strings.TrimRight(m.Endpoint, "/") {
		return a, fmt.Errorf("workspace belongs to a different cloud endpoint; select its original endpoint")
	}
	return a, nil
}

func contentFiles(files []cloud.File) []studio.ContentFile {
	out := make([]studio.ContentFile, len(files))
	for i, f := range files {
		out[i] = studio.ContentFile{Path: f.Path, Content: f.Content, Mode: f.Mode}
	}
	return out
}
func wireFiles(files []studio.ContentFile) []cloud.File {
	out := make([]cloud.File, len(files))
	for i, f := range files {
		out[i] = cloud.File{Path: f.Path, Content: f.Content, Mode: f.Mode}
	}
	return out
}
func saveAssociation(root string, a Association) error {
	if err := validateAssociation(a); err != nil {
		return err
	}
	if err := studio.CheckContentPath(root, ".vstd/cloud-workspace.json"); err != nil {
		return err
	}
	dir := filepath.Dir(associationPath(root))
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(a, "", "  ")
	b = append(b, '\n')
	tmp, err := os.CreateTemp(dir, ".cloud-workspace-")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), associationPath(root))
}

func (m Manager) Connect(ctx context.Context, root, id string) error {
	if _, err := os.Lstat(associationPath(root)); !os.IsNotExist(err) {
		return fmt.Errorf("workspace is already connected; preserve its base and use status or sync")
	}
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
	if w.ID != id || !idRE.MatchString(w.HeadRevisionID) {
		return fmt.Errorf("cloud returned a different workspace")
	}
	_, err = studio.CloudContent(root)
	if err != nil {
		return err
	}
	r, err := m.Cloud.Revision(ctx, id, w.HeadRevisionID)
	if err != nil {
		return err
	}
	if r.ID != w.HeadRevisionID || (r.WorkspaceID != "" && r.WorkspaceID != id) {
		return fmt.Errorf("cloud returned an ambiguous revision")
	}
	digest, err := studio.ContentDigest(contentFiles(r.Files))
	if err != nil {
		return err
	}
	return saveAssociation(root, Association{Version: 1, Endpoint: m.Endpoint, WorkspaceID: id, BaseRevisionID: w.HeadRevisionID, BaseDigest: digest})
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
	if w.ID != id || !idRE.MatchString(w.HeadRevisionID) {
		return fmt.Errorf("cloud returned an ambiguous workspace")
	}
	r, err := m.Cloud.Revision(ctx, id, w.HeadRevisionID)
	if err != nil {
		return err
	}
	if r.ID != w.HeadRevisionID || (r.WorkspaceID != "" && r.WorkspaceID != id) {
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
	if err := studio.ApplyCloudContent(target, contentFiles(r.Files)); err != nil {
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
	a, err := m.Association(root)
	if err != nil {
		return Status{}, err
	}
	s, err := studio.CloudContent(root)
	if err != nil {
		return Status{}, err
	}
	out := Status{WorkspaceID: a.WorkspaceID, BaseRevisionID: a.BaseRevisionID, Unsynced: s.Digest != a.BaseDigest, Conflict: a.ConflictHeadRevisionID != ""}
	out.ConflictHeadRevisionID = a.ConflictHeadRevisionID
	w, err := m.Cloud.Workspace(ctx, a.WorkspaceID)
	if cloud.IsKind(err, cloud.ErrorOffline) {
		out.Offline = true
		return out, nil
	}
	if err != nil {
		return out, err
	}
	out.CloudHeadRevisionID = w.HeadRevisionID
	if w.ID != a.WorkspaceID || !idRE.MatchString(w.HeadRevisionID) {
		return out, fmt.Errorf("cloud returned an ambiguous workspace")
	}
	if w.HeadRevisionID != a.BaseRevisionID {
		out.Conflict = true
	}
	return out, nil
}

// Pull is a two-way synchronization: local work is checkpointed and reconciled.
func (m Manager) Pull(ctx context.Context, root string) error {
	_, err := m.Sync(ctx, root, "Synced local and cloud changes")
	return err
}
func (m Manager) Sync(ctx context.Context, root, message string) (cloud.Revision, error) {
	unlockSync, err := studio.LockContent(root, "cloud-sync")
	if err != nil {
		return cloud.Revision{}, err
	}
	defer unlockSync()
	unlock, err := studio.LockContent(root, "content")
	if err != nil {
		return cloud.Revision{}, err
	}
	locked := true
	defer func() {
		if locked {
			unlock()
		}
	}()
	if err := studio.RecoverCloudContent(root); err != nil {
		return cloud.Revision{}, err
	}
	a, err := m.Association(root)
	if err != nil {
		return cloud.Revision{}, err
	}
	s, err := studio.CloudContent(root)
	if err != nil {
		return cloud.Revision{}, err
	}
	op := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%s\x00%s\x00%s\x00%t", a.WorkspaceID, a.BaseRevisionID, s.Digest, message, m.PreferCurrent)))
	operation := hex.EncodeToString(op[:])
	if err := checkpoint(root, operation, s.Files); err != nil {
		return cloud.Revision{}, err
	}
	unlock()
	locked = false
	var revision cloud.Revision
	if s.Digest == a.BaseDigest {
		w, e := m.Cloud.Workspace(ctx, a.WorkspaceID)
		if e != nil {
			return revision, e
		}
		if w.ID != a.WorkspaceID || !idRE.MatchString(w.HeadRevisionID) {
			return revision, fmt.Errorf("invalid cloud head")
		}
		revision, err = m.Cloud.Revision(ctx, a.WorkspaceID, w.HeadRevisionID)
	} else {
		writerKind := "human"
		if m.PreferCurrent {
			writerKind = "agent"
		}
		revision, err = m.Cloud.Sync(ctx, a.WorkspaceID, cloud.SyncRequest{WriterKind: writerKind, BaseRevisionID: a.BaseRevisionID, Files: wireFiles(s.Files), Message: message, OperationID: operation})
		if err == nil && len(revision.Files) == 0 {
			revision, err = m.Cloud.Revision(ctx, a.WorkspaceID, revision.ID)
		}
	}
	if err != nil {
		return cloud.Revision{}, err
	}
	if !idRE.MatchString(revision.ID) || (revision.WorkspaceID != "" && revision.WorkspaceID != a.WorkspaceID) {
		return cloud.Revision{}, fmt.Errorf("ambiguous cloud revision")
	}
	remote := contentFiles(revision.Files)
	if err := studio.ValidateCloudContent(remote); err != nil {
		return cloud.Revision{}, err
	}
	unlock, err = studio.LockContent(root, "content")
	if err != nil {
		return cloud.Revision{}, err
	}
	locked = true
	again, err := studio.CloudContent(root)
	if err != nil {
		return cloud.Revision{}, err
	}
	incoming := remote
	if again.Digest != s.Digest {
		// New edits made during the network request are a separate local delta.
		if err := checkpoint(root, operation+"-during-sync", again.Files); err != nil {
			return cloud.Revision{}, err
		}
		result, e := reconcile.Merge(reconcile.Input{Base: mergeSnapshot(s.Files), Incoming: mergeSnapshot(again.Files), Current: mergeSnapshot(remote)})
		if e != nil {
			return cloud.Revision{}, e
		}
		incoming = make([]studio.ContentFile, len(result.Files))
		for i, f := range result.Files {
			incoming[i] = studio.ContentFile{Path: f.Path, Content: f.Content, Mode: f.Mode}
		}
	}
	incomingDigest, err := studio.ContentDigest(incoming)
	if err != nil {
		return cloud.Revision{}, err
	}
	if incomingDigest != again.Digest {
		if err := studio.ApplyCloudContent(root, incoming); err != nil {
			return cloud.Revision{}, err
		}
	}
	digest, err := studio.ContentDigest(remote)
	if err != nil {
		return cloud.Revision{}, err
	}
	a.BaseRevisionID = revision.ID
	a.BaseDigest = digest
	a.ConflictHeadRevisionID = ""
	if err := saveAssociation(root, a); err != nil {
		return cloud.Revision{}, err
	}
	return revision, nil
}

func mergeSnapshot(files []studio.ContentFile) reconcile.Snapshot {
	s := reconcile.Snapshot{Files: make([]reconcile.File, len(files))}
	for i, f := range files {
		s.Files[i] = reconcile.File{Path: f.Path, Content: f.Content, Mode: f.Mode}
	}
	return s
}

// A durable local checkpoint exists before any network or replacement operation.
func checkpoint(root, id string, files []studio.ContentFile) error {
	relative := ".vstd/checkpoints/" + id + ".json"
	if err := studio.CheckContentPath(root, relative); err != nil {
		return err
	}
	target := filepath.Join(root, relative)
	if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
		return err
	}
	data, err := json.Marshal(files)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(data)
	target = filepath.Join(filepath.Dir(target), id+"-"+hex.EncodeToString(digest[:])+".json")
	if existing, err := os.ReadFile(target); err == nil && string(existing) == string(data) {
		return nil
	}
	f, err := os.CreateTemp(filepath.Dir(target), ".checkpoint-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(f.Name(), target); err != nil {
		return err
	}
	if runtime.GOOS == "windows" {
		return nil // File data is flushed above; directory handles cannot be synced here.
	}
	dir, err := os.Open(filepath.Dir(target))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
