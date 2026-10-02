package bundle

import (
	"archive/zip"
	"bytes"
	"github.com/vessica-labs/vessica-studio/internal/library"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func archive(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var b bytes.Buffer
	w := zip.NewWriter(&b)
	for n, s := range files {
		f, e := w.Create(n)
		if e != nil {
			t.Fatal(e)
		}
		f.Write([]byte(s))
	}
	if e := w.Close(); e != nil {
		t.Fatal(e)
	}
	return b.Bytes()
}
func TestInspectAndIngest(t *testing.T) {
	data := archive(t, map[string]string{"index.html": "<script>window.demo=true</script>", "data/a.txt": "hello"})
	a, e := Inspect(data, "index.html")
	if e != nil || a.FileCount != 2 || a.Bytes != int64(len(data)) {
		t.Fatalf("%+v %v", a, e)
	}
	dir := t.TempDir()
	file := filepath.Join(dir, "app.zip")
	os.WriteFile(file, data, 0644)
	lib := filepath.Join(dir, "library")
	a, e = Ingest(lib, file, "demo", "index.html", "")
	if e != nil {
		t.Fatal(e)
	}
	m, e := library.Load(lib)
	if e != nil || len(m.Bundles) != 1 {
		t.Fatal(e)
	}
	if e = Verify(data, a); e != nil {
		t.Fatal(e)
	}
	a.Hash = strings.Repeat("0", 64)
	if Verify(data, a) == nil {
		t.Fatal("corrupt metadata accepted")
	}
}
func TestRejectHostileArchives(t *testing.T) {
	for _, files := range []map[string]string{
		{"../escape": "x", "index.html": "ok"}, {"index.html": "ok", "INDEX.html": "collision"}, {"missing.html": "no entry"}, {"index.html": "<script src='remote.js'></script>"}, {"index.html": "<script type='importmap'>{}</script>"}, {"index.html": "<iframe></iframe>"},
	} {
		if _, e := Inspect(archive(t, files), "index.html"); e == nil {
			t.Fatalf("accepted %+v", files)
		}
	}
	data := archive(t, map[string]string{"index.html": strings.Repeat("x", (8<<20)+1)})
	if _, e := Inspect(data, "index.html"); e == nil {
		t.Fatal("oversize HTML accepted")
	}
}
