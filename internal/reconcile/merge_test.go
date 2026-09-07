package reconcile

import (
	"bytes"
	"strings"
	"testing"
)

func TestHTMLPreservesSVGNamespaces(t *testing.T) {
	base := `<section class="slide"><svg viewBox="0 0 10 10"><use id="icon" xlink:href="#old" fill="red"/></svg></section>`
	got := string(MergeFile("slide.html", []byte(base), []byte(strings.Replace(base, "#old", "#new", 1)), []byte(strings.Replace(base, `fill="red"`, `fill="blue"`, 1)), false))
	if !strings.Contains(got, `xlink:href="#new"`) || !strings.Contains(got, `fill="blue"`) || !strings.Contains(got, `viewBox=`) {
		t.Fatal(got)
	}
}

func TestNormalizationIsStableAndPreservesIdentity(t *testing.T) {
	first := NormalizeHTML("slide.html", []byte(`<section class="slide"><h1>Title</h1></section>`))
	if !bytes.Equal(first, NormalizeHTML("slide.html", first)) {
		t.Fatal("normalization changed existing identities")
	}
	changed := bytes.Replace(first, []byte("<h1"), []byte("<p>New</p><h1"), 1)
	normalized := NormalizeHTML("slide.html", changed)
	if !bytes.Contains(normalized, first[bytes.Index(first, []byte("<h1")):bytes.Index(first, []byte("</h1>"))]) {
		t.Fatal(string(normalized))
	}
}

func TestDeleteVersusEditPreservesTheWholeSlidePair(t *testing.T) {
	base := Snapshot{Files: []File{{Path: "decks/demo/slides/0010-a.html", Content: []byte("base")}, {Path: "decks/demo/slides/0010-a.md", Content: []byte("companion")}}}
	current := Snapshot{Files: append([]File{}, base.Files...)}
	current.Files[0].Content = []byte("human")
	result, err := Merge(Input{Base: base, Incoming: Snapshot{}, Current: current, PreferCurrent: true})
	if err != nil || len(result.Files) != 2 {
		t.Fatalf("orphaned pair: %+v %v", result, err)
	}
}

func TestConcurrentLogAppendsBothSurvive(t *testing.T) {
	base := "## Log\n- initial\n"
	got := MergeFile("slide.md", []byte(base), []byte(base+"- human\n"), []byte(base+"- agent\n"), false)
	if !strings.Contains(string(got), "- human") || !strings.Contains(string(got), "- agent") {
		t.Fatal(string(got))
	}
}

func TestInlineStylesKeepDataURLSemicolons(t *testing.T) {
	base := `<section class="slide"><div id="a" style="background:url('data:image/svg+xml;base64,AAAA');color:red">Base</div></section>`
	got := MergeFile("slide.html", []byte(base), []byte(strings.Replace(base, "Base", "Human", 1)), []byte(strings.Replace(base, "color:red", "color:blue", 1)), false)
	if !strings.Contains(string(got), "data:image/svg+xml;base64,AAAA") || !strings.Contains(string(got), "color:blue") || !strings.Contains(string(got), "Human") {
		t.Fatal(string(got))
	}
}

func TestHTMLMergesTextAndStyleOnSameObject(t *testing.T) {
	base := `<section class="slide"><h1 data-vstd-id="title" style="color:red;font-size:40px">Old</h1></section>`
	local := strings.Replace(base, "Old", "New", 1)
	remote := strings.Replace(base, "color:red", "color:blue", 1)
	result := MergeFile("slide.html", []byte(base), []byte(local), []byte(remote), false)
	if !strings.Contains(string(result), ">New</h1>") || !strings.Contains(string(result), "color:blue") {
		t.Fatal(string(result))
	}
}
func TestAgentCannotReplaceConcurrentHumanText(t *testing.T) {
	base := []byte(`<section class="slide"><h1 id="title">Old</h1></section>`)
	agent := []byte(strings.Replace(string(base), "Old", "Agent", 1))
	human := []byte(strings.Replace(string(base), "Old", "Human", 1))
	if got := string(MergeFile("slide.html", base, agent, human, true)); !strings.Contains(got, "Human") {
		t.Fatal(got)
	}
}
func TestMergeSnapshotPreservesIndependentFilesAndDeletion(t *testing.T) {
	base := Snapshot{Files: []File{{Path: "a.md", Content: []byte("a")}, {Path: "b.md", Content: []byte("b")}}}
	ours := Snapshot{Files: []File{{Path: "a.md", Content: []byte("A")}, {Path: "b.md", Content: []byte("b")}}}
	theirs := Snapshot{Files: []File{{Path: "a.md", Content: []byte("a")}}}
	out, err := Merge(Input{Base: base, Incoming: ours, Current: theirs})
	if err != nil || len(out.Files) != 1 || string(out.Files[0].Content) != "A" {
		t.Fatal(out, err)
	}
}
func TestMarkdownSectionsMerge(t *testing.T) {
	base := "## Intent\nOld\n## Notes\nOld\n"
	ours := strings.Replace(base, "Old", "Intent", 1)
	theirs := "## Intent\nOld\n## Notes\nNotes\n"
	got := string(MergeFile("slide.md", []byte(base), []byte(ours), []byte(theirs), false))
	if !strings.Contains(got, "Intent\n") || !strings.Contains(got, "Notes\nNotes") {
		t.Fatal(got)
	}
}
func TestOrderCombinesMoveAndInsertion(t *testing.T) {
	got := mergeOrder([]string{"a", "b", "c"}, []string{"c", "a", "b"}, []string{"a", "x", "b", "c"})
	if strings.Join(got, ",") != "c,a,x,b" {
		t.Fatal(got)
	}
}

func TestOrderPreservesIndependentMoves(t *testing.T) {
	got := mergeOrder([]string{"a", "b", "c", "d"}, []string{"b", "a", "c", "d"}, []string{"a", "b", "d", "c"})
	if strings.Join(got, ",") != "b,a,d,c" {
		t.Fatal(got)
	}
}
