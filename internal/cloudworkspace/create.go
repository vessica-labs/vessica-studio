package cloudworkspace

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/vessica-labs/vessica-studio/internal/cloud"
	"github.com/vessica-labs/vessica-studio/internal/studio"
)

type Creator interface {
	CreateWorkspace(context.Context, cloud.CreateWorkspaceRequest) (cloud.Revision, error)
}
type pendingCreate struct {
	Endpoint    string `json:"endpoint"`
	Title       string `json:"title"`
	Digest      string `json:"digest"`
	OperationID string `json:"operation_id"`
}

// Create uploads canonical local files once and records their cloud base. The
// non-secret journal survives uncertain responses, so retries cannot duplicate
// a presentation or silently upload changed files under the same operation.
func (m Manager) Create(ctx context.Context, root, title string) (cloud.Revision, error) {
	var zero cloud.Revision
	title = strings.TrimSpace(title)
	if title == "" || len(title) > 200 {
		return zero, errors.New("presentation title must be 1–200 bytes")
	}
	if _, err := os.Lstat(associationPath(root)); !os.IsNotExist(err) {
		return zero, errors.New("workspace is already connected; use status or sync")
	}
	creator, ok := m.Cloud.(Creator)
	if !ok {
		return zero, errors.New("cloud does not support creating presentations")
	}
	st, err := studio.Open(root)
	if err != nil {
		return zero, err
	}
	decks, err := st.ListDecks()
	if err != nil || len(decks) != 1 {
		return zero, errors.New("create requires exactly one local deck")
	}
	snapshot, err := studio.CloudContent(root)
	if err != nil {
		return zero, err
	}
	const rel = ".vstd/cloud-create.json"
	if err := studio.CheckContentPath(root, rel); err != nil {
		return zero, err
	}
	p := filepath.Join(root, filepath.FromSlash(rel))
	var pending pendingCreate
	b, err := os.ReadFile(p)
	if err == nil {
		if json.Unmarshal(b, &pending) != nil || pending.Endpoint != m.Endpoint || pending.Title != title || pending.Digest != snapshot.Digest || !idRE.MatchString(pending.OperationID) {
			return zero, errors.New("pending cloud creation differs; preserve the journal and original content before retrying")
		}
	} else if os.IsNotExist(err) {
		var id [24]byte
		if _, err := rand.Read(id[:]); err != nil {
			return zero, err
		}
		pending = pendingCreate{m.Endpoint, title, snapshot.Digest, hex.EncodeToString(id[:])}
		if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
			return zero, err
		}
		b, _ = json.Marshal(pending)
		f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return zero, err
		}
		_, writeErr := f.Write(b)
		syncErr := f.Sync()
		closeErr := f.Close()
		if writeErr != nil || syncErr != nil || closeErr != nil {
			return zero, errors.New("could not persist creation journal; no upload attempted")
		}
	} else {
		return zero, err
	}
	revision, err := creator.CreateWorkspace(ctx, cloud.CreateWorkspaceRequest{Title: title, Files: wireFiles(snapshot.Files), OperationID: pending.OperationID})
	if err != nil {
		return zero, err
	}
	if !idRE.MatchString(revision.ID) || !idRE.MatchString(revision.WorkspaceID) {
		return zero, errors.New("cloud returned an invalid created presentation")
	}
	if err := saveAssociation(root, Association{Version: 1, Endpoint: m.Endpoint, WorkspaceID: revision.WorkspaceID, BaseRevisionID: revision.ID, BaseDigest: snapshot.Digest}); err != nil {
		return zero, fmt.Errorf("presentation created; retry with the preserved journal: %w", err)
	}
	if err := os.Remove(p); err != nil {
		return revision, fmt.Errorf("presentation connected; creation journal cleanup failed: %w", err)
	}
	return revision, nil
}
