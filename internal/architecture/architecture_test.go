// Package architecture enforces public engine dependency directions with Go's parser.
package architecture

import (
	"fmt"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

const module = "github.com/vessica-labs/vessica-studio"

// Domain packages depend inward; the protocol package remains transport-neutral.
var forbidden = map[string][]string{
	"internal/studio":  {"cmd", "internal/cloud", "internal/cloudauth", "internal/cloudworkspace", "internal/cloudpublish", "internal/server", "internal/oai", "internal/s3"},
	"internal/library": {"cmd", "internal/server", "internal/oai", "internal/cloud", "internal/cloudauth", "internal/cloudworkspace", "internal/cloudpublish"},
	"internal/video":   {"cmd", "internal/server", "internal/oai", "internal/cloud", "internal/cloudauth", "internal/cloudworkspace", "internal/cloudpublish"},
	"internal/cloud":   {"cmd", "internal/studio", "internal/server", "internal/oai", "internal/s3", "internal/cloudauth", "internal/cloudworkspace", "internal/cloudpublish"},
}

func dependencyViolations(root string) ([]string, error) {
	var violations []string
	for _, tree := range []string{"cmd", "internal", "plugin"} {
		err := filepath.WalkDir(filepath.Join(root, tree), func(path string, entry fs.DirEntry, err error) error {
			if os.IsNotExist(err) {
				return nil
			}
			if err != nil {
				return err
			}
			if entry.IsDir() || !strings.HasSuffix(path, ".go") {
				return nil
			}
			relative, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			relative = filepath.ToSlash(relative)
			parts := strings.Split(relative, "/")
			owner := strings.Join(parts[:2], "/")
			files := token.NewFileSet()
			source, err := parser.ParseFile(files, path, nil, parser.ImportsOnly)
			if err != nil {
				return err
			}
			for _, spec := range source.Imports {
				target, err := strconv.Unquote(spec.Path.Value)
				if err != nil {
					return err
				}
				blocked := strings.HasPrefix(target, "github.com/vessica-labs/vessica-platform") || strings.HasPrefix(target, "github.com/vessica-labs/vessica-studio-cloud")
				for _, prefix := range forbidden[owner] {
					prefix = module + "/" + prefix
					blocked = blocked || target == prefix || strings.HasPrefix(target, prefix+"/")
				}
				if blocked {
					violations = append(violations, fmt.Sprintf("%s:%d: %s cannot import %s", relative, files.Position(spec.Pos()).Line, owner, target))
				}
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return violations, nil
}

func TestDependencyDirections(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "../.."))
	violations, err := dependencyViolations(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, violation := range violations {
		t.Error(violation)
	}
}

func TestImportParserIgnoresCommentsAndFindsAliasesInTaggedFiles(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "internal", "studio")
	if err := os.MkdirAll(directory, 0755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "example.go")
	harmless := "package studio\n// import \"" + module + "/internal/cloud\"\nimport \"fmt\"\n"
	if err := os.WriteFile(path, []byte(harmless), 0600); err != nil {
		t.Fatal(err)
	}
	found, err := dependencyViolations(root)
	if err != nil || len(found) != 0 {
		t.Fatalf("comment became an import: %v %v", found, err)
	}
	forbidden := "//go:build imaginary\n\npackage studio\nimport remote \"" + module + "/internal/cloud\"\n"
	if err := os.WriteFile(path, []byte(forbidden), 0600); err != nil {
		t.Fatal(err)
	}
	found, err = dependencyViolations(root)
	if err != nil || len(found) != 1 {
		t.Fatalf("aliased import was missed: %v %v", found, err)
	}
}

func TestServiceAndPrivateImportsCannotEnterPublicDomainPackages(t *testing.T) {
	for _, fixture := range []struct{ owner, target string }{
		{"cloud", module + "/internal/studio"},
		{"library", module + "/internal/oai"},
		{"video", module + "/internal/server"},
		{"studio", "github.com/vessica-labs/vessica-platform/client"},
		{"studio", "github.com/vessica-labs/vessica-studio-cloud/service"},
	} {
		t.Run(fixture.owner+"/"+fixture.target, func(t *testing.T) {
			root := t.TempDir()
			directory := filepath.Join(root, "internal", fixture.owner)
			if err := os.MkdirAll(directory, 0755); err != nil {
				t.Fatal(err)
			}
			source := "package " + fixture.owner + "\nimport alias " + strconv.Quote(fixture.target) + "\n"
			if err := os.WriteFile(filepath.Join(directory, "input.go"), []byte(source), 0600); err != nil {
				t.Fatal(err)
			}
			found, err := dependencyViolations(root)
			if err != nil || len(found) != 1 {
				t.Fatalf("prohibited import was missed: %v %v", found, err)
			}
		})
	}
}
