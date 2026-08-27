package studio

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// openSectionRe matches a slide fragment's root <section ...> opening tag.
var openSectionRe = regexp.MustCompile(`(?is)<section\b[^>]*>`)

var printVideoRe = regexp.MustCompile(`(?is)<video\b[^>]*\bdata-vstd-video="([^"]+)"[^>]*>`)

// SlideParked reports whether a fragment's root <section> carries
// data-parked — the player's "unused" state. Hidden (data-hidden) slides are
// still part of the deck; parked ones are not.
func SlideParked(frag string) bool {
	tag := openSectionRe.FindString(frag)
	return tag != "" && strings.Contains(tag, "data-parked")
}

// markActive adds the "active" class to the root <section> so theme rules
// keyed on .slide.active (visibility, entrance styling) apply in the static
// print page, where no player JS runs.
func markActive(frag string) string {
	tag := openSectionRe.FindString(frag)
	if tag == "" {
		return frag
	}
	var repl string
	if strings.Contains(tag, `class="`) {
		repl = strings.Replace(tag, `class="`, `class="active `, 1)
	} else {
		repl = strings.Replace(tag, "<section", `<section class="active"`, 1)
	}
	return strings.Replace(frag, tag, repl, 1)
}

// addPrintVideoPosters makes static PDF/PPTX output match the player, whose
// runtime assigns the same poster URL before a video is played. Without it,
// browser print captures a black video rectangle.
func addPrintVideoPosters(frag string) string {
	return printVideoRe.ReplaceAllStringFunc(frag, func(tag string) string {
		if strings.Contains(strings.ToLower(tag), " poster=") {
			return tag
		}
		match := printVideoRe.FindStringSubmatch(tag)
		if len(match) < 2 {
			return tag
		}
		return strings.TrimSuffix(tag, ">") + ` poster="/assets/video/` + htmlEscape(match[1]) + `/poster">`
	})
}

// printBaseCSS precedes theme.css so themes can override it — it mirrors the
// player's base so slides inherit the same body font in print as on stage.
const printBaseCSS = `
*{box-sizing:border-box;margin:0;padding:0}
html,body{margin:0;padding:0;background:#fff;font-family:var(--sans,"Trebuchet MS",Verdana,sans-serif)}
`

// printCSS pins each slide to a fixed 1280x720 page. It is appended after the
// theme so its display override beats the theme's `.slide{display:none}`.
const printCSS = `
@page{size:1280px 720px;margin:0}
.vstd-page{position:relative;width:1280px;height:720px;overflow:hidden;page-break-after:always;break-after:page}
.vstd-page:last-child{page-break-after:auto;break-after:auto}
.vstd-page .slide{display:block!important;position:absolute;inset:0;transform:none;animation:none}
.vstd-page .notes{display:none!important}
`

// BuildPrintHTML assembles a static print-ready page for PDF export: theme +
// deck CSS with one page per slide, in deck order, including hidden slides
// but excluding parked ("unused") ones. Unlike Build it embeds no player —
// the result renders deterministically under a headless browser's
// print-to-PDF. Returns the HTML and the page count.
func (s *Studio) BuildPrintHTML(deck string) (string, int, error) {
	html, ids, err := s.BuildPrintHTMLForSlides(deck, nil)
	return html, len(ids), err
}

// BuildPrintHTMLForSlides builds the same deterministic print document for a
// validated subset. A nil selection means every non-parked slide; a non-nil
// selection is emitted in deck order rather than caller order.
func (s *Studio) BuildPrintHTMLForSlides(deck string, selected []string) (string, []string, error) {
	meta, err := s.LoadDeckMeta(deck)
	if err != nil {
		return "", nil, err
	}
	themeDir := s.ThemeDir(meta.Theme)
	themeCSS, err := os.ReadFile(filepath.Join(themeDir, "theme.css"))
	if err != nil {
		return "", nil, fmt.Errorf("theme %q: %w", meta.Theme, err)
	}
	deckCSS, _ := os.ReadFile(filepath.Join(s.DeckDir(deck), "deck.css"))

	ids, err := s.SlideIDs(deck)
	if err != nil {
		return "", nil, err
	}
	wanted := map[string]bool{}
	if selected != nil {
		if len(selected) == 0 {
			return "", nil, fmt.Errorf("select at least one slide")
		}
		for _, id := range selected {
			if !ValidSlideID(id) || wanted[id] {
				return "", nil, fmt.Errorf("invalid or duplicate slide %q", id)
			}
			wanted[id] = true
		}
	}
	var pages strings.Builder
	var exported []string
	for _, id := range ids {
		if selected != nil && !wanted[id] {
			continue
		}
		frag, _, err := s.ReadSlide(deck, id)
		if err != nil {
			return "", nil, err
		}
		if SlideParked(frag) {
			if selected != nil {
				return "", nil, fmt.Errorf("slide %q is parked and cannot be exported", id)
			}
			continue
		}
		exported = append(exported, id)
		delete(wanted, id)
		pages.WriteString(fmt.Sprintf(`<div class="vstd-page" id="vstd-page-%d">`, len(exported)))
		pages.WriteString(markActive(addPrintVideoPosters(stampFragment(frag, id))))
		pages.WriteString("</div>\n")
	}
	if len(wanted) != 0 {
		return "", nil, fmt.Errorf("one or more selected slides do not exist")
	}
	if len(exported) == 0 {
		return "", nil, fmt.Errorf("deck %q has no exportable slides", deck)
	}

	var b strings.Builder
	b.WriteString("<!DOCTYPE html>\n<html><head><meta charset=\"utf-8\"><title>")
	b.WriteString(htmlEscape(meta.Title))
	b.WriteString("</title>\n<style>\n")
	b.WriteString(printBaseCSS)
	b.Write(themeCSS)
	b.WriteString("\n/* deck overrides */\n")
	b.Write(deckCSS)
	b.WriteString("\n/* print export */\n")
	b.WriteString(printCSS)
	b.WriteString("</style>\n</head>\n<body>\n")
	b.WriteString(pages.String())
	b.WriteString("</body></html>\n")
	return b.String(), exported, nil
}
