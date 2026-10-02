package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"github.com/vessica-labs/vessica-studio/internal/bundle"
	"io"
	"os"
	"path/filepath"
)

func cmdAssetBundle(args []string, inspect bool) error {
	fs := flag.NewFlagSet("asset bundle", flag.ContinueOnError)
	root := rootFlag(fs)
	slug := fs.String("slug", "", "application id")
	entry := fs.String("entrypoint", "index.html", "self-contained HTML entrypoint")
	poster := fs.String("poster", "", "library image path, e.g. img/demo.jpg")
	outputJSON := fs.Bool("json", false, "emit bundle metadata")
	if len(args) == 0 {
		return fmt.Errorf("bundle ZIP file is required")
	}
	file := args[0]
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected bundle arguments")
	}
	if inspect {
		f, err := os.Open(file)
		if err != nil {
			return err
		}
		defer f.Close()
		data, err := io.ReadAll(io.LimitReader(f, bundle.MaxArchiveBytes+1))
		if err != nil {
			return err
		}
		a, err := bundle.Inspect(data, *entry)
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(a)
	}
	st, err := openStudio(*root)
	if err != nil {
		return err
	}
	a, err := bundle.Ingest(filepath.Join(st.Root, "library"), file, *slug, *entry, *poster)
	if err != nil {
		return err
	}
	if *outputJSON {
		return json.NewEncoder(os.Stdout).Encode(a)
	}
	fmt.Printf("Added application %s (%d bytes, %d files). Reference data-vstd-bundle=%q; archive bytes stay outside Cloud snapshots.\n", a.ID, a.Bytes, a.FileCount, a.ID)
	return nil
}
