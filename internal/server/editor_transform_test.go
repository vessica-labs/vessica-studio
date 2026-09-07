package server

import (
	"context"
	"encoding/json"
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
			if _, err := TransformEditor(context.Background(), input); err == nil {
				t.Fatalf("allowed execution or unsafe route %s", p)
			}
		}
	}
}
