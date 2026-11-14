package studio

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/tdewolff/minify/v2"
	mincss "github.com/tdewolff/minify/v2/css"
	minhtml "github.com/tdewolff/minify/v2/html"
	minjs "github.com/tdewolff/minify/v2/js"
	minsvg "github.com/tdewolff/minify/v2/svg"
)

// Delivery templates are an explicit opt-in host contract. Hosts replace only
// vstd-asset:<digest>:<base64url-path> with manifest-authorized HTTPS URLs.
// They never need to parse or reproduce engine player source.
func releaseDeliveryTemplate(html string, artifacts []ReleaseArtifact) (string, error) {
	ordered := append([]ReleaseArtifact(nil), artifacts...)
	sort.Slice(ordered, func(i, j int) bool { return len(ordered[i].Path) > len(ordered[j].Path) })
	videos, posters, bundles := map[string]string{}, map[string]string{}, map[string]string{}
	for _, a := range ordered {
		if a.Path == "index.html" {
			continue
		}
		token := "vstd-asset:" + a.SHA256 + ":" + base64.RawURLEncoding.EncodeToString([]byte(a.Path))
		html = strings.ReplaceAll(html, "./"+a.Path, token)
		if strings.HasPrefix(a.Path, "assets/bundle/") {
			bundles[strings.TrimSuffix(filepath.Base(a.Path), filepath.Ext(a.Path))] = token
		}
		if strings.HasPrefix(a.Path, "assets/video/") {
			videos[strings.TrimSuffix(filepath.Base(a.Path), filepath.Ext(a.Path))] = token
		}
		if strings.HasPrefix(a.Path, "assets/video-posters/") {
			posters[strings.TrimSuffix(filepath.Base(a.Path), filepath.Ext(a.Path))] = token
		}
	}
	bundleJSON, _ := json.Marshal(bundles)
	html = strings.Replace(html, `const logical='./assets/bundle/'+asset.id+'.zip';`, `const logical=(`+string(bundleJSON)+`)[asset.id]||'';`, 1)
	videoJSON, _ := json.Marshal(videos)
	posterJSON, _ := json.Marshal(posters)
	html = strings.ReplaceAll(html, `const srcFor=id=>'./assets/video/'+id+'.mp4';`, `const srcFor=id=>(`+string(videoJSON)+`)[id]||'';`)
	html = strings.ReplaceAll(html, `const posterFor=id=>'./assets/video-posters/'+id+'.webp';`, `const posterFor=id=>(`+string(posterJSON)+`)[id]||'';`)
	html = strings.ReplaceAll(html, `const posterFor=id=>'./assets/video-posters/'+id+'.jpg';`, `const posterFor=id=>(`+string(posterJSON)+`)[id]||'';`)
	// The host fills this data-only URL. A cookie-authenticated image ping keeps
	// media credentials alive during long presentations without exposing them to JS.
	html = strings.Replace(html, "</body>", `<script>(function(){const url="vstd-session:keepalive";if(!url)return;setInterval(()=>{const image=new Image();image.src=url+'?t='+Date.now();},120000);})();</script></body>`, 1)
	return html, nil
}

func deliveryMinifier() *minify.M {
	m := minify.New()
	m.AddFunc("text/css", mincss.Minify)
	m.AddFunc("application/javascript", minjs.Minify)
	m.AddFunc("text/javascript", minjs.Minify)
	m.Add("text/html", &minhtml.Minifier{KeepDocumentTags: true, KeepEndTags: true, KeepQuotes: true})
	m.AddFunc("image/svg+xml", minsvg.Minify)
	return m
}

const (
	svgNamespace   = "http://www.w3.org/2000/svg"
	xlinkNamespace = "http://www.w3.org/1999/xlink"
)

var officeFillRule = regexp.MustCompile(`^\s*\.(MsftOfcThm_[A-Za-z0-9_]+_Fill_v[0-9]+)\s*\{\s*fill\s*:\s*(#[[:xdigit:]]{6})\s*;?\s*\}\s*`)

// Preserve local fragment references and drop Office fill rules only when every
// affected element already has the same fill as a presentation attribute.
func sanitizeDeliverySVG(source []byte) ([]byte, error) {
	d := xml.NewDecoder(bytes.NewReader(source))
	var out bytes.Buffer
	e := xml.NewEncoder(&out)
	allowed := map[string]bool{}
	for _, name := range strings.Fields("svg g path rect circle ellipse line polyline polygon text tspan defs linearGradient radialGradient stop clipPath mask pattern title desc use symbol filter feGaussianBlur feOffset feBlend feColorMatrix") {
		allowed[name] = true
	}
	depth, skipping := 0, 0
	var rootSeen bool
	var sourceNamespace string
	fills := map[string]string{}
	var styled []svgClassFill
	for {
		token, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		switch v := token.(type) {
		case xml.Directive:
			return nil, fmt.Errorf("SVG directives are forbidden")
		case xml.ProcInst:
			continue
		case xml.Comment:
			continue
		case xml.StartElement:
			depth++
			if skipping > 0 {
				skipping++
				continue
			}
			if depth == 1 {
				if rootSeen || v.Name.Local != "svg" || v.Name.Space != "" && v.Name.Space != svgNamespace {
					return nil, fmt.Errorf("invalid SVG root")
				}
				rootSeen = true
				sourceNamespace = v.Name.Space
			}
			if v.Name.Local == "metadata" {
				skipping = 1
				continue
			}
			if v.Name.Local == "style" && depth == 2 && v.Name.Space == sourceNamespace {
				if err := readOfficeFillRules(d, v, fills); err != nil {
					return nil, err
				}
				depth--
				continue
			}
			if !allowed[v.Name.Local] || v.Name.Space != sourceNamespace {
				return nil, fmt.Errorf("unsupported SVG element")
			}
			// Encode the default namespace once. Use the canonical XLink prefix
			// because the SVG minifier drops attributes with generated prefixes.
			v.Name.Space = ""
			attrs := v.Attr[:0]
			var classes, fill string
			for _, a := range v.Attr {
				if a.Name.Space == "xmlns" || a.Name.Space == "" && a.Name.Local == "xmlns" {
					continue
				}
				if a.Name.Space == "" {
					switch a.Name.Local {
					case "class":
						classes = a.Value
					case "fill":
						fill = a.Value
					}
				}
				value := strings.ToLower(strings.TrimSpace(a.Value))
				name := strings.ToLower(a.Name.Local)
				if strings.HasPrefix(name, "on") || name == "style" || name == "base" || strings.Contains(value, "javascript:") || strings.Contains(value, "data:") || strings.Contains(value, "://") || (name == "href" && !strings.HasPrefix(value, "#")) || strings.Contains(value, "url(") && !strings.HasPrefix(value, "url(#") {
					return nil, fmt.Errorf("active SVG attribute is forbidden")
				}
				if a.Name.Space == xlinkNamespace {
					a.Name = xml.Name{Local: "xlink:" + a.Name.Local}
				}
				attrs = append(attrs, a)
			}
			if depth == 1 {
				attrs = append(attrs,
					xml.Attr{Name: xml.Name{Local: "xmlns"}, Value: svgNamespace},
					xml.Attr{Name: xml.Name{Local: "xmlns:xlink"}, Value: xlinkNamespace},
				)
			}
			if classes != "" {
				styled = append(styled, svgClassFill{classes: classes, fill: fill})
			}
			v.Attr = attrs
			token = v
		case xml.EndElement:
			depth--
			if skipping > 0 {
				skipping--
				continue
			}
			v.Name.Space = ""
			token = v
		default:
			if skipping > 0 {
				continue
			}
		}
		if err := e.EncodeToken(token); err != nil {
			return nil, err
		}
	}
	if !rootSeen || depth != 0 {
		return nil, fmt.Errorf("invalid SVG")
	}
	for _, element := range styled {
		for _, class := range strings.Fields(element.classes) {
			if expected, ok := fills[class]; ok && !strings.EqualFold(strings.TrimSpace(element.fill), expected) {
				return nil, fmt.Errorf("Office SVG stylesheet changes an element's fill")
			}
		}
	}
	if err := e.Flush(); err != nil {
		return nil, err
	}
	if len(fills) != 0 {
		// Keep Office logo path precision after removing the stylesheet.
		return out.Bytes(), nil
	}
	return deliveryMinifier().Bytes("image/svg+xml", out.Bytes())
}

type svgClassFill struct {
	classes string
	fill    string
}

func readOfficeFillRules(d *xml.Decoder, start xml.StartElement, fills map[string]string) error {
	if len(start.Attr) != 0 {
		return fmt.Errorf("unsupported SVG stylesheet attributes")
	}
	var css strings.Builder
read:
	for {
		token, err := d.Token()
		if err != nil {
			return err
		}
		switch v := token.(type) {
		case xml.CharData:
			css.Write(v)
		case xml.EndElement:
			break read
		default:
			return fmt.Errorf("unsupported SVG stylesheet content")
		}
	}
	for remaining := strings.TrimSpace(css.String()); remaining != ""; {
		rule := officeFillRule.FindStringSubmatch(remaining)
		if rule == nil {
			return fmt.Errorf("unsupported SVG stylesheet rule")
		}
		if previous, ok := fills[rule[1]]; ok && !strings.EqualFold(previous, rule[2]) {
			return fmt.Errorf("conflicting Office SVG fill rules")
		}
		fills[rule[1]] = rule[2]
		remaining = remaining[len(rule[0]):]
	}
	return nil
}
