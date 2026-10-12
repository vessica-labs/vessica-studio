package cloud

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
)

// TransferFile references bytes by path and SHA-256. Missing Content means reuse;
// a pointer preserves the distinction between a reference and an empty file.
type TransferFile struct {
	Path    string  `json:"path"`
	SHA256  string  `json:"sha256"`
	Content *[]byte `json:"content,omitempty"`
	Mode    uint32  `json:"mode,omitempty"`
}
type transferRevision struct {
	ID          string         `json:"id"`
	WorkspaceID string         `json:"workspace_id"`
	Files       []TransferFile `json:"files"`
}
type incrementalSync struct {
	WriterKind     string         `json:"writer_kind,omitempty"`
	BaseRevisionID string         `json:"base_revision_id"`
	Files          []TransferFile `json:"files"`
	Message        string         `json:"message,omitempty"`
	OperationID    string         `json:"operation_id"`
}

func fileHash(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func transferFiles(files []File, known []TransferFile) []TransferFile {
	hashes := map[string]string{}
	for _, f := range known {
		hashes[f.Path] = f.SHA256
	}
	out := make([]TransferFile, 0, len(files))
	for _, f := range files {
		t := TransferFile{Path: f.Path, SHA256: fileHash(f.Content), Mode: f.Mode}
		if hashes[f.Path] != t.SHA256 {
			b := f.Content
			if b == nil {
				b = []byte{}
			}
			t.Content = &b
		}
		out = append(out, t)
	}
	return out
}
func resolveTransfer(r transferRevision, known []File, workspace, id string) (Revision, error) {
	if id == "" || r.ID != id || r.WorkspaceID != workspace || len(r.Files) == 0 || len(r.Files) > 4096 {
		return Revision{}, fmt.Errorf("invalid incremental revision")
	}
	cache := map[string]File{}
	for _, f := range known {
		cache[f.Path] = f
	}
	seen := map[string]bool{}
	out := Revision{ID: r.ID, WorkspaceID: r.WorkspaceID}
	var total int
	for _, t := range r.Files {
		if seen[t.Path] || t.Path == "" {
			return Revision{}, fmt.Errorf("invalid incremental file list")
		}
		seen[t.Path] = true
		var b []byte
		if t.Content != nil {
			b = *t.Content
		} else {
			f, ok := cache[t.Path]
			if !ok {
				return Revision{}, fmt.Errorf("missing incremental file")
			}
			b = f.Content
		}
		total += len(b)
		if len(b) > 16<<20 || total > 128<<20 || fileHash(b) != t.SHA256 {
			return Revision{}, fmt.Errorf("incremental file integrity check failed")
		}
		out.Files = append(out.Files, File{Path: t.Path, Content: b, Mode: t.Mode})
	}
	return out, nil
}
func revisionPath(workspace, id string) string {
	return "/v1/workspaces/" + url.PathEscape(workspace) + "/revisions/" + url.PathEscape(id)
}
func (c *Client) syncIncremental(ctx context.Context, workspace string, in SyncRequest) (Revision, error) {
	var base transferRevision
	if err := c.doWithHTTP(ctx, c.revisionHTTP, http.MethodGet, revisionPath(workspace, in.BaseRevisionID)+"/manifest", nil, &base, true); err != nil {
		return Revision{}, err
	}
	if base.ID != in.BaseRevisionID || base.WorkspaceID != workspace {
		return Revision{}, fmt.Errorf("invalid incremental base")
	}
	body := incrementalSync{WriterKind: in.WriterKind, BaseRevisionID: in.BaseRevisionID, Files: transferFiles(in.Files, base.Files), Message: in.Message, OperationID: in.OperationID}
	var out transferRevision
	if err := c.doWithHTTP(ctx, c.revisionHTTP, http.MethodPost, "/v1/workspaces/"+url.PathEscape(workspace)+"/revisions/incremental", body, &out, true); err != nil {
		return Revision{}, err
	}
	return resolveTransfer(out, in.Files, workspace, out.ID)
}

// RevisionCached transfers only bytes different from the captured local snapshot.
// Capability negotiation keeps older servers on the full revision contract.
func (c *Client) RevisionCached(ctx context.Context, workspace, id string, known []File) (Revision, error) {
	if err := c.negotiate(ctx, CapabilityWorkspaceIncremental); err != nil {
		if e, ok := err.(*IncompatibleError); ok && e.MissingCapability == CapabilityWorkspaceIncremental {
			return c.Revision(ctx, workspace, id)
		}
		return Revision{}, err
	}
	refs := transferFiles(known, nil)
	for i := range refs {
		refs[i].Content = nil
	}
	var out transferRevision
	if err := c.doWithHTTP(ctx, c.revisionHTTP, http.MethodPost, revisionPath(workspace, id)+"/transfer", struct {
		Files []TransferFile `json:"files"`
	}{refs}, &out, true); err != nil {
		return Revision{}, err
	}
	return resolveTransfer(out, known, workspace, id)
}

// RevisionManifest describes a complete immutable revision without asset bytes.
// It is suitable for verification/publication, never for applying local content.
type RevisionManifest = transferRevision

func (c *Client) RevisionManifest(ctx context.Context, workspace, id string) (RevisionManifest, error) {
	var out RevisionManifest
	if err := c.negotiate(ctx, CapabilityWorkspaceIncremental); err != nil {
		return out, err
	}
	err := c.doWithHTTP(ctx, c.revisionHTTP, http.MethodGet, revisionPath(workspace, id)+"/manifest", nil, &out, true)
	if err == nil && (out.ID != id || out.WorkspaceID != workspace) {
		return RevisionManifest{}, fmt.Errorf("invalid revision manifest")
	}
	return out, err
}

// SyncManifest commits and reconciles a full snapshot but downloads only its
// canonical manifest. Callers must not mark local content clean from this result.
func (c *Client) SyncManifest(ctx context.Context, workspace string, in SyncRequest) (RevisionManifest, error) {
	base, err := c.RevisionManifest(ctx, workspace, in.BaseRevisionID)
	if err != nil {
		return RevisionManifest{}, err
	}
	if in.OperationID == "" && c.operationID != nil {
		in.OperationID = c.operationID()
	}
	body := struct {
		incrementalSync
		ResponseMode string `json:"response_mode"`
	}{incrementalSync{WriterKind: in.WriterKind, BaseRevisionID: in.BaseRevisionID, Files: transferFiles(in.Files, base.Files), Message: in.Message, OperationID: in.OperationID}, "manifest"}
	var out RevisionManifest
	err = c.doWithHTTP(ctx, c.revisionHTTP, http.MethodPost, "/v1/workspaces/"+url.PathEscape(workspace)+"/revisions/incremental", body, &out, true)
	if err == nil && (out.ID == "" || out.WorkspaceID != workspace || len(out.Files) == 0) {
		return RevisionManifest{}, fmt.Errorf("invalid sync manifest")
	}
	return out, err
}
