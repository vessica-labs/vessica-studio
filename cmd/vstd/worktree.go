package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/vessica-labs/vessica-studio/internal/cloud"
	"github.com/vessica-labs/vessica-studio/internal/cloudauth"
	"github.com/vessica-labs/vessica-studio/internal/cloudworkspace"
)

func connectedManager(root string) (*cloudworkspace.Manager, error) {
	association, err := cloudworkspace.LoadAssociation(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	raw, err := cloud.NewClient(cloud.WithEndpoint(association.Endpoint), cloud.WithHTTPClient(cloudHTTPClient()), cloud.WithClientVersion(version))
	if err != nil {
		return nil, err
	}
	auth := cloudauth.New(raw, cloudCredentialStore(association.Endpoint))
	client, err := cloud.NewClient(cloud.WithEndpoint(association.Endpoint), cloud.WithHTTPClient(cloudHTTPClient()), cloud.WithClientVersion(version), cloud.WithTokenSource(auth))
	if err != nil {
		return nil, err
	}
	return &cloudworkspace.Manager{Cloud: client, Endpoint: association.Endpoint}, nil
}

func syncConnected(root string, agent bool) error {
	manager, err := connectedManager(root)
	if err != nil || manager == nil {
		return err
	}
	manager.PreferCurrent = agent
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	_, err = manager.Sync(ctx, root, "Synced authoring changes")
	return err
}

func cmdWorktree(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: vstd worktree begin|finish --root DIR [--local-only]")
	}
	fs := flag.NewFlagSet("worktree", flag.ContinueOnError)
	root := fs.String("root", ".", "parent studio for begin, worktree for finish")
	local := fs.Bool("local-only", false, "do not synchronize with Cloud")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	switch args[0] {
	case "begin":
		if !*local {
			if err := syncConnected(*root, false); err != nil {
				fmt.Fprintln(os.Stderr, "Working offline; local files are preserved:", err)
			}
		}
		branch, err := cloudworkspace.BeginBranch(*root)
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(map[string]string{"worktree": branch.Root, "parent": branch.Parent})
	case "finish":
		branch, err := cloudworkspace.LoadBranch(*root)
		if err != nil {
			return err
		}
		if !*local {
			if err := syncConnected(branch.Parent, false); err != nil {
				fmt.Fprintln(os.Stderr, "Cloud unavailable; preserving local work:", err)
			}
		}
		if err := cloudworkspace.FinishBranch(*root); err != nil {
			return err
		}
		if !*local {
			if err := syncConnected(branch.Parent, true); err != nil {
				return fmt.Errorf("worktree reconciled locally and checkpointed; cloud sync pending: %w", err)
			}
		}
		fmt.Println("Reconciled worktree; original checkpoints retained.")
		return nil
	default:
		return fmt.Errorf("unknown worktree command %q", args[0])
	}
}
