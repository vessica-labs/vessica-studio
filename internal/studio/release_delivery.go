package studio

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"path/filepath"
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
	videos, posters := map[string]string{}, map[string]string{}
	for _, a := range ordered {
		if a.Path == "index.html" {
			continue
		}
		token := "vstd-asset:" + a.SHA256 + ":" + base64.RawURLEncoding.EncodeToString([]byte(a.Path))
		html = strings.ReplaceAll(html, "./"+a.Path, token)
		if strings.HasPrefix(a.Path, "assets/video/") {
			videos[strings.TrimSuffix(filepath.Base(a.Path), filepath.Ext(a.Path))] = token
		}
		if strings.HasPrefix(a.Path, "assets/video-posters/") {
			posters[strings.TrimSuffix(filepath.Base(a.Path), filepath.Ext(a.Path))] = token
		}
	}
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

// Reject active/external SVG features rather than trying to repair ambiguous
// untrusted markup. Local fragment paint/clip references remain available.
func sanitizeDeliverySVG(source []byte) ([]byte, error) {
	d := xml.NewDecoder(bytes.NewReader(source))
	var out bytes.Buffer
	e := xml.NewEncoder(&out)
	allowed := map[string]bool{}
	for _, name := range strings.Fields("svg g path rect circle ellipse line polyline polygon text tspan defs linearGradient radialGradient stop clipPath mask pattern title desc use symbol filter feGaussianBlur feOffset feBlend feColorMatrix") {
		allowed[name] = true
	}
	depth, skipping := 0, 0
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
			if v.Name.Local == "metadata" {
				skipping = 1
				continue
			}
			if !allowed[v.Name.Local] || (v.Name.Space != "" && v.Name.Space != "http://www.w3.org/2000/svg") {
				return nil, fmt.Errorf("unsupported SVG element")
			}
			for _, a := range v.Attr {
				value := strings.ToLower(strings.TrimSpace(a.Value))
				name := strings.ToLower(a.Name.Local)
				if strings.HasPrefix(name, "on") || name == "style" || name == "base" || strings.Contains(value, "javascript:") || strings.Contains(value, "data:") || strings.Contains(value, "://") && name != "xmlns" || (name == "href" && !strings.HasPrefix(value, "#")) || strings.Contains(value, "url(") && !strings.HasPrefix(value, "url(#") {
					return nil, fmt.Errorf("active SVG attribute is forbidden")
				}
			}
		case xml.EndElement:
			depth--
			if skipping > 0 {
				skipping--
				continue
			}
		default:
			if skipping > 0 {
				continue
			}
		}
		if err := e.EncodeToken(token); err != nil {
			return nil, err
		}
	}
	if depth != 0 {
		return nil, fmt.Errorf("invalid SVG")
	}
	if err := e.Flush(); err != nil {
		return nil, err
	}
	return deliveryMinifier().Bytes("image/svg+xml", out.Bytes())
}
