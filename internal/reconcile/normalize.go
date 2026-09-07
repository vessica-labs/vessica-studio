package reconcile

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// NormalizeHTML assigns persistent identities once when a source enters the
// editor. Existing author IDs are never changed. New objects use unique IDs.
func NormalizeHTML(name string, data []byte) []byte {
	nodes, err := html.ParseFragment(bytes.NewReader(data), &html.Node{Type: html.ElementNode, Data: "div", DataAtom: atom.Div})
	if err != nil {
		return data
	}
	used := map[string]bool{}
	var collect func(*html.Node)
	collect = func(n *html.Node) {
		for _, a := range n.Attr {
			if a.Key == "id" || a.Key == "data-vstd-id" {
				used[a.Val] = true
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			collect(c)
		}
	}
	for _, node := range nodes {
		collect(node)
	}
	var assign func(*html.Node, string)
	assign = func(n *html.Node, p string) {
		if n.Type == html.ElementNode {
			identified := false
			for _, a := range n.Attr {
				if (a.Key == "id" || a.Key == "data-vstd-id") && a.Val != "" {
					identified = true
					break
				}
			}
			if !identified {
				hash := sha256.Sum256([]byte(name + ":" + p))
				id := fmt.Sprintf("v-%x", hash[:8])
				for salt := 0; used[id]; salt++ {
					hash = sha256.Sum256(append([]byte(fmt.Sprintf("%s:%s:%d:", name, p, salt)), data...))
					id = fmt.Sprintf("v-%x", hash[:8])
				}
				n.Attr = append(n.Attr, html.Attribute{Key: "data-vstd-id", Val: id})
				used[id] = true
			}
		}
		counts := map[string]int{}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			counts[c.Data]++
			assign(c, fmt.Sprintf("%s/%s:%d", p, c.Data, counts[c.Data]))
		}
	}
	var out bytes.Buffer
	for i, node := range nodes {
		assign(node, fmt.Sprintf("%d", i))
		if html.Render(&out, node) != nil {
			return data
		}
	}
	return out.Bytes()
}
