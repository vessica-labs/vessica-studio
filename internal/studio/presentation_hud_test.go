package studio

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPresentationHUDKeepsEditingActionsInEditorRibbon(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "studio.yaml"), "theme_default: default\n")
	writeFile(t, filepath.Join(root, "themes", "default", "theme.css"), ".slide{width:1280px;height:720px}")
	writeFile(t, filepath.Join(root, "decks", "demo", "deck.yaml"), "title: Demo\ntheme: default\n")
	writeFile(t, filepath.Join(root, "decks", "demo", "slides", "0010-a.html"), `<section class="slide"><h1>Demo</h1></section>`)
	studio, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	output, err := studio.Build("demo")
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	html := string(body)
	for _, id := range []string{"hudSticky", "companionbtn", "newslidebtn"} {
		if !strings.Contains(html, `id="`+id+`" data-edit-control style="display:none"`) {
			t.Fatalf("presentation HUD control %s is not edit-only and hidden", id)
		}
	}
	for _, label := range []string{"Sticky note", "Companion", "Knowledge base", "New slide"} {
		if !strings.Contains(html, `data-tip="`+label) {
			t.Fatalf("editor ribbon lost %s", label)
		}
	}
	if !strings.Contains(html, "presentationStart=new URLSearchParams(location.search)") {
		t.Fatal("presentation-mode launch does not suppress automatic editing")
	}
}
