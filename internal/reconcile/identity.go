package reconcile

import (
	"bytes"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

func nodeIdentity(n *html.Node) html.Attribute {
	var found html.Attribute
	for _, a := range n.Attr {
		if a.Key == "data-vstd-id" && a.Val != "" {
			return a
		}
		if a.Key == "id" && a.Val != "" {
			found = a
		}
	}
	return found
}

// A writer may omit an engine identity while rewriting markup. Match only
// otherwise-unmatched siblings of the same element type; never steal an ID
// still present elsewhere in the submitted tree or override an explicit new ID.
func restoreHTMLIdentities(base, changed []byte) []byte {
	parse := func(data []byte) (*html.Node, error) {
		nodes, err := html.ParseFragment(bytes.NewReader(data), &html.Node{Type: html.ElementNode, Data: "div", DataAtom: atom.Div})
		if err != nil {
			return nil, err
		}
		root := &html.Node{Type: html.ElementNode, Data: "div"}
		for _, n := range nodes {
			root.AppendChild(n)
		}
		return root, nil
	}
	b, err := parse(base)
	if err != nil {
		return changed
	}
	c, err := parse(changed)
	if err != nil {
		return changed
	}
	used := map[html.Attribute]bool{}
	var collect func(*html.Node)
	collect = func(n *html.Node) {
		if id := nodeIdentity(n); id.Val != "" {
			used[id] = true
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			collect(child)
		}
	}
	collect(c)
	var align func(*html.Node, *html.Node)
	align = func(before, after *html.Node) {
		consumed := map[*html.Node]bool{}
		for n := after.FirstChild; n != nil; n = n.NextSibling {
			if n.Type != html.ElementNode {
				continue
			}
			id := nodeIdentity(n)
			var match *html.Node
			for old := before.FirstChild; old != nil; old = old.NextSibling {
				if consumed[old] || old.Type != n.Type || old.Data != n.Data {
					continue
				}
				oldID := nodeIdentity(old)
				if id.Val != "" {
					if id == oldID {
						match = old
						break
					}
				} else if oldID.Val == "" || !used[oldID] {
					match = old
					break
				}
			}
			if match == nil {
				continue
			}
			consumed[match] = true
			if oldID := nodeIdentity(match); id.Val == "" && oldID.Val != "" {
				n.Attr = append(n.Attr, oldID)
				used[oldID] = true
			}
			align(match, n)
		}
	}
	align(b, c)
	var out bytes.Buffer
	for n := c.FirstChild; n != nil; n = n.NextSibling {
		if html.Render(&out, n) != nil {
			return changed
		}
	}
	return out.Bytes()
}
