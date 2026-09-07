package reconcile

import (
	"bytes"
	"fmt"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
	"strings"
)

// Node identity is explicit when available. Positional fallback is restricted to
// existing fragments; the editor assigns persistent IDs before creating edits.
func parseHTML(data []byte) (any, error) {
	nodes, err := html.ParseFragment(bytes.NewReader(data), &html.Node{Type: html.ElementNode, Data: "div", DataAtom: atom.Div})
	if err != nil {
		return nil, err
	}
	root := &html.Node{Type: html.ElementNode, Data: "div"}
	for _, n := range nodes {
		root.AppendChild(n)
	}
	var encode func(*html.Node) any
	encode = func(n *html.Node) any {
		attrs := map[string]any{}
		for _, a := range n.Attr {
			key := a.Key
			if a.Namespace != "" {
				key = a.Namespace + ":" + a.Key
			}
			if a.Key == "style" {
				m := map[string]any{}
				for _, p := range styleDeclarations(a.Val) {
					k, v, ok := strings.Cut(p, ":")
					if ok {
						m[strings.TrimSpace(k)] = strings.TrimSpace(v)
					}
				}
				attrs[key] = m
			} else {
				attrs[key] = a.Val
			}
		}
		children := map[string]any{}
		order := []any{}
		counts := map[string]int{}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			key := ""
			for _, a := range c.Attr {
				if a.Key == "data-vstd-id" {
					key = a.Key + ":" + a.Val
					break
				}
				if a.Key == "id" {
					key = "id:" + a.Val
				}
			}
			if key == "" {
				counts[c.Data]++
				if c.Type == html.TextNode {
					key = fmt.Sprintf("text:%d", len(order))
				} else {
					key = fmt.Sprintf("%s:%d", c.Data, counts[c.Data])
				}
			}
			if _, duplicate := children[key]; duplicate {
				key = fmt.Sprintf("%s:%d", key, len(order))
			}
			children[key] = encode(c)
			order = append(order, key)
		}
		return map[string]any{"type": int(n.Type), "data": n.Data, "namespace": n.Namespace, "attrs": attrs, "children": children, "order": order}
	}
	return encode(root), nil
}

// Semicolons inside quoted strings and url()/other functions are values, not
// declaration separators (notably embedded SVG/data URLs).
func styleDeclarations(style string) []string {
	var out []string
	start, depth := 0, 0
	var quote byte
	escaped := false
	for i := 0; i < len(style); i++ {
		c := style[i]
		if escaped {
			escaped = false
			continue
		}
		if c == '\\' {
			escaped = true
			continue
		}
		if quote != 0 {
			if c == quote {
				quote = 0
			}
			continue
		}
		if c == '\'' || c == '"' {
			quote = c
			continue
		}
		if c == '(' {
			depth++
		}
		if c == ')' && depth > 0 {
			depth--
		}
		if c == ';' && depth == 0 {
			out = append(out, style[start:i])
			start = i + 1
		}
	}
	return append(out, style[start:])
}
func mergeHTML(base, ours, theirs []byte) ([]byte, bool) {
	ours = restoreHTMLIdentities(base, ours)
	theirs = restoreHTMLIdentities(base, theirs)
	b, e := parseHTML(base)
	if e != nil {
		return nil, false
	}
	o, e := parseHTML(ours)
	if e != nil {
		return nil, false
	}
	t, e := parseHTML(theirs)
	if e != nil {
		return nil, false
	}
	var decode func(any) *html.Node
	decode = func(v any) *html.Node {
		m, ok := v.(map[string]any)
		if !ok {
			return nil
		}
		n := &html.Node{Type: html.NodeType(m["type"].(int)), Data: m["data"].(string), Namespace: m["namespace"].(string)}
		n.DataAtom = atom.Lookup([]byte(n.Data))
		attrs := m["attrs"].(map[string]any)
		for _, k := range sortedKeys(attrs) {
			value := attrs[k]
			s, ok := value.(string)
			if !ok {
				if styles, yes := value.(map[string]any); yes {
					var p []string
					for _, prop := range sortedKeys(styles) {
						p = append(p, prop+":"+fmt.Sprint(styles[prop]))
					}
					s = strings.Join(p, ";")
				}
			}
			attribute := html.Attribute{Key: k, Val: s}
			if ns, local, ok := strings.Cut(k, ":"); ok && (ns == "xlink" || ns == "xml" || ns == "xmlns") {
				attribute.Namespace, attribute.Key = ns, local
			}
			n.Attr = append(n.Attr, attribute)
		}
		children := m["children"].(map[string]any)
		var order []string
		switch xs := m["order"].(type) {
		case []string:
			order = xs
		case []any:
			for _, x := range xs {
				order = append(order, x.(string))
			}
		}
		for _, key := range order {
			if c := decode(children[key]); c != nil {
				n.AppendChild(c)
			}
		}
		return n
	}
	root := decode(mergeValue(b, o, t))
	var out bytes.Buffer
	for c := root.FirstChild; c != nil; c = c.NextSibling {
		if html.Render(&out, c) != nil {
			return nil, false
		}
	}
	return out.Bytes(), true
}
