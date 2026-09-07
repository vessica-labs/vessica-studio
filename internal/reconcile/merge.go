// Package reconcile owns three-way presentation reconciliation for every writer.
// Inputs are immutable checkpoints; callers retain them even when a policy chooses
// one visible value. No merge conflict markers are inserted into authoring files.
package reconcile

import (
	"bytes"
	"encoding/json"
	"fmt"
	"gopkg.in/yaml.v3"
	"path"
	"reflect"
	"sort"
	"strings"
)

type File struct {
	Path    string `json:"path"`
	Content []byte `json:"content"`
	Mode    uint32 `json:"mode,omitempty"`
}
type Snapshot struct {
	Files []File `json:"files"`
}
type Input struct {
	Normalize     bool     `json:"normalize"`
	Base          Snapshot `json:"base"`
	Incoming      Snapshot `json:"incoming"`
	Current       Snapshot `json:"current"`
	PreferCurrent bool     `json:"preferCurrent"`
}
type Result struct {
	Files        []File   `json:"files"`
	Alternatives []string `json:"alternatives"`
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func Merge(in Input) (Result, error) {
	maps := []map[string]File{}
	keys := map[string]bool{}
	for _, s := range []Snapshot{in.Base, in.Incoming, in.Current} {
		m := map[string]File{}
		total := 0
		if len(s.Files) > 10000 {
			return Result{}, fmt.Errorf("snapshot limit")
		}
		for _, f := range s.Files {
			if f.Path == "" || path.Clean(f.Path) != f.Path || strings.HasPrefix(f.Path, "/") || strings.HasPrefix(f.Path, "../") || strings.ContainsAny(f.Path, "\\\x00") || strings.Contains("/"+f.Path+"/", "/.git/") {
				return Result{}, fmt.Errorf("unsafe path")
			}
			if _, ok := m[f.Path]; ok {
				return Result{}, fmt.Errorf("duplicate path")
			}
			total += len(f.Content)
			if len(f.Content) > 16<<20 || total > 128<<20 {
				return Result{}, fmt.Errorf("snapshot limit")
			}
			m[f.Path] = f
			keys[f.Path] = true
		}
		normalizeSlideOrder(m)
		maps = append(maps, m)
	}
	out := Result{Files: []File{}, Alternatives: []string{}}
	if in.Normalize {
		for _, file := range maps[1] {
			if strings.HasSuffix(file.Path, ".html") {
				file.Content = NormalizeHTML(file.Path, file.Content)
			}
			out.Files = append(out.Files, file)
		}
		sort.Slice(out.Files, func(i, j int) bool { return out.Files[i].Path < out.Files[j].Path })
		return out, nil
	}
	// A deleted slide is a paired structural operation, not two unrelated file
	// deletions. Never retain an edited fragment while dropping its companion.
	pairChoice := map[string]int{}
	for k := range keys {
		if !strings.Contains(k, "/slides/") || !strings.HasSuffix(k, ".html") {
			continue
		}
		pair := []string{k, strings.TrimSuffix(k, ".html") + ".md"}
		deleted := func(side int) bool {
			for _, p := range pair {
				if _, existed := maps[0][p]; existed {
					if _, exists := maps[side][p]; !exists {
						return true
					}
				}
			}
			return false
		}
		changed := func(side int) bool {
			for _, p := range pair {
				b, be := maps[0][p]
				v, ve := maps[side][p]
				if be != ve || !bytes.Equal(b.Content, v.Content) {
					return true
				}
			}
			return false
		}
		if (deleted(1) && changed(2)) || (deleted(2) && changed(1)) {
			side := 1
			if in.PreferCurrent {
				side = 2
			}
			for _, p := range pair {
				pairChoice[p] = side
			}
		}
	}
	for k := range keys {
		if side, ok := pairChoice[k]; ok {
			out.Alternatives = append(out.Alternatives, k)
			if f, exists := maps[side][k]; exists {
				out.Files = append(out.Files, f)
			}
			continue
		}
		b, bok := maps[0][k]
		o, ook := maps[1][k]
		t, tok := maps[2][k]
		unchanged := func(x File, ok bool) bool { return ok == bok && bytes.Equal(x.Content, b.Content) }
		switch {
		case unchanged(o, ook):
			if tok {
				out.Files = append(out.Files, t)
			}
		case unchanged(t, tok):
			if ook {
				out.Files = append(out.Files, o)
			}
		case ook && tok && bytes.Equal(o.Content, t.Content):
			out.Files = append(out.Files, o)
		default:
			out.Alternatives = append(out.Alternatives, k)
			if !ook || !tok {
				if in.PreferCurrent {
					if tok {
						out.Files = append(out.Files, t)
					}
				} else if ook {
					out.Files = append(out.Files, o)
				}
				continue
			}
			o.Content = MergeFile(k, b.Content, o.Content, t.Content, in.PreferCurrent)
			out.Files = append(out.Files, o)
		}
	}
	sort.Slice(out.Files, func(i, j int) bool { return out.Files[i].Path < out.Files[j].Path })
	sort.Strings(out.Alternatives)
	return out, nil
}

func normalizeSlideOrder(files map[string]File) {
	for name, file := range files {
		if !strings.HasPrefix(name, "decks/") || !strings.HasSuffix(name, "/deck.yaml") {
			continue
		}
		prefix := strings.TrimSuffix(name, "deck.yaml") + "slides/"
		ids := []string{}
		available := map[string]bool{}
		for p := range files {
			if strings.HasPrefix(p, prefix) && strings.HasSuffix(p, ".html") {
				id := strings.TrimSuffix(strings.TrimPrefix(p, prefix), ".html")
				if !strings.Contains(id, "/") {
					ids = append(ids, id)
					available[id] = true
				}
			}
		}
		sort.Strings(ids)
		var meta map[string]any
		if yaml.Unmarshal(file.Content, &meta) != nil || meta == nil {
			continue
		}
		ordered := []string{}
		if list, ok := stringList(meta["slide_order"]); ok {
			for _, id := range list {
				if available[id] {
					ordered = append(ordered, id)
					delete(available, id)
				}
			}
		}
		for _, id := range ids {
			if available[id] {
				ordered = append(ordered, id)
			}
		}
		meta["slide_order"] = ordered
		if content, err := yaml.Marshal(meta); err == nil {
			file.Content = content
			files[name] = file
		}
	}
}

func MergeFile(name string, base, incoming, current []byte, preferCurrent bool) []byte {
	if bytes.Equal(incoming, base) {
		return current
	}
	if bytes.Equal(current, base) || bytes.Equal(incoming, current) {
		return incoming
	}
	// Choosing current reverses the priority, without changing the common ancestor.
	if preferCurrent {
		incoming, current = current, incoming
	}
	switch path.Ext(name) {
	case ".html":
		if out, ok := mergeHTML(base, incoming, current); ok {
			return out
		}
	case ".yaml", ".yml", ".json":
		var b, o, t any
		if yaml.Unmarshal(base, &b) == nil && yaml.Unmarshal(incoming, &o) == nil && yaml.Unmarshal(current, &t) == nil {
			v := mergeValue(b, o, t)
			if path.Ext(name) == ".json" {
				if out, e := json.MarshalIndent(v, "", "  "); e == nil {
					return append(out, '\n')
				}
			} else if out, e := yaml.Marshal(v); e == nil {
				return out
			}
		}
	case ".md":
		return []byte(mergeSections(string(base), string(incoming), string(current)))
	}
	return incoming
}

func mergeValue(b, o, t any) any {
	if reflect.DeepEqual(o, b) {
		return t
	}
	if reflect.DeepEqual(t, b) || reflect.DeepEqual(o, t) {
		return o
	}
	bm, bok := b.(map[string]any)
	om, ook := o.(map[string]any)
	tm, tok := t.(map[string]any)
	if ook && tok {
		if !bok {
			bm = map[string]any{}
		}
		out := map[string]any{}
		keys := map[string]bool{}
		for k := range bm {
			keys[k] = true
		}
		for k := range om {
			keys[k] = true
		}
		for k := range tm {
			keys[k] = true
		}
		for k := range keys {
			bv, be := bm[k]
			ov, oe := om[k]
			tv, te := tm[k]
			if oe == be && reflect.DeepEqual(ov, bv) {
				if te {
					out[k] = tv
				}
			} else if !oe {
				continue
			} else if !te && be && !reflect.DeepEqual(tv, bv) {
				out[k] = ov
			} else {
				out[k] = mergeValue(bv, ov, tv)
			}
		}
		return out
	}
	bs, bo := stringList(b)
	os, oo := stringList(o)
	ts, to := stringList(t)
	if bo && oo && to {
		return mergeOrder(bs, os, ts)
	}
	return o
}
func stringList(v any) ([]string, bool) {
	xs, ok := v.([]any)
	if !ok {
		return nil, false
	}
	out := []string{}
	seen := map[string]bool{}
	for _, x := range xs {
		s, ok := x.(string)
		if !ok || seen[s] {
			return nil, false
		}
		out = append(out, s)
		seen[s] = true
	}
	return out, true
}

// Reapply the minimum incoming moves/inserts onto current order. A changed
// predecessor does not itself imply a move: that would undo independent moves.
func mergeOrder(base, ours, theirs []string) []string {
	positions := map[string]int{}
	for i, key := range base {
		positions[key] = i
	}
	tails := []int{}
	previousIndex := make([]int, len(ours))
	for i := range previousIndex {
		previousIndex[i] = -1
	}
	for i, key := range ours {
		position, exists := positions[key]
		if !exists {
			continue
		}
		at := sort.Search(len(tails), func(j int) bool { return positions[ours[tails[j]]] >= position })
		if at > 0 {
			previousIndex[i] = tails[at-1]
		}
		if at == len(tails) {
			tails = append(tails, i)
		} else {
			tails[at] = i
		}
	}
	stable := map[string]bool{}
	if len(tails) > 0 {
		for i := tails[len(tails)-1]; i >= 0; i = previousIndex[i] {
			stable[ours[i]] = true
		}
	}
	result := append([]string{}, theirs...)
	index := func(a []string, k string) int {
		for i, v := range a {
			if v == k {
				return i
			}
		}
		return -1
	}
	remove := func(k string) {
		if i := index(result, k); i >= 0 {
			result = append(result[:i], result[i+1:]...)
		}
	}
	for _, k := range base {
		if index(ours, k) < 0 {
			remove(k)
		}
	}
	previous := ""
	for _, k := range ours {
		if !stable[k] {
			remove(k)
			i := index(result, previous) + 1
			if previous == "" {
				i = 0
			}
			result = append(result, "")
			copy(result[i+1:], result[i:])
			result[i] = k
		}
		if index(result, k) >= 0 {
			previous = k
		}
	}
	return result
}
func mergeSections(base, ours, theirs string) string {
	split := func(s string) (map[string]string, []string) {
		m := map[string]string{}
		order := []string{""}
		key := ""
		for _, line := range strings.SplitAfter(s, "\n") {
			if strings.HasPrefix(line, "#") {
				key = strings.TrimSpace(line)
				if _, ok := m[key]; !ok {
					order = append(order, key)
				}
			}
			m[key] += line
		}
		return m, order
	}
	b, _ := split(base)
	o, order := split(ours)
	t, to := split(theirs)
	for _, k := range to {
		if _, ok := o[k]; !ok {
			if _, was := b[k]; !was {
				order = append(order, k)
			}
		}
	}
	var out strings.Builder
	for _, k := range order {
		ov, oe := o[k]
		tv, te := t[k]
		bv, be := b[k]
		if ov != bv && tv != bv && ov != tv && strings.HasPrefix(ov, bv) && strings.HasPrefix(tv, bv) {
			out.WriteString(tv)
			out.WriteString(strings.TrimPrefix(ov, bv))
			continue
		}
		if oe == be && ov == bv {
			if te {
				out.WriteString(tv)
			}
		} else if oe {
			out.WriteString(ov)
		} else if te && !be {
			out.WriteString(tv)
		}
	}
	return out.String()
}
