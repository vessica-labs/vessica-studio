package studio

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func transferStudio(t *testing.T) *Studio {
	t.Helper()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "studio.yaml"), "theme_default: default\n")
	writeFile(t, filepath.Join(root, "themes/default/theme.css"), ".slide{}")
	for _, deck := range []string{"source", "target"} {
		writeFile(t, filepath.Join(root, "decks", deck, "deck.yaml"), "title: "+deck+"\ntheme: default\n")
		writeFile(t, filepath.Join(root, "decks", deck, "deck.css"), "/* "+deck+" */\n")
	}
	writeFile(t, filepath.Join(root, "decks/source/slides/0010-one.html"), `<section class="slide"><h1>One</h1><img src="../sources/evidence.pdf"></section>`)
	writeFile(t, filepath.Join(root, "decks/source/slides/0010-one.md"), "---\nslide: 0010-one\nattachments:\n  - name: evidence.pdf\n    path: sources/evidence.pdf\n---\n## Intent\nOne\n")
	writeFile(t, filepath.Join(root, "decks/source/sources/evidence.pdf"), "evidence")
	writeFile(t, filepath.Join(root, "decks/source/slides/0020-two.html"), `<section class="slide"><h1>Two</h1></section>`)
	writeFile(t, filepath.Join(root, "decks/source/slides/0020-two.md"), "## Intent\nTwo\n")
	writeFile(t, filepath.Join(root, "decks/target/slides/0010-cover.html"), `<section class="slide"><h1>Target</h1></section>`)
	writeFile(t, filepath.Join(root, "decks/target/slides/0010-cover.md"), "## Intent\nTarget\n")
	st, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func TestTransferSlidesCopiesBatchAndAttachments(t *testing.T) {
	st := transferStudio(t)
	res, err := st.TransferSlides(SlideTransferRequest{SourceDeck: "source", TargetDeck: "target", SlideIDs: []string{"0020-two", "0010-one"}, Mode: "copy"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(res.SlideIDs, ",") != "0020-one,0030-two" {
		t.Fatalf("target ids=%v", res.SlideIDs)
	}
	_, companion, err := st.ReadSlide("target", "0020-one")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(companion, "sources/evidence.pdf") || !strings.Contains(companion, "sources/attachment-") {
		t.Fatalf("attachment path was not rewritten: %s", companion)
	}
	fragment, _, _ := st.ReadSlide("target", "0020-one")
	if strings.Contains(fragment, "sources/evidence.pdf") || !strings.Contains(fragment, "sources/attachment-") {
		t.Fatalf("fragment attachment path was not rewritten: %s", fragment)
	}
	attachments, err := st.CompanionAttachments("target", "0020-one")
	if err != nil || len(attachments) != 1 {
		t.Fatalf("attachments=%v err=%v", attachments, err)
	}
	if _, err := os.Stat(filepath.Join(st.DeckDir("target"), filepath.FromSlash(attachments[0].Path))); err != nil {
		t.Fatal(err)
	}
	if st.IsLinkedSlide("target", "0020-one") {
		t.Fatal("copied slide unexpectedly linked")
	}
}

func TestLinkedSlideRefreshFallbackAndDetach(t *testing.T) {
	st := transferStudio(t)
	res, err := st.TransferSlides(SlideTransferRequest{SourceDeckID: "deck_source", SourceDeck: "source", SourceDeckTitle: "Source", TargetDeck: "target", SlideIDs: []string{"0010-one"}, Mode: "link"})
	if err != nil {
		t.Fatal(err)
	}
	id := res.SlideIDs[0]
	if !st.IsLinkedSlide("target", id) {
		t.Fatal("linked slide metadata missing")
	}
	if err := st.WriteFragment("source", "0010-one", `<section class="slide"><h1>Changed</h1></section>`); err != nil {
		t.Fatal(err)
	}
	changed, err := st.RefreshSlideLink("target", id)
	if err != nil || !changed {
		t.Fatalf("refresh changed=%v err=%v", changed, err)
	}
	fragment, _, _ := st.ReadSlide("target", id)
	if !strings.Contains(fragment, "Changed") {
		t.Fatalf("snapshot not refreshed: %s", fragment)
	}
	if err := os.WriteFile(filepath.Join(st.DeckDir("source"), "sources/evidence.pdf"), []byte("new evidence"), 0o644); err != nil {
		t.Fatal(err)
	}
	changed, err = st.RefreshSlideLink("target", id)
	if err != nil || !changed {
		t.Fatalf("attachment refresh changed=%v err=%v", changed, err)
	}
	if err := os.Remove(st.SlidePath("source", "0010-one", ".html")); err != nil {
		t.Fatal(err)
	}
	if _, err := st.RefreshSlideLink("target", id); err == nil {
		t.Fatal("missing source unexpectedly refreshed")
	}
	fragment, _, _ = st.ReadSlide("target", id)
	if !strings.Contains(fragment, "Changed") {
		t.Fatal("last snapshot was not retained")
	}
	if err := st.DetachSlideLink("target", id); err != nil || st.IsLinkedSlide("target", id) {
		t.Fatalf("detach err=%v linked=%v", err, st.IsLinkedSlide("target", id))
	}
}

func TestMoveSlideCarriesLinkMetadata(t *testing.T) {
	st := transferStudio(t)
	res, err := st.TransferSlides(SlideTransferRequest{SourceDeck: "source", TargetDeck: "target", SlideIDs: []string{"0010-one"}, Mode: "link"})
	if err != nil {
		t.Fatal(err)
	}
	newID, err := st.MoveSlide("target", res.SlideIDs[0], "")
	if err != nil {
		t.Fatal(err)
	}
	if !st.IsLinkedSlide("target", newID) {
		t.Fatalf("link metadata did not move to %s", newID)
	}
}

func TestMoveSourceSlideRewritesLinkedReferences(t *testing.T) {
	st := transferStudio(t)
	res, err := st.TransferSlides(SlideTransferRequest{SourceDeck: "source", TargetDeck: "target", SlideIDs: []string{"0010-one"}, Mode: "link"})
	if err != nil {
		t.Fatal(err)
	}
	newSourceID, err := st.MoveSlide("source", "0010-one", "0020-two")
	if err != nil {
		t.Fatal(err)
	}
	link, err := st.ReadSlideLink("target", res.SlideIDs[0])
	if err != nil {
		t.Fatal(err)
	}
	if link.SourceSlide != newSourceID {
		t.Fatalf("link source=%q want=%q", link.SourceSlide, newSourceID)
	}
}

func TestBatchTransferFailureLeavesTargetUnchanged(t *testing.T) {
	st := transferStudio(t)
	link := "version: 1\nsource_deck: target\nsource_slide: 0010-cover\nsource_fragment_hash: x\nlast_refreshed_at: 2026-08-26T00:00:00Z\n"
	if err := os.WriteFile(st.LinkPath("source", "0020-two"), []byte(link), 0o644); err != nil {
		t.Fatal(err)
	}
	before, _ := st.SlideIDs("target")
	if _, err := st.TransferSlides(SlideTransferRequest{SourceDeck: "source", TargetDeck: "target", SlideIDs: []string{"0010-one", "0020-two"}, Mode: "link"}); err == nil {
		t.Fatal("expected link-chain rejection")
	}
	after, _ := st.SlideIDs("target")
	if strings.Join(before, ",") != strings.Join(after, ",") {
		t.Fatalf("target changed after failed batch: before=%v after=%v", before, after)
	}
}
