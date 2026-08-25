package studio

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// PPTXDeck is a browser-measured, presentation-neutral description of a deck.
// The server obtains it from the rendered DOM; this package owns the OOXML
// serialization so PowerPoint export remains part of the studio domain.
type PPTXDeck struct {
	Title  string      `json:"title"`
	Slides []PPTXSlide `json:"slides"`
}

type PPTXSlide struct {
	ID       string        `json:"id"`
	Elements []PPTXElement `json:"elements"`
}

type PPTXPoint struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

// PPTXElement maps one rendered DOM/SVG object to one or more editable
// PowerPoint objects. Kind is text, rect, roundRect, ellipse, line, path, or
// image. Path becomes editable line segments rather than a flattened picture.
type PPTXElement struct {
	Kind        string      `json:"kind"`
	Name        string      `json:"name,omitempty"`
	X           float64     `json:"x"`
	Y           float64     `json:"y"`
	W           float64     `json:"w"`
	H           float64     `json:"h"`
	Rotation    float64     `json:"rotation,omitempty"`
	Text        string      `json:"text,omitempty"`
	FontFamily  string      `json:"font_family,omitempty"`
	FontSize    float64     `json:"font_size,omitempty"`
	Bold        bool        `json:"bold,omitempty"`
	Italic      bool        `json:"italic,omitempty"`
	Align       string      `json:"align,omitempty"`
	VAlign      string      `json:"valign,omitempty"`
	NoWrap      bool        `json:"nowrap,omitempty"`
	Color       string      `json:"color,omitempty"`
	Fill        string      `json:"fill,omitempty"`
	Gradient    []string    `json:"gradient,omitempty"`
	Stroke      string      `json:"stroke,omitempty"`
	StrokeWidth float64     `json:"stroke_width,omitempty"`
	Opacity     float64     `json:"opacity,omitempty"`
	Points      []PPTXPoint `json:"points,omitempty"`
	ImageData   string      `json:"image_data,omitempty"`
}

const (
	pptxSlideCX = int64(12192000) // 13.333 in, 16:9
	pptxSlideCY = int64(6858000)  // 7.5 in
)

func xmlText(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '&':
			b.WriteString("&amp;")
		case '<':
			b.WriteString("&lt;")
		case '>':
			b.WriteString("&gt;")
		case '"':
			b.WriteString("&quot;")
		case '\'':
			b.WriteString("&apos;")
		default:
			if r == '\t' || r == '\n' || r == '\r' || r >= 0x20 {
				b.WriteRune(r)
			}
		}
	}
	return b.String()
}

func pxEMU(v float64) int64 { return int64(math.Round(v * 9525)) }

var cssRGBRe = regexp.MustCompile(`(?i)rgba?\(\s*([0-9.]+)[, ]+\s*([0-9.]+)[, ]+\s*([0-9.]+)`)

func pptxColor(value, fallback string) string {
	v := strings.TrimSpace(value)
	if strings.HasPrefix(v, "#") {
		h := strings.TrimPrefix(v, "#")
		if len(h) == 3 {
			h = string([]byte{h[0], h[0], h[1], h[1], h[2], h[2]})
		}
		if len(h) >= 6 {
			return strings.ToUpper(h[:6])
		}
	}
	if m := cssRGBRe.FindStringSubmatch(v); m != nil {
		var rgb [3]int
		for i := range rgb {
			f, _ := strconv.ParseFloat(m[i+1], 64)
			rgb[i] = max(0, min(255, int(math.Round(f))))
		}
		return fmt.Sprintf("%02X%02X%02X", rgb[0], rgb[1], rgb[2])
	}
	return fallback
}

func alphaXML(opacity float64) string {
	if opacity <= 0 || opacity >= .999 {
		return ""
	}
	return fmt.Sprintf(`<a:alpha val="%d"/>`, int(math.Round(opacity*100000)))
}

func solidFillXML(color string, opacity float64) string {
	if color == "" || strings.Contains(color, "transparent") || strings.Contains(color, "rgba(0, 0, 0, 0)") {
		return `<a:noFill/>`
	}
	return `<a:solidFill><a:srgbClr val="` + pptxColor(color, "FFFFFF") + `">` + alphaXML(opacity) + `</a:srgbClr></a:solidFill>`
}

func fillXML(e PPTXElement) string {
	if len(e.Gradient) >= 2 {
		return `<a:gradFill rotWithShape="1"><a:gsLst><a:gs pos="0"><a:srgbClr val="` + pptxColor(e.Gradient[0], "FFFFFF") + `">` + alphaXML(e.Opacity) + `</a:srgbClr></a:gs><a:gs pos="100000"><a:srgbClr val="` + pptxColor(e.Gradient[len(e.Gradient)-1], "FFFFFF") + `">` + alphaXML(e.Opacity) + `</a:srgbClr></a:gs></a:gsLst><a:lin ang="5400000" scaled="1"/></a:gradFill>`
	}
	return solidFillXML(e.Fill, e.Opacity)
}

func lineXML(color string, width float64, opacity float64) string {
	if color == "" || width <= 0 || strings.Contains(color, "transparent") {
		return `<a:ln><a:noFill/></a:ln>`
	}
	return fmt.Sprintf(`<a:ln w="%d"><a:solidFill><a:srgbClr val="%s">%s</a:srgbClr></a:solidFill><a:prstDash val="solid"/></a:ln>`, pxEMU(width), pptxColor(color, "000000"), alphaXML(opacity))
}

func xfrmXML(e PPTXElement) string {
	rot := ""
	if math.Abs(e.Rotation) > .01 {
		rot = fmt.Sprintf(` rot="%d"`, int(math.Round(e.Rotation*60000)))
	}
	return fmt.Sprintf(`<a:xfrm%s><a:off x="%d" y="%d"/><a:ext cx="%d" cy="%d"/></a:xfrm>`, rot, pxEMU(e.X), pxEMU(e.Y), max(int64(1), pxEMU(e.W)), max(int64(1), pxEMU(e.H)))
}

func shapeXML(e PPTXElement, id int, name string) string {
	geom := e.Kind
	if geom != "ellipse" && geom != "roundRect" {
		geom = "rect"
	}
	return fmt.Sprintf(`<p:sp><p:nvSpPr><p:cNvPr id="%d" name="%s"/><p:cNvSpPr/><p:nvPr/></p:nvSpPr><p:spPr>%s<a:prstGeom prst="%s"><a:avLst/></a:prstGeom>%s%s</p:spPr></p:sp>`, id, xmlText(name), xfrmXML(e), geom, fillXML(e), lineXML(e.Stroke, e.StrokeWidth, e.Opacity))
}

func textParagraphXML(text string, e PPTXElement) string {
	fontSize := e.FontSize
	if fontSize <= 0 {
		fontSize = 18
	}
	// CSS px to PowerPoint points, represented in hundredths of a point.
	sz := max(100, int(math.Round(fontSize*75)))
	font := strings.TrimSpace(strings.Split(e.FontFamily, ",")[0])
	font = strings.Trim(font, ` "'`)
	if font == "" {
		font = "Arial"
	}
	align := map[string]string{"center": "ctr", "right": "r", "justify": "just"}[e.Align]
	if align == "" {
		align = "l"
	}
	b, i := "0", "0"
	if e.Bold {
		b = "1"
	}
	if e.Italic {
		i = "1"
	}
	return fmt.Sprintf(`<a:p><a:pPr algn="%s"/><a:r><a:rPr lang="en-US" sz="%d" b="%s" i="%s"><a:solidFill><a:srgbClr val="%s">%s</a:srgbClr></a:solidFill><a:latin typeface="%s"/></a:rPr><a:t>%s</a:t></a:r><a:endParaRPr lang="en-US" sz="%d"/></a:p>`, align, sz, b, i, pptxColor(e.Color, "111111"), alphaXML(e.Opacity), xmlText(font), xmlText(text), sz)
}

func textXML(e PPTXElement, id int, name string) string {
	anchor := map[string]string{"middle": "ctr", "center": "ctr", "bottom": "b"}[e.VAlign]
	if anchor == "" {
		anchor = "t"
	}
	var paras strings.Builder
	lines := strings.Split(strings.ReplaceAll(e.Text, "\r\n", "\n"), "\n")
	for _, line := range lines {
		paras.WriteString(textParagraphXML(line, e))
	}
	wrap := "square"
	if e.NoWrap {
		wrap = "none"
	}
	return fmt.Sprintf(`<p:sp><p:nvSpPr><p:cNvPr id="%d" name="%s"/><p:cNvSpPr txBox="1"/><p:nvPr/></p:nvSpPr><p:spPr>%s<a:prstGeom prst="rect"><a:avLst/></a:prstGeom><a:noFill/><a:ln><a:noFill/></a:ln></p:spPr><p:txBody><a:bodyPr wrap="%s" lIns="0" tIns="0" rIns="0" bIns="0" anchor="%s"/><a:lstStyle/>%s</p:txBody></p:sp>`, id, xmlText(name), xfrmXML(e), wrap, anchor, paras.String())
}

func lineShapeXML(x1, y1, x2, y2 float64, e PPTXElement, id int, name string) string {
	x, y := math.Min(x1, x2), math.Min(y1, y2)
	w, h := math.Abs(x2-x1), math.Abs(y2-y1)
	flipH, flipV := "", ""
	if x2 < x1 {
		flipH = ` flipH="1"`
	}
	if y2 < y1 {
		flipV = ` flipV="1"`
	}
	if e.StrokeWidth <= 0 {
		e.StrokeWidth = 1
	}
	if e.Stroke == "" {
		e.Stroke = e.Fill
	}
	return fmt.Sprintf(`<p:sp><p:nvSpPr><p:cNvPr id="%d" name="%s"/><p:cNvSpPr/><p:nvPr/></p:nvSpPr><p:spPr><a:xfrm%s%s><a:off x="%d" y="%d"/><a:ext cx="%d" cy="%d"/></a:xfrm><a:prstGeom prst="line"><a:avLst/></a:prstGeom><a:noFill/>%s</p:spPr></p:sp>`, id, xmlText(name), flipH, flipV, pxEMU(x), pxEMU(y), max(int64(1), pxEMU(w)), max(int64(1), pxEMU(h)), lineXML(e.Stroke, e.StrokeWidth, e.Opacity))
}

func decodePPTXImage(data string) ([]byte, string, bool) {
	comma := strings.IndexByte(data, ',')
	if !strings.HasPrefix(data, "data:image/") || comma < 0 {
		return nil, "", false
	}
	mime := data[len("data:image/"):comma]
	if semi := strings.IndexByte(mime, ';'); semi >= 0 {
		mime = mime[:semi]
	}
	ext := "png"
	if mime == "jpeg" || mime == "jpg" {
		ext = "jpeg"
	}
	b, err := base64.StdEncoding.DecodeString(data[comma+1:])
	return b, ext, err == nil && len(b) > 0
}

type pptxPart struct {
	name string
	data []byte
}

type pptxBuild struct {
	parts    []pptxPart
	mediaSeq int
}

func (b *pptxBuild) add(name, data string) { b.parts = append(b.parts, pptxPart{name, []byte(data)}) }

func (b *pptxBuild) slide(slide PPTXSlide, number int) (string, string) {
	var shapes, rels strings.Builder
	rels.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/slideLayout" Target="../slideLayouts/slideLayout1.xml"/>`)
	id, relID := 2, 2
	for idx, e := range slide.Elements {
		name := e.Name
		if name == "" {
			name = fmt.Sprintf("%s %d", e.Kind, idx+1)
		}
		switch e.Kind {
		case "text":
			if strings.TrimSpace(e.Text) != "" {
				shapes.WriteString(textXML(e, id, name))
				id++
			}
		case "line":
			if len(e.Points) >= 2 {
				shapes.WriteString(lineShapeXML(e.Points[0].X, e.Points[0].Y, e.Points[1].X, e.Points[1].Y, e, id, name))
			} else {
				shapes.WriteString(lineShapeXML(e.X, e.Y, e.X+e.W, e.Y+e.H, e, id, name))
			}
			id++
		case "path":
			for p := 1; p < len(e.Points); p++ {
				shapes.WriteString(lineShapeXML(e.Points[p-1].X, e.Points[p-1].Y, e.Points[p].X, e.Points[p].Y, e, id, fmt.Sprintf("%s segment %d", name, p)))
				id++
			}
		case "image":
			img, ext, ok := decodePPTXImage(e.ImageData)
			if !ok {
				continue
			}
			b.mediaSeq++
			media := fmt.Sprintf("image%d.%s", b.mediaSeq, ext)
			b.parts = append(b.parts, pptxPart{"ppt/media/" + media, img})
			rels.WriteString(fmt.Sprintf(`<Relationship Id="rId%d" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/image" Target="../media/%s"/>`, relID, media))
			shapes.WriteString(fmt.Sprintf(`<p:pic><p:nvPicPr><p:cNvPr id="%d" name="%s"/><p:cNvPicPr><a:picLocks noChangeAspect="1"/></p:cNvPicPr><p:nvPr/></p:nvPicPr><p:blipFill><a:blip r:embed="rId%d"/><a:stretch><a:fillRect/></a:stretch></p:blipFill><p:spPr>%s<a:prstGeom prst="rect"><a:avLst/></a:prstGeom><a:ln><a:noFill/></a:ln></p:spPr></p:pic>`, id, xmlText(name), relID, xfrmXML(e)))
			id++
			relID++
		default:
			shapes.WriteString(shapeXML(e, id, name))
			id++
		}
	}
	rels.WriteString(`</Relationships>`)
	xml := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><p:sld xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships" xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main"><p:cSld name="` + xmlText(slide.ID) + `"><p:spTree><p:nvGrpSpPr><p:cNvPr id="1" name=""/><p:cNvGrpSpPr/><p:nvPr/></p:nvGrpSpPr><p:grpSpPr><a:xfrm><a:off x="0" y="0"/><a:ext cx="0" cy="0"/><a:chOff x="0" y="0"/><a:chExt cx="0" cy="0"/></a:xfrm></p:grpSpPr>` + shapes.String() + `</p:spTree></p:cSld><p:clrMapOvr><a:masterClrMapping/></p:clrMapOvr></p:sld>`
	return xml, rels.String()
}

// BuildPPTX serializes a measured deck as editable PresentationML. It does
// not render full-slide screenshots; every emitted picture is one source DOM
// image and text/SVG/layout objects remain native shapes.
func BuildPPTX(deck PPTXDeck) ([]byte, error) {
	if len(deck.Slides) == 0 {
		return nil, fmt.Errorf("deck has no exportable slides")
	}
	b := &pptxBuild{}
	b.add("_rels/.rels", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="ppt/presentation.xml"/><Relationship Id="rId2" Type="http://schemas.openxmlformats.org/package/2006/relationships/metadata/core-properties" Target="docProps/core.xml"/><Relationship Id="rId3" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/extended-properties" Target="docProps/app.xml"/></Relationships>`)
	b.add("docProps/core.xml", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><cp:coreProperties xmlns:cp="http://schemas.openxmlformats.org/package/2006/metadata/core-properties" xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:dcterms="http://purl.org/dc/terms/" xmlns:dcmitype="http://purl.org/dc/dcmitype/" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"><dc:title>`+xmlText(deck.Title)+`</dc:title><dc:creator>Vessica Studio</dc:creator><cp:lastModifiedBy>Vessica Studio</cp:lastModifiedBy><dcterms:created xsi:type="dcterms:W3CDTF">`+time.Now().UTC().Format(time.RFC3339)+`</dcterms:created></cp:coreProperties>`)
	b.add("docProps/app.xml", fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Properties xmlns="http://schemas.openxmlformats.org/officeDocument/2006/extended-properties" xmlns:vt="http://schemas.openxmlformats.org/officeDocument/2006/docPropsVTypes"><Application>Vessica Studio</Application><PresentationFormat>Widescreen</PresentationFormat><Slides>%d</Slides><Notes>0</Notes><HiddenSlides>0</HiddenSlides><Company>Vessica</Company><AppVersion>16.0000</AppVersion></Properties>`, len(deck.Slides)))

	var ids, presRels strings.Builder
	presRels.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/slideMaster" Target="slideMasters/slideMaster1.xml"/>`)
	for i, slide := range deck.Slides {
		xml, rels := b.slide(slide, i+1)
		b.add(fmt.Sprintf("ppt/slides/slide%d.xml", i+1), xml)
		b.add(fmt.Sprintf("ppt/slides/_rels/slide%d.xml.rels", i+1), rels)
		ids.WriteString(fmt.Sprintf(`<p:sldId id="%d" r:id="rId%d"/>`, 256+i, i+2))
		presRels.WriteString(fmt.Sprintf(`<Relationship Id="rId%d" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/slide" Target="slides/slide%d.xml"/>`, i+2, i+1))
	}
	presRels.WriteString(`</Relationships>`)
	b.add("ppt/_rels/presentation.xml.rels", presRels.String())
	b.add("ppt/presentation.xml", fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><p:presentation xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships" xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main"><p:sldMasterIdLst><p:sldMasterId id="2147483648" r:id="rId1"/></p:sldMasterIdLst><p:sldIdLst>%s</p:sldIdLst><p:sldSz cx="%d" cy="%d" type="screen16x9"/><p:notesSz cx="6858000" cy="9144000"/><p:defaultTextStyle/></p:presentation>`, ids.String(), pptxSlideCX, pptxSlideCY))

	b.add("ppt/slideMasters/_rels/slideMaster1.xml.rels", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/slideLayout" Target="../slideLayouts/slideLayout1.xml"/><Relationship Id="rId2" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/theme" Target="../theme/theme1.xml"/></Relationships>`)
	b.add("ppt/slideMasters/slideMaster1.xml", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><p:sldMaster xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships" xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main"><p:cSld name="Vessica"><p:spTree><p:nvGrpSpPr><p:cNvPr id="1" name=""/><p:cNvGrpSpPr/><p:nvPr/></p:nvGrpSpPr><p:grpSpPr><a:xfrm><a:off x="0" y="0"/><a:ext cx="0" cy="0"/><a:chOff x="0" y="0"/><a:chExt cx="0" cy="0"/></a:xfrm></p:grpSpPr></p:spTree></p:cSld><p:clrMap accent1="accent1" accent2="accent2" accent3="accent3" accent4="accent4" accent5="accent5" accent6="accent6" bg1="lt1" bg2="lt2" folHlink="folHlink" hlink="hlink" tx1="dk1" tx2="dk2"/><p:sldLayoutIdLst><p:sldLayoutId id="1" r:id="rId1"/></p:sldLayoutIdLst><p:txStyles><p:titleStyle/><p:bodyStyle/><p:otherStyle/></p:txStyles></p:sldMaster>`)
	b.add("ppt/slideLayouts/_rels/slideLayout1.xml.rels", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/slideMaster" Target="../slideMasters/slideMaster1.xml"/></Relationships>`)
	b.add("ppt/slideLayouts/slideLayout1.xml", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><p:sldLayout xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships" xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main" type="blank" preserve="1"><p:cSld name="Blank"><p:spTree><p:nvGrpSpPr><p:cNvPr id="1" name=""/><p:cNvGrpSpPr/><p:nvPr/></p:nvGrpSpPr><p:grpSpPr><a:xfrm><a:off x="0" y="0"/><a:ext cx="0" cy="0"/><a:chOff x="0" y="0"/><a:chExt cx="0" cy="0"/></a:xfrm></p:grpSpPr></p:spTree></p:cSld><p:clrMapOvr><a:masterClrMapping/></p:clrMapOvr></p:sldLayout>`)
	b.add("ppt/theme/theme1.xml", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><a:theme xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" name="Vessica"><a:themeElements><a:clrScheme name="Vessica"><a:dk1><a:srgbClr val="0C2B15"/></a:dk1><a:lt1><a:srgbClr val="FFFFFF"/></a:lt1><a:dk2><a:srgbClr val="14251A"/></a:dk2><a:lt2><a:srgbClr val="F8FAF8"/></a:lt2><a:accent1><a:srgbClr val="20E3AC"/></a:accent1><a:accent2><a:srgbClr val="96F977"/></a:accent2><a:accent3><a:srgbClr val="4F8A5B"/></a:accent3><a:accent4><a:srgbClr val="6D7E70"/></a:accent4><a:accent5><a:srgbClr val="B8D7BE"/></a:accent5><a:accent6><a:srgbClr val="E3FDDB"/></a:accent6><a:hlink><a:srgbClr val="0563C1"/></a:hlink><a:folHlink><a:srgbClr val="954F72"/></a:folHlink></a:clrScheme><a:fontScheme name="Vessica"><a:majorFont><a:latin typeface="Arial"/><a:ea typeface=""/><a:cs typeface=""/></a:majorFont><a:minorFont><a:latin typeface="Arial"/><a:ea typeface=""/><a:cs typeface=""/></a:minorFont></a:fontScheme><a:fmtScheme name="Vessica"><a:fillStyleLst><a:solidFill><a:schemeClr val="phClr"/></a:solidFill><a:solidFill><a:schemeClr val="phClr"/></a:solidFill><a:solidFill><a:schemeClr val="phClr"/></a:solidFill></a:fillStyleLst><a:lnStyleLst><a:ln w="9525"><a:solidFill><a:schemeClr val="phClr"/></a:solidFill></a:ln><a:ln w="25400"><a:solidFill><a:schemeClr val="phClr"/></a:solidFill></a:ln><a:ln w="38100"><a:solidFill><a:schemeClr val="phClr"/></a:solidFill></a:ln></a:lnStyleLst><a:effectStyleLst><a:effectStyle><a:effectLst/></a:effectStyle><a:effectStyle><a:effectLst/></a:effectStyle><a:effectStyle><a:effectLst/></a:effectStyle></a:effectStyleLst><a:bgFillStyleLst><a:solidFill><a:schemeClr val="phClr"/></a:solidFill><a:solidFill><a:schemeClr val="phClr"/></a:solidFill><a:solidFill><a:schemeClr val="phClr"/></a:solidFill></a:bgFillStyleLst></a:fmtScheme></a:themeElements></a:theme>`)
	b.add("ppt/presProps.xml", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><p:presentationPr xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships" xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main"/>`)
	b.add("ppt/viewProps.xml", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><p:viewPr xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships" xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main"><p:normalViewPr/><p:slideViewPr/><p:notesTextViewPr/><p:gridSpacing cx="78028800" cy="78028800"/></p:viewPr>`)
	b.add("ppt/tableStyles.xml", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><a:tblStyleLst xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" def="{5C22544A-7EE6-4342-B048-85BDC9FD1C3A}"/>`)

	var overrides strings.Builder
	for i := range deck.Slides {
		overrides.WriteString(fmt.Sprintf(`<Override PartName="/ppt/slides/slide%d.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.slide+xml"/>`, i+1))
	}
	b.add("[Content_Types].xml", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Default Extension="png" ContentType="image/png"/><Default Extension="jpeg" ContentType="image/jpeg"/><Override PartName="/ppt/presentation.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.presentation.main+xml"/><Override PartName="/ppt/slideMasters/slideMaster1.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.slideMaster+xml"/><Override PartName="/ppt/slideLayouts/slideLayout1.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.slideLayout+xml"/><Override PartName="/ppt/theme/theme1.xml" ContentType="application/vnd.openxmlformats-officedocument.theme+xml"/><Override PartName="/ppt/presProps.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.presProps+xml"/><Override PartName="/ppt/viewProps.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.viewProps+xml"/><Override PartName="/ppt/tableStyles.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.tableStyles+xml"/><Override PartName="/docProps/core.xml" ContentType="application/vnd.openxmlformats-package.core-properties+xml"/><Override PartName="/docProps/app.xml" ContentType="application/vnd.openxmlformats-officedocument.extended-properties+xml"/>`+overrides.String()+`</Types>`)

	var out bytes.Buffer
	zw := zip.NewWriter(&out)
	for _, part := range b.parts {
		w, err := zw.Create(part.name)
		if err != nil {
			return nil, err
		}
		if _, err := w.Write(part.data); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// BuildRasterPPTX packages one 1280x720 PNG per slide as a full-bleed image.
// This is the visual-fidelity export: browser-rendered CSS, SVG, typography,
// gradients, and effects remain pixel-identical instead of being approximated
// with PowerPoint's different layout and text engines.
func BuildRasterPPTX(title string, pngs [][]byte) ([]byte, error) {
	if len(pngs) == 0 {
		return nil, fmt.Errorf("deck has no rendered slides")
	}
	deck := PPTXDeck{Title: title, Slides: make([]PPTXSlide, len(pngs))}
	for i, png := range pngs {
		if len(png) == 0 {
			return nil, fmt.Errorf("rendered slide %d is empty", i+1)
		}
		deck.Slides[i] = PPTXSlide{
			ID: fmt.Sprintf("slide-%d", i+1),
			Elements: []PPTXElement{{
				Kind:      "image",
				Name:      fmt.Sprintf("HTML slide %d", i+1),
				X:         0,
				Y:         0,
				W:         1280,
				H:         720,
				Opacity:   1,
				ImageData: "data:image/png;base64," + base64.StdEncoding.EncodeToString(png),
			}},
		}
	}
	return BuildPPTX(deck)
}
