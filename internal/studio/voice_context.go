package studio

import (
	"fmt"
	"strings"

	"golang.org/x/net/html"
)

// VoicePage describes a slide using the same page numbering as gotoNum in the
// player. Parked slides have no presentation page number; hidden slides do.
type VoicePage struct {
	ID      string `json:"id"`
	Page    int    `json:"page"`
	Title   string `json:"title"`
	Summary string `json:"summary"`
	Hidden  bool   `json:"hidden"`
	Parked  bool   `json:"parked"`
}

type VoiceCurrentPage struct {
	VoicePage
	Companion string `json:"companion"`
}

type VoicePresentationContext struct {
	Current VoiceCurrentPage `json:"current"`
	Pages   []VoicePage      `json:"pages"`
}

// VoiceContext reads the authoritative file contract, never generated HTML.
// Callers must restrict this response to presenters: companions can be private.
func (s *Studio) VoiceContext(deck, currentID string) (VoicePresentationContext, error) {
	var out VoicePresentationContext
	if !ValidDeckName(deck) || !ValidSlideID(currentID) {
		return out, fmt.Errorf("invalid deck/slide id")
	}
	ids, err := s.SlideIDs(deck)
	if err != nil {
		return out, err
	}
	page, found := 0, false
	for _, id := range ids {
		fragment, companion, err := s.ReadSlide(deck, id)
		if err != nil {
			return out, err
		}
		entry := voicePage(id, fragment, companion)
		if !entry.Parked {
			page++
			entry.Page = page
		}
		out.Pages = append(out.Pages, entry)
		if id == currentID {
			out.Current = VoiceCurrentPage{VoicePage: entry, Companion: companion}
			found = true
		}
	}
	if !found {
		return out, fmt.Errorf("slide not found: %s", currentID)
	}
	return out, nil
}

func voicePage(id, fragment, companion string) VoicePage {
	out := VoicePage{ID: id}
	doc, _ := html.Parse(strings.NewReader(fragment))
	var visible []string
	var menuTitle string
	titleRank := 0
	var visit func(*html.Node)
	visit = func(n *html.Node) {
		if n.Type == html.ElementNode {
			if n.Data == "script" || n.Data == "style" || n.Data == "template" {
				return
			}
			attrs := make(map[string]string)
			for _, a := range n.Attr {
				attrs[a.Key] = a.Val
			}
			classes := " " + strings.Join(strings.Fields(attrs["class"]), " ") + " "
			if strings.Contains(classes, " notes ") {
				return
			}
			if strings.Contains(classes, " slide ") {
				_, out.Parked = attrs["data-parked"]
				_, out.Hidden = attrs["data-hidden"]
				menuTitle = attrs["data-menu"]
			}
			_, explicit := attrs["data-slide-title"]
			rank := 0
			switch {
			case explicit || strings.Contains(classes, " s-title "):
				rank = 4
			case n.Data == "h1":
				rank = 3
			case n.Data == "h2":
				rank = 2
			case strings.Contains(classes, " serif "):
				rank = 1
			}
			if rank > titleRank {
				if title := voiceNodeText(n); title != "" {
					out.Title = title
					titleRank = rank
				}
			}
		}
		if n.Type == html.TextNode {
			visible = append(visible, n.Data)
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			visit(child)
		}
	}
	if doc != nil {
		visit(doc)
	}
	if out.Title == "" {
		out.Title = menuTitle
	}
	if out.Title == "" {
		out.Title = id
	}
	out.Title = voiceExcerpt(out.Title, 160)
	out.Summary = voiceCompanionSummary(companion)
	if out.Summary == "" {
		out.Summary = voiceExcerpt(strings.Join(visible, " "), 360)
	}
	return out
}

func voiceNodeText(n *html.Node) string {
	if n.Type == html.TextNode {
		return n.Data
	}
	var parts []string
	for child := n.FirstChild; child != nil; child = child.NextSibling {
		parts = append(parts, voiceNodeText(child))
	}
	return strings.Join(strings.Fields(strings.Join(parts, " ")), " ")
}

func voiceCompanionSummary(markdown string) string {
	_, body, _ := companionFrontmatter(markdown)
	sections := make(map[string][]string)
	heading := ""
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			level := len(trimmed) - len(strings.TrimLeft(trimmed, "#"))
			if level <= 2 {
				heading = strings.ToLower(strings.TrimSpace(strings.TrimLeft(trimmed, "#")))
			}
			continue
		}
		sections[heading] = append(sections[heading], trimmed)
	}
	var selected []string
	for _, heading := range []string{"intent", "summary", "key ideas", "narrative", "talk track"} {
		selected = append(selected, sections[heading]...)
	}
	return voiceExcerpt(strings.Join(selected, " "), 360)
}

func voiceExcerpt(text string, limit int) string {
	runes := []rune(strings.Join(strings.Fields(text), " "))
	if len(runes) > limit {
		return string(runes[:limit]) + "…"
	}
	return string(runes)
}
