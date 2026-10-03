package studio

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
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
	for _, want := range []string{`name="Card"`, `name="Title"`, `<a:t>Editable title</a:t>`, `name="Curve"`, `<p:pic>`} {
		if !strings.Contains(slide, want) {
			t.Errorf("slide XML missing %q", want)
		}
	}
	if strings.Count(slide, "<p:sp>") < 3 {
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

func pptxParts(t *testing.T, data []byte) map[string]string {
	t.Helper()
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	parts := map[string]string{}
	for _, file := range reader.File {
		stream, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(stream)
		stream.Close()
		if err != nil {
			t.Fatal(err)
		}
		parts[file.Name] = string(body)
	}
	return parts
}

func TestPPTXPreservesSubpathsAndBezierControls(t *testing.T) {
	data, err := BuildPPTX(PPTXDeck{Slides: []PPTXSlide{{Elements: []PPTXElement{{Kind: "path", Fill: "rgba(50, 150, 70, 0.4)", Opacity: .5, Commands: []PPTXPathCommand{
		{Kind: "move", Points: []PPTXPoint{{10, 20}}}, {Kind: "cubic", Points: []PPTXPoint{{15, 5}, {40, 10}, {50, 20}}}, {Kind: "close"},
		{Kind: "move", Points: []PPTXPoint{{80, 80}}}, {Kind: "quad", Points: []PPTXPoint{{85, 60}, {100, 80}}},
	}}}}}})
	if err != nil {
		t.Fatal(err)
	}
	xml := pptxParts(t, data)["ppt/slides/slide1.xml"]
	if strings.Count(xml, "<p:sp>") != 1 || strings.Count(xml, "<a:moveTo>") != 2 || !strings.Contains(xml, "<a:cubicBezTo>") || !strings.Contains(xml, "<a:quadBezTo>") || !strings.Contains(xml, "<a:close/>") {
		t.Fatalf("lost editable path structure: %s", xml)
	}
	if !strings.Contains(xml, `<a:alpha val="20000"/>`) {
		t.Fatal("CSS alpha and inherited opacity were not multiplied")
	}
}

func TestPPTXRoundCornersRotationAndMediaRelationships(t *testing.T) {
	poster := "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII="
	data, err := BuildPPTX(PPTXDeck{Slides: []PPTXSlide{{Elements: []PPTXElement{
		{Kind: "roundRect", W: 200, H: 100, Radius: 10, Fill: "#fff"},
		{Kind: "text", Text: "Vertical", W: 100, H: 20, Rotation: -90},
		{Kind: "video", W: 1280, H: 720, ImageData: poster, VideoData: []byte("mp4-fixture")},
	}}}})
	if err != nil {
		t.Fatal(err)
	}
	parts := pptxParts(t, data)
	xml := parts["ppt/slides/slide1.xml"]
	for _, want := range []string{`fmla="val 10000"`, `rot="16200000"`, `<a:videoFile r:link="rId3"/>`, `p14:media`, `3DE7FCFB9230`, `ppaction://media`} {
		if !strings.Contains(xml, want) {
			t.Errorf("missing %s", want)
		}
	}
	if parts["ppt/media/video1.mp4"] != "mp4-fixture" {
		t.Fatal("media bytes missing")
	}
	rels := parts["ppt/slides/_rels/slide1.xml.rels"]
	if !strings.Contains(rels, `relationships/video" Target="../media/video1.mp4"`) || !strings.Contains(rels, `relationships/media" Target="../media/video1.mp4"`) {
		t.Fatal("video references are not embedded")
	}
	if !strings.Contains(parts["ppt/slideMasters/slideMaster1.xml"], `sldLayoutId id="2147483649"`) {
		t.Fatal("PowerPoint requires unsigned master layout IDs")
	}
}

func TestPPTXTransparentGradientStopsAndDashedCurves(t *testing.T) {
	e := PPTXElement{Kind: "rect", W: 40, H: 20, Opacity: 1, GradientStops: []PPTXGradientStop{{Color: "#21bf61", Offset: 0, Opacity: 1}, {Color: "#21bf61", Offset: 1, Opacity: 0}}, GradientAngle: 90, Stroke: "#21bf61", StrokeWidth: 3, DashArray: []float64{7, 6}, LineCap: "round"}
	xml := shapeXML(e, 2, "Gradient")
	for _, want := range []string{`<a:gradFill`, `<a:alpha val="0"/>`, `ang="5400000"`, `cap="rnd"`, `<a:custDash>`, `d="233333" sp="200000"`} {
		if !strings.Contains(xml, want) {
			t.Errorf("missing %s", want)
		}
	}
	if solidFillXML("rgba(255, 0, 0, 0)", 1) != `<a:noFill/>` {
		t.Fatal("zero CSS alpha became opaque")
	}
}

func TestPPTXVideoWithoutPosterRetainsEmbeddedMedia(t *testing.T) {
	data, err := BuildPPTX(PPTXDeck{Slides: []PPTXSlide{{Elements: []PPTXElement{{Kind: "video", W: 200, H: 100, VideoData: []byte("video-without-poster")}}}}})
	if err != nil {
		t.Fatal(err)
	}
	parts := pptxParts(t, data)
	if parts["ppt/media/video1.mp4"] != "video-without-poster" || !strings.Contains(parts["ppt/slides/slide1.xml"], "p14:media") {
		t.Fatal("optional poster discarded the video")
	}
}

func TestPPTXVideoClickTimingTargetsEveryMediaObject(t *testing.T) {
	media := PPTXElement{Kind: "video", W: 200, H: 100, VideoData: []byte("same-video")}
	data, err := BuildPPTX(PPTXDeck{Slides: []PPTXSlide{{Elements: []PPTXElement{{Kind: "text", Text: "Title"}, media, media}}}})
	if err != nil {
		t.Fatal(err)
	}
	parts := pptxParts(t, data)
	d := xml.NewDecoder(strings.NewReader(parts["ppt/slides/slide1.xml"]))
	ids, targets := map[string]bool{}, map[string]int{}
	commands, starts, mediaNodes := 0, 0, 0
	for {
		token, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		e, ok := token.(xml.StartElement)
		if !ok {
			continue
		}
		for _, attr := range e.Attr {
			if e.Name.Local == "cTn" && attr.Name.Local == "id" {
				if ids[attr.Value] {
					t.Fatal("duplicate animation node ID")
				}
				ids[attr.Value] = true
			}
			if e.Name.Local == "spTgt" && attr.Name.Local == "spid" {
				targets[attr.Value]++
			}
			if e.Name.Local == "cmd" && attr.Name.Local == "cmd" && attr.Value == "playFrom(0.0)" {
				starts++
			}
			if e.Name.Local == "cmd" && attr.Name.Local == "cmd" && attr.Value == "togglePause" {
				commands++
			}
		}
		if e.Name.Local == "cMediaNode" {
			mediaNodes++
		}
	}
	if len(ids) != 22 || targets["3"] != 5 || targets["4"] != 5 || len(targets) != 2 || commands != 2 || starts != 2 || mediaNodes != 2 {
		t.Fatalf("incorrect media activation: timing=%v targets=%v commands=%d media=%d", ids, targets, commands, mediaNodes)
	}
	if parts["ppt/media/video1.mp4"] != "same-video" || parts["ppt/media/video2.mp4"] != "" {
		t.Fatal("repeated video packaged more than once")
	}
}
