// Why: a binding of a large header (windows.h binds to megabytes of Mk) is
// mostly items no importer names, and checking them all is wasted work.
//
// What: Want and Uses, which collect the names the importing files use;
// PruneAll and prune, which keep the blocks those names reach and blank the
// rest; API and APIDiff with the E0213 diagnostics Mismatch and NoSysroot
// for `mk check --all-targets`.
//
// How: the unit is a block, a run of non-empty lines of the binding as emit
// writes them (an item with its "// C:" comment, an enum with its
// constants, an accessor pair). A block defines the names its column-0
// "export" lines declare (a method "export T.M(" defines T, so a kept type
// keeps every accessor) and references the identifiers of its code
// (comments and string literals skipped) that another block defines, plus qualified names other.N of an earlier
// import's binding. Kept blocks are found transitively, every other line
// is blanked, and every kept line keeps its line number in the view
// gen/<target>/c/<name>.mk, so diagnostics and go-to-definition point into
// the view unchanged.

package cimport

import (
	"fmt"
	"slices"
	"strings"

	"mk/internal/diag"
	"mk/internal/source"
)

// Want is the set of names wanted from each binding, by import name.
type Want map[string]map[string]bool

// Add records that name is wanted from import imp.
func (w Want) Add(imp, name string) {
	s := w[imp]
	if s == nil {
		s = map[string]bool{}
		w[imp] = s
	}
	s[name] = true
}

// PruneAll prunes the bindings texts (in import order, names[i] the import
// name of texts[i]) to the names want lists, adding what a kept item of a
// later binding needs from an earlier one. It returns the pruned texts,
// each with the line count of its input.
func PruneAll(names, texts []string, want Want) []string {
	idx := map[string]int{}
	for i, n := range names {
		idx[n] = i
	}
	out := make([]string, len(texts))
	// A binding refers only to earlier ones, so the last goes first.
	for i := len(texts) - 1; i >= 0; i-- {
		var ext []qualRef
		out[i], ext = prune(texts[i], want[names[i]], idx)
		for _, r := range ext {
			if j, ok := idx[r.mod]; ok && j < i {
				want.Add(r.mod, r.name)
			}
		}
	}
	return out
}

type qualRef struct{ mod, name string }

type block struct {
	lo, hi int // line range [lo, hi)
	defs   []string
	refs   []string
	quals  []qualRef
}

// prune keeps the blocks of text that want reaches. mods are the import
// names a qualified reference may name.
func prune(text string, want map[string]bool, mods map[string]int) (string, []qualRef) {
	lines := strings.Split(text, "\n")
	// The head is everything before the first item: the comment lines and
	// the imports. Items start at the first "// C:" line.
	head := len(lines)
	for i, l := range lines {
		if strings.HasPrefix(l, "// C:") {
			head = i
			for head > 0 && lines[head-1] != "" {
				head--
			}
			break
		}
	}
	var blocks []*block
	byDef := map[string][]int{} // folded name to blocks
	exact := map[string][]int{} // name to blocks
	for i := head; i < len(lines); {
		if lines[i] == "" {
			i++
			continue
		}
		b := &block{lo: i}
		for i < len(lines) && lines[i] != "" {
			i++
		}
		b.hi = i
		for _, l := range lines[b.lo:b.hi] {
			if n := defName(l); n != "" {
				b.defs = append(b.defs, n)
			}
		}
		k := len(blocks)
		blocks = append(blocks, b)
		for _, d := range b.defs {
			exact[d] = append(exact[d], k)
			byDef[fold(d)] = append(byDef[fold(d)], k)
		}
	}
	for _, b := range blocks {
		seen := map[string]bool{}
		for _, l := range lines[b.lo:b.hi] {
			scanIdents(l, func(id, qual string) {
				if qual != "" {
					if _, ok := mods[id]; ok {
						b.quals = append(b.quals, qualRef{id, qual})
					}
					return
				}
				if !seen[id] && len(exact[id]) > 0 {
					seen[id] = true
					b.refs = append(b.refs, id)
				}
			})
		}
	}
	keep := make([]bool, len(blocks))
	var work []int
	mark := func(ks []int) {
		for _, k := range ks {
			if !keep[k] {
				keep[k] = true
				work = append(work, k)
			}
		}
	}
	for n := range want {
		mark(byDef[fold(n)])
	}
	var ext []qualRef
	for len(work) > 0 {
		k := work[len(work)-1]
		work = work[:len(work)-1]
		for _, r := range blocks[k].refs {
			mark(exact[r])
		}
		ext = append(ext, blocks[k].quals...)
	}
	for k, b := range blocks {
		if keep[k] {
			continue
		}
		for i := b.lo; i < b.hi; i++ {
			lines[i] = ""
		}
	}
	// An import the kept items no longer use is blanked too (E0208).
	used := map[string]bool{}
	for k, b := range blocks {
		if !keep[k] {
			continue
		}
		for _, l := range lines[b.lo:b.hi] {
			scanIdents(l, func(id, qual string) {
				if qual != "" {
					used[id] = true
				}
			})
		}
	}
	for i := 0; i < head; i++ {
		if a := importAlias(lines[i]); a != "" && !used[a] {
			lines[i] = ""
		}
	}
	return strings.Join(lines, "\n"), ext
}

// importAlias is the name an "import std.raw" or "import c.x" line binds.
func importAlias(l string) string {
	if !strings.HasPrefix(l, "import ") {
		return ""
	}
	p := strings.TrimSpace(l[len("import "):])
	return p[strings.LastIndexByte(p, '.')+1:]
}

// defKeywords are the words before an item's name on its export line.
var defKeywords = map[string]bool{"extern": true, "packed": true, "align": true, "struct": true,
	"type": true, "const": true, "var": true}

// defName is the name a column-0 export line declares ("" for any other
// line); a method T.M declares T.
func defName(l string) string {
	if !strings.HasPrefix(l, "export ") {
		return ""
	}
	name := ""
	scanIdents(l[len("export "):], func(id, qual string) {
		if name == "" && !defKeywords[id] {
			name = id
		}
	})
	return name
}

// scanIdents calls f for each identifier of line l outside comments and
// string literals: f(id, "") for a plain one, f(a, b) for a qualified a.b
// (b is not reported alone).
func scanIdents(l string, f func(id, qual string)) {
	isStart := func(c byte) bool { return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }
	isPart := func(c byte) bool { return isStart(c) || c >= '0' && c <= '9' }
	for i := 0; i < len(l); {
		c := l[i]
		switch {
		case c == '/' && i+1 < len(l) && l[i+1] == '/':
			return
		case c == '"' || c == '\'':
			i++
			for i < len(l) && l[i] != c {
				if l[i] == '\\' {
					i++
				}
				i++
			}
			i++
		case isStart(c):
			j := i
			for j < len(l) && isPart(l[j]) {
				j++
			}
			id := l[i:j]
			if i > 0 && l[i-1] == '.' {
				// The member of an expression: no item name.
				i = j
				continue
			}
			if j+1 < len(l) && l[j] == '.' && isStart(l[j+1]) {
				k := j + 1
				for k < len(l) && isPart(l[k]) {
					k++
				}
				f(id, l[j+1:k])
				f(id, "")
				i = k
				continue
			}
			f(id, "")
			i = j
		case c >= '0' && c <= '9':
			for i < len(l) && isPart(l[i]) {
				i++
			}
		default:
			i++
		}
	}
}

// Uses adds to want the names a Mk file uses from its C imports: for each
// "import c.x" or "import a = c.x" line, every a.N of the file (comments
// and strings skipped). It over-approximates, which only keeps more.
func Uses(text string, want Want) {
	if !strings.Contains(text, "c.") {
		return
	}
	alias := map[string]string{}
	lines := strings.Split(text, "\n")
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if !strings.HasPrefix(l, "import ") {
			continue
		}
		p := strings.TrimSpace(l[len("import "):])
		a := ""
		if eq := strings.IndexByte(p, '='); eq >= 0 {
			a, p = strings.TrimSpace(p[:eq]), strings.TrimSpace(p[eq+1:])
		}
		if cut := strings.IndexAny(p, " \t/"); cut >= 0 {
			p = p[:cut]
		}
		if !strings.HasPrefix(p, "c.") || strings.Count(p, ".") != 1 {
			continue
		}
		name := p[2:]
		if a == "" {
			a = name
		}
		alias[a] = name
		if want[name] == nil {
			want[name] = map[string]bool{}
		}
	}
	if len(alias) == 0 {
		return
	}
	for _, l := range lines {
		scanIdents(l, func(id, qual string) {
			if qual == "" {
				return
			}
			if name, ok := alias[id]; ok {
				want.Add(name, qual)
			}
		})
	}
}

// API is the interface of a (pruned) binding by item name: the code lines
// of every block that defines the name, comments dropped and a constant's
// value cut (its type is its API), in order.
func API(text string) map[string]string {
	lines := strings.Split(text, "\n")
	out := map[string]string{}
	var cur []string
	var defs []string
	flush := func() {
		body := strings.Join(cur, "\n")
		for _, d := range defs {
			if out[d] != "" {
				out[d] += "\n"
			}
			out[d] += body
		}
		cur, defs = nil, nil
	}
	for _, l := range lines {
		if l == "" {
			flush()
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(l), "//") || strings.HasPrefix(l, "import ") {
			continue
		}
		if n := defName(l); n != "" {
			if !slices.Contains(defs, n) {
				defs = append(defs, n)
			}
			if strings.HasPrefix(l, "export const ") {
				if eq := strings.Index(l, " = "); eq >= 0 {
					l = l[:eq]
				}
			}
		}
		cur = append(cur, l)
	}
	flush()
	return out
}

// APIDiff lists the items whose API differs between two bindings of one
// import, sorted: an item one side lacks, or one spelled differently.
func APIDiff(a, b map[string]string) []string {
	var out []string
	for n, x := range a {
		if y, ok := b[n]; !ok || x != y {
			out = append(out, n)
		}
	}
	for n := range b {
		if _, ok := a[n]; !ok {
			out = append(out, n)
		}
	}
	slices.Sort(out)
	return out
}

// Mismatch is the E0213 of mk check --all-targets for C import name: the
// items the package uses whose API differs between targets a and b.
func Mismatch(at source.Span, name, a, b string, diff []string) *diag.Diagnostic {
	shown := diff
	if len(shown) > 10 {
		shown = shown[:10]
	}
	return diag.New(diag.E0213, at, fmt.Sprintf("c.%s differs between %s and %s in %d used item(s): %s",
		name, a, b, len(diff), strings.Join(shown, ", "))).
		Text("import", "c."+name).Text("targets", a+" "+b).Text("items", strings.Join(diff, " "))
}

// NoSysroot is the E0213 of --all-targets for a target with no sysroot and
// no vendored binding: skipping a target is an error.
func NoSysroot(at source.Span, target string) *diag.Diagnostic {
	return diag.New(diag.E0213, at, fmt.Sprintf("--all-targets: no sysroot for %s: set MK_SYSROOT_%s, put its headers in $MK_EVIDENCE/c-import/sysroots/%s, or vendor the C imports", target, target, target)).
		Text("target", target).Text("reason", "no-sysroot")
}
