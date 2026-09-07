package cloudworkspace

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/vessica-labs/vessica-studio/internal/reconcile"
	"github.com/vessica-labs/vessica-studio/internal/studio"
)

type Branch struct {
	Root   string               `json:"root"`
	Parent string               `json:"parent"`
	Base   []studio.ContentFile `json:"base"`
}

// BeginBranch creates an independent file-contract worktree, without Git or a
// cloud account. The original base stays outside the agent's content projection.
func BeginBranch(root string) (Branch, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return Branch{}, err
	}
	unlock, err := studio.LockContent(root, "content")
	if err != nil {
		return Branch{}, err
	}
	defer unlock()
	snapshot, err := studio.CloudContent(root)
	if err != nil {
		return Branch{}, err
	}
	var token [16]byte
	if _, err := rand.Read(token[:]); err != nil {
		return Branch{}, err
	}
	id := hex.EncodeToString(token[:])
	relative := ".vstd/worktrees/" + id
	if err := studio.CheckContentPath(root, relative); err != nil {
		return Branch{}, err
	}
	target := filepath.Join(root, relative)
	if err := os.MkdirAll(target, 0700); err != nil {
		return Branch{}, err
	}
	if err := studio.ApplyCloudContent(target, snapshot.Files); err != nil {
		return Branch{}, err
	}
	branch := Branch{Root: target, Parent: root, Base: snapshot.Files}
	data, err := json.Marshal(branch)
	if err != nil {
		return Branch{}, err
	}
	if err := os.WriteFile(target+".json", data, 0600); err != nil {
		return Branch{}, err
	}
	if err := checkpoint(root, "branch-"+id, snapshot.Files); err != nil {
		return Branch{}, err
	}
	return branch, nil
}

func LoadBranch(root string) (Branch, error) {
	var branch Branch
	abs, err := filepath.Abs(root)
	if err != nil {
		return branch, err
	}
	data, err := os.ReadFile(abs + ".json")
	if err != nil {
		return branch, err
	}
	if err := json.Unmarshal(data, &branch); err != nil {
		return branch, err
	}
	if branch.Root != abs || filepath.Dir(abs) != filepath.Join(branch.Parent, ".vstd", "worktrees") {
		return branch, fmt.Errorf("invalid worktree metadata")
	}
	return branch, nil
}

// FinishBranch applies only the worktree delta. Conflicting direct edits in the
// parent win; both complete inputs remain as durable recovery checkpoints.
func FinishBranch(root string) error {
	branch, err := LoadBranch(root)
	if err != nil {
		return err
	}
	unlock, err := studio.LockContent(branch.Parent, "content")
	if err != nil {
		return err
	}
	defer unlock()
	incoming, err := studio.CloudContent(branch.Root)
	if err != nil {
		return err
	}
	current, err := studio.CloudContent(branch.Parent)
	if err != nil {
		return err
	}
	if err := checkpoint(branch.Parent, "agent-"+incoming.Digest, incoming.Files); err != nil {
		return err
	}
	if err := checkpoint(branch.Parent, "human-"+current.Digest, current.Files); err != nil {
		return err
	}
	result, err := reconcile.Merge(reconcile.Input{Base: mergeSnapshot(branch.Base), Incoming: mergeSnapshot(incoming.Files), Current: mergeSnapshot(current.Files), PreferCurrent: true})
	if err != nil {
		return err
	}
	files := make([]studio.ContentFile, len(result.Files))
	for i, file := range result.Files {
		files[i] = studio.ContentFile{Path: file.Path, Content: file.Content, Mode: file.Mode}
	}
	if err := studio.ApplyCloudContent(branch.Parent, files); err != nil {
		return err
	}
	// Keep the branch and base: interruption and repeated finish are non-destructive.
	return nil
}
