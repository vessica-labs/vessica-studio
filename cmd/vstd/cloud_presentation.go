package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/vessica-labs/vessica-studio/internal/cloud"
	"github.com/vessica-labs/vessica-studio/internal/cloudworkspace"
	"github.com/vessica-labs/vessica-studio/internal/studio"
)

var cloudPresentationConfigDir = os.UserConfigDir
var managedPathID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,255}$`)

type cloudPresentationResult struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Root     string `json:"root"`
	Revision string `json:"revision"`
	Created  bool   `json:"created"`
}

func runCloudPresentation(ctx context.Context, client *cloud.Client, endpoint string, args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: vstd cloud presentation list|open|create")
	}
	switch args[0] {
	case "list":
		fs := flag.NewFlagSet("cloud presentation list", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		asJSON := fs.Bool("json", false, "emit machine-readable output")
		if err := fs.Parse(args[1:]); err != nil || fs.NArg() != 0 {
			return errors.New("usage: vstd cloud presentation list [--json]")
		}
		presentations, err := listCloudPresentations(ctx, client)
		if err != nil {
			return err
		}
		if *asJSON {
			return json.NewEncoder(out).Encode(struct {
				Presentations []cloud.Workspace `json:"presentations"`
			}{presentations})
		}
		for _, presentation := range presentations {
			fmt.Fprintf(out, "%s\t%s\t%s\n", presentation.ID, presentation.Name, presentation.HeadRevisionID)
		}
		return nil
	case "open":
		return openCloudPresentation(ctx, client, endpoint, args[1:], out)
	case "create":
		return createCloudPresentation(ctx, client, endpoint, args[1:], out)
	default:
		return fmt.Errorf("unknown cloud presentation command %q", args[0])
	}
}

func openCloudPresentation(ctx context.Context, client *cloud.Client, endpoint string, args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: vstd cloud presentation open <title-or-id> [--root DIR] [--json]")
	}
	selector := args[0]
	fs := flag.NewFlagSet("cloud presentation open", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	rootFlag := fs.String("root", "", "local working directory")
	asJSON := fs.Bool("json", false, "emit machine-readable output")
	if err := fs.Parse(args[1:]); err != nil || fs.NArg() != 0 {
		return errors.New("usage: vstd cloud presentation open <title-or-id> [--root DIR] [--json]")
	}
	presentations, err := listCloudPresentations(ctx, client)
	if err != nil {
		return err
	}
	presentation, err := selectCloudPresentation(selector, presentations)
	if err != nil {
		return err
	}
	if presentation.CanEdit != nil && !*presentation.CanEdit {
		return fmt.Errorf("cloud presentation %q is view-only; ask its owner for authoring access or create your own presentation", presentation.Name)
	}
	root := strings.TrimSpace(*rootFlag)
	if root == "" {
		account, err := client.Account(ctx)
		if err != nil {
			return err
		}
		base, err := managedPresentationBase(endpoint, account.ID)
		if err != nil {
			return err
		}
		root, err = findManagedPresentationRoot(base, endpoint, presentation.ID)
		if err != nil {
			return err
		}
		if root == "" {
			root, err = availableManagedPresentationRoot(base, presentationSlug(presentation.Name)+"-"+shortID(presentation.ID))
			if err != nil {
				return err
			}
		}
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return err
	}
	manager := cloudworkspace.Manager{Cloud: client, Endpoint: endpoint}
	revision := presentation.HeadRevisionID
	if info, statErr := os.Stat(root); statErr == nil {
		if !info.IsDir() {
			return errors.New("presentation root is not a directory")
		}
		association, associationErr := manager.Association(root)
		if associationErr != nil || association.WorkspaceID != presentation.ID {
			return errors.New("presentation root is already occupied by different local content")
		}
		synced, syncErr := manager.Sync(ctx, root, "Opened from agent chat")
		if syncErr != nil {
			return syncErr
		}
		revision = synced.ID
	} else if os.IsNotExist(statErr) {
		if err := os.MkdirAll(filepath.Dir(root), 0700); err != nil {
			return err
		}
		if err := manager.Clone(ctx, root, presentation.ID); err != nil {
			return err
		}
		association, err := manager.Association(root)
		if err != nil {
			return err
		}
		revision = association.BaseRevisionID
	} else {
		return statErr
	}
	return writeCloudPresentationResult(out, *asJSON, cloudPresentationResult{ID: presentation.ID, Title: presentation.Name, Root: root, Revision: revision})
}

func createCloudPresentation(ctx context.Context, client *cloud.Client, endpoint string, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("cloud presentation create", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	title := fs.String("title", "", "presentation title")
	rootFlag := fs.String("root", "", "local working directory")
	asJSON := fs.Bool("json", false, "emit machine-readable output")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 || strings.TrimSpace(*title) == "" {
		return errors.New("usage: vstd cloud presentation create --title TITLE [--root DIR] [--json]")
	}
	trimmedTitle := strings.TrimSpace(*title)
	if len(trimmedTitle) > 200 {
		return errors.New("presentation title must be 1–200 bytes")
	}
	root := strings.TrimSpace(*rootFlag)
	managed := root == ""
	if managed {
		account, err := client.Account(ctx)
		if err != nil {
			return err
		}
		base, err := managedPresentationBase(endpoint, account.ID)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(base, 0700); err != nil {
			return err
		}
		root, err = os.MkdirTemp(base, presentationSlug(trimmedTitle)+"-")
		if err != nil {
			return err
		}
	} else if _, err := os.Stat(root); !os.IsNotExist(err) {
		return errors.New("new presentation root must not exist")
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	if err := studio.Init(root); err != nil {
		return fmt.Errorf("presentation draft preserved at %s: %w", root, err)
	}
	st, err := studio.Open(root)
	if err != nil {
		return fmt.Errorf("presentation draft preserved at %s: %w", root, err)
	}
	if err := st.NewDeck(presentationSlug(trimmedTitle), trimmedTitle); err != nil {
		return fmt.Errorf("presentation draft preserved at %s: %w", root, err)
	}
	revision, err := (cloudworkspace.Manager{Cloud: client, Endpoint: endpoint}).Create(ctx, root, trimmedTitle)
	if err != nil {
		return fmt.Errorf("presentation draft preserved at %s: %w", root, err)
	}
	if managed {
		final := filepath.Join(filepath.Dir(root), presentationSlug(trimmedTitle)+"-"+shortID(revision.WorkspaceID))
		if _, statErr := os.Stat(final); os.IsNotExist(statErr) {
			if renameErr := os.Rename(root, final); renameErr == nil {
				root = final
			}
		}
	}
	return writeCloudPresentationResult(out, *asJSON, cloudPresentationResult{ID: revision.WorkspaceID, Title: trimmedTitle, Root: root, Revision: revision.ID, Created: true})
}

func listCloudPresentations(ctx context.Context, client *cloud.Client) ([]cloud.Workspace, error) {
	var presentations []cloud.Workspace
	cursor := ""
	for page := 0; page < 100; page++ {
		result, err := client.Workspaces(ctx, cursor)
		if err != nil {
			return nil, err
		}
		presentations = append(presentations, result.Workspaces...)
		if result.NextCursor == "" {
			sort.SliceStable(presentations, func(i, j int) bool {
				return strings.ToLower(presentations[i].Name) < strings.ToLower(presentations[j].Name)
			})
			return presentations, nil
		}
		if result.NextCursor == cursor {
			return nil, errors.New("cloud presentation listing returned a repeated cursor")
		}
		cursor = result.NextCursor
	}
	return nil, errors.New("cloud presentation listing exceeded the page limit")
}

func selectCloudPresentation(selector string, presentations []cloud.Workspace) (cloud.Workspace, error) {
	selector = strings.TrimSpace(selector)
	if selector == "" {
		return cloud.Workspace{}, errors.New("presentation title or ID is required")
	}
	for _, presentation := range presentations {
		if presentation.ID == selector {
			return presentation, nil
		}
	}
	var exact, partial []cloud.Workspace
	for _, presentation := range presentations {
		if strings.EqualFold(strings.TrimSpace(presentation.Name), selector) {
			exact = append(exact, presentation)
		} else if strings.Contains(strings.ToLower(presentation.Name), strings.ToLower(selector)) {
			partial = append(partial, presentation)
		}
	}
	matches := exact
	if len(matches) == 0 {
		matches = partial
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	if len(matches) == 0 {
		return cloud.Workspace{}, fmt.Errorf("no editable cloud presentation matches %q", selector)
	}
	labels := make([]string, 0, min(len(matches), 10))
	for _, match := range matches[:min(len(matches), 10)] {
		labels = append(labels, match.ID+" ("+match.Name+")")
	}
	return cloud.Workspace{}, fmt.Errorf("cloud presentation %q is ambiguous: %s", selector, strings.Join(labels, ", "))
}

func managedPresentationBase(endpoint, accountID string) (string, error) {
	if !managedPathID.MatchString(accountID) {
		return "", errors.New("cloud returned an invalid account identifier")
	}
	config, err := cloudPresentationConfigDir()
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256([]byte(strings.TrimRight(endpoint, "/")))
	return filepath.Join(config, "vessica-studio", "cloud-presentations", hex.EncodeToString(digest[:8]), accountID), nil
}

func findManagedPresentationRoot(base, endpoint, id string) (string, error) {
	entries, err := os.ReadDir(base)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		root := filepath.Join(base, entry.Name())
		association, err := cloudworkspace.LoadAssociation(root)
		if err == nil && association.WorkspaceID == id && strings.TrimRight(association.Endpoint, "/") == strings.TrimRight(endpoint, "/") {
			return root, nil
		}
	}
	return "", nil
}

func availableManagedPresentationRoot(base, name string) (string, error) {
	for suffix := 1; suffix <= 1000; suffix++ {
		candidate := filepath.Join(base, name)
		if suffix > 1 {
			candidate = filepath.Join(base, fmt.Sprintf("%s-%d", name, suffix))
		}
		if _, err := os.Stat(candidate); os.IsNotExist(err) {
			return candidate, nil
		} else if err != nil {
			return "", err
		}
	}
	return "", errors.New("could not allocate a managed presentation directory")
}

func presentationSlug(title string) string {
	var slug strings.Builder
	dash := false
	for _, r := range strings.ToLower(strings.TrimSpace(title)) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			if dash && slug.Len() > 0 {
				slug.WriteByte('-')
			}
			dash = false
			slug.WriteRune(r)
		} else {
			dash = true
		}
		if slug.Len() >= 48 {
			break
		}
	}
	value := strings.Trim(slug.String(), "-")
	if value == "" {
		return "presentation"
	}
	return value
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func writeCloudPresentationResult(out io.Writer, asJSON bool, result cloudPresentationResult) error {
	if asJSON {
		return json.NewEncoder(out).Encode(result)
	}
	fmt.Fprintf(out, "presentation: %s\ntitle: %s\nroot: %s\nrevision: %s\n", result.ID, result.Title, result.Root, result.Revision)
	if result.Created {
		fmt.Fprintln(out, "state: created and synchronized")
	} else {
		fmt.Fprintln(out, "state: synchronized")
	}
	return nil
}
