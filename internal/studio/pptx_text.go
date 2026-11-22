package studio

import (
	"fmt"
	"math"
	"strings"
)

// PPTXParagraph represents an authored line break or block within a text box.
// PowerPoint wraps the runs within each paragraph using the text box width.
type PPTXParagraph struct {
	Runs       []PPTXTextRun `json:"runs"`
	LineHeight float64       `json:"line_height,omitempty"`
}

type PPTXTextRun struct {
	Text          string  `json:"text"`
	FontFamily    string  `json:"font_family,omitempty"`
	FontSize      float64 `json:"font_size,omitempty"`
	Bold          bool    `json:"bold,omitempty"`
	Italic        bool    `json:"italic,omitempty"`
	Underline     bool    `json:"underline,omitempty"`
	Color         string  `json:"color,omitempty"`
	Opacity       float64 `json:"opacity,omitempty"`
	LetterSpacing float64 `json:"letter_spacing,omitempty"`
}

func textRunPropertiesXML(tag string, run PPTXTextRun) string {
	color := run.Color
	if color == "" {
		color = "#111111"
	}
	size := run.FontSize
	if size <= 0 {
		size = 18
	}
	// CSS px to PowerPoint points, represented in hundredths of a point.
	sz := max(100, min(400000, int(math.Round(size*75))))
	font := strings.Trim(strings.TrimSpace(strings.Split(run.FontFamily, ",")[0]), ` "'`)
	if font == "" {
		font = "Arial"
	}
	underline := ""
	if run.Underline {
		underline = ` u="sng"`
	}
	return fmt.Sprintf(`<a:%s lang="en-US" sz="%d" b="%t" i="%t"%s spc="%d" kern="0">%s<a:latin typeface="%s"/></a:%s>`,
		tag, sz, run.Bold, run.Italic, underline, int(math.Round(run.LetterSpacing*75)), solidFillXML(color, run.Opacity), xmlText(font), tag)
}

func textParagraphXML(paragraph PPTXParagraph, defaults PPTXTextRun, align string) string {
	var out strings.Builder
	spacing := `<a:spcPct val="100000"/>`
	if paragraph.LineHeight > 0 {
		spacing = fmt.Sprintf(`<a:spcPts val="%d"/>`, min(158400, int(math.Round(paragraph.LineHeight*75))))
	}
	fmt.Fprintf(&out, `<a:p><a:pPr algn="%s"><a:lnSpc>%s</a:lnSpc><a:spcBef><a:spcPts val="0"/></a:spcBef><a:spcAft><a:spcPts val="0"/></a:spcAft>%s</a:pPr>`,
		align, spacing, textRunPropertiesXML("defRPr", defaults))
	for _, run := range paragraph.Runs {
		fmt.Fprintf(&out, `<a:r>%s<a:t>%s</a:t></a:r>`, textRunPropertiesXML("rPr", run), xmlText(run.Text))
	}
	out.WriteString(textRunPropertiesXML("endParaRPr", defaults))
	out.WriteString(`</a:p>`)
	return out.String()
}

func textXML(e PPTXElement, id int, name string) string {
	// Chromium Range bounds and Office's first-line leading differ by font.
	// Legacy/unmeasured and unknown fonts retain their existing positioning.
	if e.Measured && math.Abs(e.Rotation) < .01 {
		switch strings.ToLower(e.FontFamily) {
		case "henderson bcg sans", "henderson bcg serif":
			e.Y -= e.FontSize * .067
		case "arial":
			e.Y -= e.FontSize * .075
		}
	}

	anchor := map[string]string{"middle": "ctr", "center": "ctr", "bottom": "b"}[e.VAlign]
	if anchor == "" {
		anchor = "t"
	}
	align := map[string]string{"center": "ctr", "right": "r", "justify": "just"}[e.Align]
	if align == "" {
		align = "l"
	}
	defaults := PPTXTextRun{
		FontFamily: e.FontFamily, FontSize: e.FontSize, Bold: e.Bold, Italic: e.Italic,
		Color: e.Color, Opacity: e.Opacity, LetterSpacing: e.LetterSpacing,
	}
	var paras strings.Builder
	if len(e.Paragraphs) > 0 {
		for _, paragraph := range e.Paragraphs {
			paras.WriteString(textParagraphXML(paragraph, defaults, align))
		}
	} else {
		for _, line := range strings.Split(strings.ReplaceAll(e.Text, "\r\n", "\n"), "\n") {
			run := defaults
			run.Text = line
			paras.WriteString(textParagraphXML(PPTXParagraph{Runs: []PPTXTextRun{run}}, defaults, align))
		}
	}
	wrap := "square"
	if e.NoWrap {
		wrap = "none"
	}
	return fmt.Sprintf(`<p:sp><p:nvSpPr><p:cNvPr id="%d" name="%s"/><p:cNvSpPr txBox="1"/><p:nvPr/></p:nvSpPr><p:spPr>%s<a:prstGeom prst="rect"><a:avLst/></a:prstGeom><a:noFill/><a:ln><a:noFill/></a:ln></p:spPr><p:txBody><a:bodyPr wrap="%s" lIns="0" tIns="0" rIns="0" bIns="0" anchor="%s"><a:noAutofit/></a:bodyPr><a:lstStyle/>%s</p:txBody></p:sp>`, id, xmlText(name), xfrmXML(e), wrap, anchor, paras.String())
}
