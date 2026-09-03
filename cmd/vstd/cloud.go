package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/vessica-labs/vessica-studio/internal/cloud"
	"github.com/vessica-labs/vessica-studio/internal/cloudauth"
	"github.com/vessica-labs/vessica-studio/internal/cloudpublish"
	"github.com/vessica-labs/vessica-studio/internal/cloudworkspace"
)

const defaultCloudEndpoint = "https://cloud.vessica.studio"

var (
	cloudCredentialStore = func() cloudauth.Store { return cloudauth.NewKeyringStore() }
	cloudHTTPClient      = func() *http.Client { return &http.Client{Timeout: 30 * time.Second} }
)

func cmdCloud(args []string) error { return runCloud(args, os.Stdout) }

func runCloud(args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: vstd cloud login|logout|account|workspace|publish|diagnostics")
	}
	endpoint := strings.TrimSpace(os.Getenv("VSTD_CLOUD_ENDPOINT"))
	if endpoint == "" {
		endpoint = defaultCloudEndpoint
	}
	store := cloudCredentialStore()
	raw, err := cloud.NewClient(cloud.WithEndpoint(endpoint), cloud.WithHTTPClient(cloudHTTPClient()), cloud.WithClientVersion(version))
	if err != nil {
		return err
	}
	auth := cloudauth.New(raw, store)
	client, err := cloud.NewClient(cloud.WithEndpoint(endpoint), cloud.WithHTTPClient(cloudHTTPClient()), cloud.WithClientVersion(version), cloud.WithTokenSource(auth))
	if err != nil {
		return err
	}
	ctx := context.Background()
	switch args[0] {
	case "login":
		_, err = auth.Login(ctx, func(p cloudauth.LoginPrompt) error {
			fmt.Fprintf(out, "Open %s and enter code %s\n", p.VerificationURI, p.UserCode)
			return nil
		})
		if err == nil {
			fmt.Fprintln(out, "Logged in to Vessica Studio Cloud.")
		}
		return err
	case "logout":
		err = auth.Logout(ctx)
		if err == nil {
			fmt.Fprintln(out, "Logged out of Vessica Studio Cloud.")
		}
		return err
	case "account":
		a, e := client.Account(ctx)
		if e != nil {
			return e
		}
		fmt.Fprintf(out, "%s\t%s\t%s\n", a.ID, a.DisplayName, a.Email)
		return nil
	case "workspace":
		return runCloudWorkspace(ctx, client, endpoint, args[1:], out)
	case "publish":
		return runCloudPublish(ctx, client, args[1:], out)
	case "diagnostics":
		caps, e := client.Capabilities(ctx)
		if e != nil {
			return e
		}
		u, _ := url.Parse(endpoint)
		fmt.Fprintf(out, "endpoint: %s://%s\nprotocol: %s\nminimum-vstd: %s\n", u.Scheme, u.Host, caps.Protocol, caps.MinimumClientVersion)
		return nil
	default:
		return fmt.Errorf("unknown cloud command %q", args[0])
	}
}

func runCloudWorkspace(ctx context.Context, client *cloud.Client, endpoint string, args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: vstd cloud workspace list|clone|connect|status|pull|sync")
	}
	switch args[0] {
	case "list":
		page, err := client.Workspaces(ctx, "")
		if err != nil {
			return err
		}
		for _, w := range page.Workspaces {
			fmt.Fprintf(out, "%s\t%s\t%s\n", w.ID, w.Name, w.HeadRevisionID)
		}
		return nil
	case "clone":
		fs := flag.NewFlagSet("cloud workspace clone", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		target := fs.String("target", "", "target directory")
		if len(args) < 2 {
			return errors.New("usage: vstd cloud workspace clone <workspace> --target DIR")
		}
		if err := fs.Parse(args[2:]); err != nil {
			return err
		}
		if fs.NArg() != 0 || *target == "" {
			return errors.New("usage: vstd cloud workspace clone <workspace> --target DIR")
		}
		workspaceID := args[1]
		err := (cloudworkspace.Manager{Cloud: client, Endpoint: endpoint}).Clone(ctx, *target, workspaceID)
		if err == nil {
			fmt.Fprintf(out, "Cloned workspace %s to %s.\n", workspaceID, *target)
		}
		return err
	default:
		fs := flag.NewFlagSet("cloud workspace "+args[0], flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		root := fs.String("root", ".", "studio root")
		message := fs.String("message", "", "revision message")
		parseArgs := args[1:]
		workspaceID := ""
		if args[0] == "connect" && len(parseArgs) > 0 && !strings.HasPrefix(parseArgs[0], "-") {
			workspaceID, parseArgs = parseArgs[0], parseArgs[1:]
		}
		if err := fs.Parse(parseArgs); err != nil {
			return err
		}
		manager := cloudworkspace.Manager{Cloud: client, Endpoint: endpoint}
		switch args[0] {
		case "connect":
			if workspaceID == "" || fs.NArg() != 0 {
				return errors.New("usage: vstd cloud workspace connect <workspace> [--root DIR]")
			}
			err := manager.Connect(ctx, *root, workspaceID)
			if err == nil {
				fmt.Fprintf(out, "Connected workspace %s.\n", workspaceID)
			}
			return err
		case "status":
			s, err := manager.Status(ctx, *root)
			if err != nil {
				return err
			}
			state := "synchronized"
			if s.Unsynced {
				state = "unsynced"
			}
			if s.Conflict {
				state = "conflict"
			}
			if s.Offline {
				state += ", offline"
			}
			fmt.Fprintf(out, "workspace: %s\nbase revision: %s\ncloud revision: %s\nstate: %s\n", s.WorkspaceID, s.BaseRevisionID, s.CloudHeadRevisionID, state)
			return nil
		case "pull":
			err := manager.Pull(ctx, *root)
			if err == nil {
				fmt.Fprintln(out, "Workspace is up to date.")
			}
			return err
		case "sync":
			r, err := manager.Sync(ctx, *root, *message)
			if err == nil {
				fmt.Fprintf(out, "Synchronized revision %s.\n", r.ID)
			}
			return err
		default:
			return fmt.Errorf("unknown cloud workspace command %q", args[0])
		}
	}
}

func runCloudPublish(ctx context.Context, service *cloud.Client, args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: vstd cloud publish create|status")
	}
	fs := flag.NewFlagSet("cloud publish", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	root := fs.String("root", ".", "studio root")
	revision := fs.String("revision", "", "revision ID")
	parseArgs := args[1:]
	publicationID := ""
	if args[0] == "status" && len(parseArgs) > 0 && !strings.HasPrefix(parseArgs[0], "-") {
		publicationID, parseArgs = parseArgs[0], parseArgs[1:]
	}
	if err := fs.Parse(parseArgs); err != nil {
		return err
	}
	a, err := cloudworkspace.LoadAssociation(*root)
	if err != nil {
		return err
	}
	publisher := cloudpublish.New(service, version)
	switch args[0] {
	case "create":
		s, e := (cloudworkspace.Manager{Cloud: service, Endpoint: a.Endpoint}).Status(ctx, *root)
		if e != nil {
			return e
		}
		p, e := publisher.Publish(ctx, cloudpublish.Association{WorkspaceID: a.WorkspaceID, SynchronizedRevisionID: a.BaseRevisionID, Unsynced: s.Unsynced, Conflict: s.Conflict}, *revision)
		if e != nil {
			return e
		}
		fmt.Fprintf(out, "publication: %s\nrevision: %s\nstatus: %s\nurl: %s\n", p.ID, p.RevisionID, p.Status, p.URL)
		return nil
	case "status":
		if publicationID == "" || fs.NArg() != 0 {
			return errors.New("usage: vstd cloud publish status <publication> [--root DIR]")
		}
		p, e := publisher.Status(ctx, a.WorkspaceID, publicationID)
		if e != nil {
			return e
		}
		fmt.Fprintf(out, "publication: %s\nrevision: %s\nstatus: %s\nurl: %s\n", p.ID, p.RevisionID, p.Status, p.URL)
		return nil
	default:
		return fmt.Errorf("unknown cloud publish command %q", args[0])
	}
}

func cloudErrorText(_ string, secret string, message string) string {
	return cloudauth.Redact(message, secret)
}
