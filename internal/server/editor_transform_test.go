package server

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vessica-labs/vessica-studio/internal/studio"
)

func TestEditorTransformRenderAndEdit(t *testing.T) {
	st := testStudio(t)
	snapshot, err := studio.CloudContent(st.Root)
	if err != nil {
		t.Fatal(err)
	}
	input := EditorTransformInput{Deck: "demo", Files: snapshot.Files, Method: "GET", Path: "/d/demo/"}
	rendered, err := TransformEditor(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if rendered.Status != 200 || !strings.Contains(string(rendered.Body), `id="editRibbon"`) {
		t.Fatalf("render: %d", rendered.Status)
	}
	input.Method = "PUT"
	input.Path = "/api/deck/demo/slide/0010-a/fragment"
	input.Body = []byte(`<section class="slide"><h1>Direct cloud edit</h1></section>`)
	edited, err := TransformEditor(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if edited.Status != 200 {
		t.Fatalf("edit: %d %s", edited.Status, edited.Body)
	}
	found := false
	for _, f := range edited.Files {
		if f.Path == "decks/demo/slides/0010-a.html" {
			found = strings.Contains(string(f.Content), "Direct cloud edit")
		}
	}
	if !found {
		t.Fatal("result did not preserve edited snapshot")
	}
	encoded, err := json.Marshal(edited)
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		Files []struct {
			Path    string `json:"path"`
			Content []byte `json:"content"`
		}
	}
	if err := json.Unmarshal(encoded, &wire); err != nil || len(wire.Files) != len(edited.Files) {
		t.Fatal("invalid wire snapshot")
	}
	// A second transform uses only the returned snapshot; no process or worktree
	// state is necessary to render a durable edit after a restart.
	input.Files = edited.Files
	input.Method, input.Path, input.Body = "GET", "/d/demo/", nil
	reopened, err := TransformEditor(context.Background(), input)
	if err != nil || !strings.Contains(string(reopened.Body), "Direct cloud edit") {
		t.Fatal("reopen lost edit", err)
	}
	for _, p := range []string{"/api/deck/demo/export.pdf", "/api/asset/video", "/api/realtime/token", "/api/deck/other/slide/a", "/library/../../etc/passwd", "/api/deck/demo/slide/0010-a/refresh-link"} {
		input.Path = p
		for _, method := range []string{"GET", "POST", "PUT"} {
			input.Method = method
			if got, err := TransformEditor(context.Background(), input); err == nil && got.Status != 404 {
				t.Fatalf("allowed execution or unsafe route %s", p)
			}
		}
	}
}

func TestEditorTransformAllowsReadOnlyVideoPlayback(t *testing.T) {
	if !transformRoute("GET", "/assets/video/vstd-upload-850815525", "demo") {
		t.Fatal("editor transform rejected the engine video route")
	}
	for _, method := range []string{"POST", "PUT", "DELETE"} {
		if transformRoute(method, "/assets/video/vstd-upload-850815525", "demo") {
			t.Fatalf("editor transform allowed %s video route", method)
		}
	}
}
func TestEditorTransformFileBackedDelta(t *testing.T) {
	st := testStudio(t)
	in := EditorTransformInput{Root: st.Root, Delta: true, Deck: "demo", Method: "GET", Path: "/api/me"}
	result, err := TransformEditor(context.Background(), in)
	if err != nil || result.Status != 200 || !result.Delta || len(result.Files) != 0 {
		t.Fatalf("read delta: %+v %v", result, err)
	}
	in.Method, in.Path = "PUT", "/api/deck/demo/slide/0010-a/fragment"
	in.Body = []byte(`<section class="slide"><h1>Changed delta</h1></section>`)
	result, err = TransformEditor(context.Background(), in)
	if err != nil || result.Status != 200 || !result.Delta || len(result.Files) == 0 {
		t.Fatalf("write delta: %+v %v", result, err)
	}
	for _, file := range result.Files {
		if !strings.HasPrefix(file.Path, "decks/demo/") {
			t.Fatalf("unchanged file in delta: %s", file.Path)
		}
	}
	original, err := studio.CloudContent(st.Root)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range original.Files {
		if strings.Contains(string(f.Content), "Changed delta") {
			t.Fatal("read-only input root mutated")
		}
	}
	in.Files = original.Files
	if _, err = TransformEditor(context.Background(), in); err == nil {
		t.Fatal("ambiguous root accepted")
	}
}

func TestEditorTransformLargeFileBackedDeck(t *testing.T) {
	st := testStudio(t)
	if err := os.MkdirAll(filepath.Join(st.Root, "library", "img"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a", "b", "c", "d"} {
		if err := os.WriteFile(filepath.Join(st.Root, "library", "img", name+".png"), make([]byte, 11<<20), 0600); err != nil {
			t.Fatal(err)
		}
	}
	in := EditorTransformInput{Root: st.Root, Delta: true, Deck: "demo", Method: "GET", Path: "/d/demo/"}
	rendered, err := TransformEditor(context.Background(), in)
	if err != nil || rendered.Status != 200 || len(rendered.Files) != 0 || !strings.Contains(string(rendered.Body), `id="editRibbon"`) {
		t.Fatalf("large render: status=%d files=%d err=%v", rendered.Status, len(rendered.Files), err)
	}
	in.Method, in.Path = "PUT", "/api/deck/demo/slide/0010-a/fragment"
	in.Body = []byte(`<section class="slide"><h1>Large deck edit</h1></section>`)
	edited, err := TransformEditor(context.Background(), in)
	if err != nil || edited.Status != 200 || len(edited.Files) == 0 {
		t.Fatalf("large edit: status=%d files=%d err=%v", edited.Status, len(edited.Files), err)
	}
	for _, f := range edited.Files {
		if strings.HasPrefix(f.Path, "library/") {
			t.Fatalf("unchanged media copied to delta: %s", f.Path)
		}
	}
}

func TestEditorTransformCloudAudience(t *testing.T) {
	st := testStudio(t)
	snapshot, err := studio.CloudContent(st.Root)
	if err != nil {
		t.Fatal(err)
	}
	link := "https://studio.example/s/ABCD2345"
	input := EditorTransformInput{Deck: "demo", Files: snapshot.Files, Method: "GET", Path: "/d/demo/", AudienceURL: &link}
	result, err := TransformEditor(context.Background(), input)
	if err != nil || result.Status != 200 || !strings.Contains(string(result.Body), `"audience_url":"https://studio.example/s/ABCD2345"`) {
		t.Fatalf("render: %v %d", err, result.Status)
	}
	input.Path = "/api/deck/demo/share-qr.png?ttl=168"
	result, err = TransformEditor(context.Background(), input)
	if err != nil || result.Status != 200 || result.Headers["content-type"] != "image/png" {
		t.Fatalf("QR: %v %+v", err, result.Headers)
	}
	link = ""
	result, err = TransformEditor(context.Background(), input)
	if err != nil || result.Status != 404 {
		t.Fatalf("disabled QR: %v %d", err, result.Status)
	}
	link = "javascript:alert(1)"
	if _, err = TransformEditor(context.Background(), input); err == nil {
		t.Fatal("unsafe URL accepted")
	}
}
