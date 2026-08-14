package studio

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"strings"
	"testing"
)

func TestBuildPPTXEmitsEditableObjects(t *testing.T) {
	deck := PPTXDeck{Title: "Editable export", Slides: []PPTXSlide{{ID: "0010-test", Elements: []PPTXElement{
		{Kind: "rect", Name: "Card", X: 20, Y: 30, W: 400, H: 180, Fill: "rgb(240, 250, 242)", Stroke: "#20e3ac", StrokeWidth: 2, Opacity: 1},
		{Kind: "text", Name: "Title", X: 40, Y: 50, W: 350, H: 50, Text: "Editable title", FontFamily: "Arial", FontSize: 32, Bold: true, Color: "#0c2b15", Opacity: 1},
		{Kind: "path", Name: "Curve", Stroke: "#20e3ac", StrokeWidth: 3, Opacity: 1, Points: []PPTXPoint{{X: 60, Y: 300}, {X: 180, Y: 240}, {X: 340, Y: 190}}},
		{Kind: "image", Name: "Picture", X: 500, Y: 100, W: 100, H: 100, ImageData: "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII="},
	}}}}
	b, err := BuildPPTX(deck)
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatal(err)
	}
	parts := map[string]string{}
	for _, f := range zr.File {
		r, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(r)
		r.Close()
		if err != nil {
			t.Fatal(err)
		}
		parts[f.Name] = string(data)
	}
	slide := parts["ppt/slides/slide1.xml"]
	for _, want := range []string{`name="Card"`, `name="Title"`, `<a:t>Editable title</a:t>`, `name="Curve segment 1"`, `<p:pic>`} {
		if !strings.Contains(slide, want) {
			t.Errorf("slide XML missing %q", want)
		}
	}
	if strings.Count(slide, "<p:sp>") < 4 {
		t.Fatalf("expected multiple editable native shapes, slide XML:\n%s", slide)
	}
	if _, ok := parts["ppt/media/image1.png"]; !ok {
		t.Fatal("individual image object was not embedded")
	}
}

func TestBuildRasterPPTXEmitsOneFullBleedImagePerSlide(t *testing.T) {
	png := []byte{137, 80, 78, 71, 13, 10, 26, 10, 0, 0, 0, 0}
	b, err := BuildRasterPPTX("Visual exact", [][]byte{png, png})
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatal(err)
	}
	parts := map[string]string{}
	for _, f := range zr.File {
		r, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(r)
		r.Close()
		if err != nil {
			t.Fatal(err)
		}
		parts[f.Name] = string(data)
	}
	for i := 1; i <= 2; i++ {
		slide := parts[fmt.Sprintf("ppt/slides/slide%d.xml", i)]
		if strings.Count(slide, "<p:pic>") != 1 || !strings.Contains(slide, `name="HTML slide `) {
			t.Fatalf("slide %d is not a single visual-exact image: %s", i, slide)
		}
		if !strings.Contains(slide, `<a:off x="0" y="0"/><a:ext cx="12192000" cy="6858000"/>`) {
			t.Fatalf("slide %d image is not full bleed: %s", i, slide)
		}
	}
}
