// Why: a novice learns std from the editor, so every exported std item must
// answer hover, definition, completion and signature help from user code,
// not only the few items the other tests name.
// What: TestLSPStdCoverage, which enumerates std's exported items from its
// sources and asks the server about each one; TestLSPLanguageCoverage,
// which hovers every keyword, contextual word, primitive type, universe
// name and operator; TestPreludeNamesQuoteTheReference; and
// TestLSPLatencyLargeFile.
// How: one generated user file per part of a std module (at most covChunk
// items) references each item as a user writes it (`m.F()`, `v.M()`,
// `m.T.New()`, `v.field`, `m.E.V`); the test counts the answers per kind
// and fails on a miss. MK_LSP_COVERAGE=1
// sweeps every module (about 20 minutes here); the default run sweeps a
// sample.

package lsp

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// covItem is one exported std item as a user's file refers to it.
type covItem struct {
	mod   string // std.fs; std.core for the prelude
	owner string // a method's, field's or variant's type; "" for an item
	name  string
	kind  string // fn, method, static, type, const, field, variant
	file  string // the declaring file
	line  int    // the declaration's zero-based line
	doc   string // the first line of its doc comment, "" when none
	// ref is the reference's text in the generated file, and at the
	// name's byte offset in it.
	ref   string
	gline int // the reference's zero-based line in the generated file
}

// covSample are the default run's modules.
var covSample = []string{"std.core", "std.fs", "std.coll", "std.json", "std.time"}

// covStdItems reads the exported items of one std module's directory.
func covStdItems(t *testing.T, mod, dir string) []covItem {
	es, err := os.ReadDir(dir)
	must(t, err)
	var out []covItem
	seen := map[string]bool{}
	for _, e := range es {
		n := e.Name()
		if e.IsDir() || !strings.HasSuffix(n, ".mk") || strings.HasPrefix(n, "tests_") || strings.HasSuffix(n, "_test.mk") {
			continue
		}
		p := filepath.Join(dir, n)
		raw, err := os.ReadFile(p)
		must(t, err)
		text := string(raw)
		ls := newLines(text)
		add := func(it covItem, d decl) {
			key := it.owner + "." + it.name
			if seen[key] || strings.HasPrefix(it.name, "test ") || it.name == "" {
				return
			}
			seen[key] = true
			it.mod, it.file, it.line = mod, p, ls.pos(d.NameStart, true).Line
			it.doc = covDoc(text, d.Start)
			out = append(out, it)
		}
		for _, d := range decls(text, scan(text)) {
			if !d.Exported || d.Kind == symNamespace {
				continue
			}
			switch {
			case d.Owner != "":
				head := text[d.NameEnd:lineEnd(text, d.NameEnd)]
				kind := "static"
				if hasSelfParam(head) {
					kind = "method"
				}
				add(covItem{owner: d.Owner, name: d.Name, kind: kind}, d)
			case d.Kind == symFunction:
				add(covItem{name: d.Name, kind: "fn"}, d)
			case d.Kind == symConstant || d.Kind == symVariable:
				add(covItem{name: d.Name, kind: "const"}, d)
			case d.Detail == "impl":
			default:
				add(covItem{name: d.Name, kind: "type"}, d)
				for _, ch := range d.Children {
					switch {
					case ch.Kind == symField && ch.Exported:
						add(covItem{owner: d.Name, name: ch.Name, kind: "field"}, ch)
					case ch.Kind == symEnumMember:
						add(covItem{owner: d.Name, name: ch.Name, kind: "variant"}, ch)
					}
				}
			}
		}
	}
	return out
}

// coreAlias reads `export type Duration = core.Duration`, a module's alias
// of a std.core type outside the prelude.
var coreAlias = regexp.MustCompile(`(?m)^export type (\w+) = core\.(\w+)\s*$`)

// covAliasMembers are the methods and fields of the std.core types a
// module re-exports by alias, as the module's users reach them
// (`v: &time.Duration`, `v.Seconds()`). They are declared in std.core.
func covAliasMembers(t *testing.T, c *client, mod, dir string) []covItem {
	es, err := os.ReadDir(dir)
	must(t, err)
	alias := map[string]string{} // core name -> the module's name
	for _, e := range es {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".mk") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		must(t, err)
		for _, m := range coreAlias.FindAllStringSubmatch(string(raw), -1) {
			alias[m[2]] = m[1]
		}
	}
	if len(alias) == 0 {
		return nil
	}
	var out []covItem
	for _, it := range covStdItems(t, "std.core", c.srv.stdModuleDir("std.core")) {
		if name, ok := alias[it.owner]; ok && (it.kind == "method" || it.kind == "field") {
			it.mod, it.owner = mod, name
			out = append(out, it)
		}
	}
	return out
}

// covDoc is the first line of the `///` comment above the declaration at
// byte start, "" when it has none. Plain `//` lines in the same block are
// notes for the file's reader, not the item's doc, and hover leaves them out.
func covDoc(text string, start int) string {
	lines := strings.Split(text[:strings.LastIndexByte(text[:start], '\n')+1], "\n")
	first := ""
	for i := len(lines) - 1; i >= 0; i-- {
		l := strings.TrimSpace(lines[i])
		if i == len(lines)-1 && l == "" {
			continue // the split's empty tail
		}
		if !strings.HasPrefix(l, "//") {
			break
		}
		if d, ok := strings.CutPrefix(l, "///"); ok && strings.TrimSpace(d) != "" {
			first = strings.TrimSpace(d)
		}
	}
	return first
}

// hasSelfParam reports whether a method head's parameter list starts with
// a receiver (`self`, `&self`, `mut self`, `&mut self`).
func hasSelfParam(head string) bool {
	if i := strings.IndexByte(head, '['); i == 0 {
		head = head[skipBracketsAt(head, 0):]
	}
	head = strings.TrimPrefix(head, "(")
	head = strings.TrimLeft(head, "&")
	head = strings.TrimPrefix(head, "mut ")
	head = strings.TrimLeft(head, "&")
	return strings.HasPrefix(head, "self")
}

// covFile writes the user file referring to every item of a module.
func covFile(mod string, items []covItem) string {
	bind := mod[strings.LastIndexByte(mod, '.')+1:]
	qual := bind + "."
	var b strings.Builder
	if mod != "std.core" {
		b.WriteString("import " + mod + "\n\n")
	} else {
		qual = ""
	}
	byOwner := map[string][]int{}
	var owners []string
	b.WriteString("Probe() {\n")
	line := strings.Count(b.String(), "\n")
	for i := range items {
		it := &items[i]
		switch it.kind {
		case "fn":
			it.ref = qual + it.name + "()"
		case "static":
			it.ref = qual + it.owner + "." + it.name + "()"
		case "type", "const":
			it.ref = qual + it.name
		case "variant":
			it.ref = qual + it.owner + "." + it.name
		default: // method, field: through a parameter of the owning type
			if byOwner[it.owner] == nil {
				owners = append(owners, it.owner)
			}
			byOwner[it.owner] = append(byOwner[it.owner], i)
			continue
		}
		it.gline = line
		line++
		b.WriteString("    _ = " + it.ref + "\n")
	}
	b.WriteString("}\n")
	line++
	for n, o := range owners {
		typ := "&" + qual + o
		if mod == "std.core" && o == "Slice" {
			typ = "[]int" // Slice[T] is spelled []T
		}
		fmt.Fprintf(&b, "\nProbe%d(v: %s) {\n", n, typ)
		line += 2
		for _, i := range byOwner[o] {
			it := &items[i]
			it.ref = "v." + it.name
			if it.kind == "method" {
				it.ref += "()"
			}
			it.gline = line
			line++
			b.WriteString("    _ = " + it.ref + "\n")
		}
		b.WriteString("}\n")
		line++
	}
	return b.String()
}

// covTally counts one kind's items and the answers they got.
type covTally struct{ n, hover, doc, def, compl, sig, sigN, nodoc int }

type covResult struct {
	mu     sync.Mutex
	kinds  map[string]*covTally
	misses map[string][]string // by request and kind
	undoc  []string            // items with no `///` comment: nothing to show
}

func (r *covResult) miss(req, kind, what string) {
	r.misses[req+" "+kind] = append(r.misses[req+" "+kind], what)
}

// TestLSPStdCoverage asks hover, definition, completion and signature help
// of every exported item of std's public modules, from user code.
func TestLSPStdCoverage(t *testing.T) {
	repo, err := filepath.Abs(filepath.Join("..", ".."))
	must(t, err)
	std := filepath.Join(repo, "std")
	if _, err := os.Stat(filepath.Join(std, "fs", "fs.mk")); err != nil {
		t.Skip("no std in this tree")
	}
	full := os.Getenv("MK_LSP_COVERAGE") != ""
	dir := t.TempDir()
	must(t, os.WriteFile(filepath.Join(dir, "pkg.mk"), []byte(strings.Replace(fixtureManifest, `"fx"`, `"cov"`, 2)), 0o644))
	mods := covSample
	if m := os.Getenv("MK_LSP_COVERAGE_MODULES"); m != "" {
		mods = strings.Fields(m)
	} else if full {
		// Every public module, as import completion offers them.
		c := startWith(t, Options{StdDir: std, CacheDir: testCache, Jobs: 4})
		c.init(true)
		p := filepath.Join(dir, "main.mk")
		text := "import std.fs\n\nexport Main(sys: &System) {\n    _ = fs.ReadText(sys.arena, sys.io, \"x\")\n}\n"
		must(t, writeFile(p, text))
		c.open(pathURI(p), text)
		c.diagnostics(pathURI(p))
		var list completionList
		c.result(c.call("textDocument/completion", at(pathURI(p), 0, 7)), &list)
		mods = []string{"std.core"}
		for _, it := range list.Items {
			if strings.HasPrefix(it.Label, "std.") {
				mods = append(mods, it.Label)
			}
		}
		c.notify("textDocument/didClose", doc(pathURI(p)))
		c.shutdown()
		if len(mods) < 20 {
			t.Fatalf("import completion offers %d std modules: %v", len(mods), mods)
		}
	}
	res := &covResult{kinds: map[string]*covTally{}, misses: map[string][]string{}}
	// Each worker takes the next job when it finishes one. A job is at
	// most covChunk items of one module, largest first, so std.core (more
	// than half of the sample's items and most of its time) is shared by
	// the workers instead of setting the wall time alone.
	jobs := covJobs(t, startWith(t, Options{StdDir: std, CacheDir: testCache, Jobs: 2}), std, mods)
	workers := 2
	queue := make(chan covJob, len(jobs))
	for _, j := range jobs {
		queue <- j
	}
	close(queue)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			c := startWith(t, Options{StdDir: std, CacheDir: testCache, Jobs: 2})
			c.init(true)
			for j := range queue {
				begin := time.Now()
				n := covModule(t, c, std, filepath.Join(dir, fmt.Sprintf("w%d", w)), j.mod, j.items, res)
				t.Logf("%s (part %d of %d): %d items in %v", j.mod, j.part, j.parts, n, time.Since(begin).Round(time.Millisecond))
			}
			c.shutdown()
		}(w)
	}
	wg.Wait()
	covReport(t, res, full)
}

// covChunk bounds the items of one job: one generated file, opened once.
const covChunk = 60

// covJob is one part of one module's items.
type covJob struct {
	mod         string
	items       []covItem
	part, parts int
}

// covJobs reads the items of each module (c is a server that is never
// initialized: only its std directory is read) and splits each module
// into parts of at most covChunk items, the largest parts first. Every
// item is in exactly one part.
func covJobs(t *testing.T, c *client, std string, mods []string) []covJob {
	var jobs []covJob
	for _, mod := range mods {
		items := covModuleItems(t, c, std, mod)
		parts := (len(items) + covChunk - 1) / covChunk
		if parts == 0 {
			parts = 1
		}
		for p := 0; p < parts; p++ {
			lo, hi := len(items)*p/parts, len(items)*(p+1)/parts
			jobs = append(jobs, covJob{mod: mod, items: items[lo:hi:hi], part: p + 1, parts: parts})
		}
	}
	sort.SliceStable(jobs, func(i, j int) bool { return len(jobs[i].items) > len(jobs[j].items) })
	return jobs
}

// covModuleItems are the items of mod a user's file can refer to.
func covModuleItems(t *testing.T, c *client, std, mod string) []covItem {
	// The directory this OS builds (std.linux is linux/other on Windows).
	sd := c.srv.stdModuleDir(mod)
	if sd == "" {
		sd = joinModule(std, strings.TrimPrefix(mod, "std."))
	}
	items := covStdItems(t, mod, sd)
	if mod == "std.core" {
		// User code sees only the prelude of std.core; its other exports
		// are std-internal.
		var vis []covItem
		for _, it := range items {
			if preludeNames[it.name] && it.owner == "" || preludeNames[it.owner] {
				vis = append(vis, it)
			}
		}
		return vis
	}
	return append(items, covAliasMembers(t, c, mod, sd)...)
}

// covModule writes and opens one file referring to items, all of module
// mod, and asks about each item.
func covModule(t *testing.T, c *client, std, dir, mod string, items []covItem, res *covResult) int {
	items = slices.Clone(items) // covFile writes each item's reference
	text := covFile(mod, items)
	must(t, os.MkdirAll(dir, 0o755))
	if _, err := os.Stat(filepath.Join(dir, "pkg.mk")); err != nil {
		must(t, os.WriteFile(filepath.Join(dir, "pkg.mk"), []byte(strings.Replace(fixtureManifest, `"fx"`, `"cov"`, 2)), 0o644))
	}
	p := filepath.Join(dir, "main.mk")
	must(t, writeFile(p, text))
	uri := pathURI(p)
	c.open(uri, text)
	c.diagnostics(uri)
	ls := strings.Split(text, "\n")
	complCache := map[string][]string{}
	for _, it := range items {
		line := it.gline
		if ls[line] != "    _ = "+it.ref {
			t.Fatalf("%s: line %d is %q, want the reference %q", mod, line, ls[line], it.ref)
		}
		col := len("    _ = ") + strings.LastIndex(it.ref, it.name)
		where := fmt.Sprintf("%s %s", mod, strings.TrimPrefix(it.owner+"."+it.name, "."))
		var hv *hover
		c.result(c.call("textDocument/hover", at(uri, line, col+1)), &hv)
		hoverText := ""
		if hv != nil {
			hoverText = hv.Contents.Value
		}
		var defs []Location
		c.result(c.call("textDocument/definition", at(uri, line, col+1)), &defs)
		// Completion just after the dot before the name.
		dot := col
		key := ls[line][:dot]
		labels, ok := complCache[key]
		if !ok {
			var list completionList
			m := c.call("textDocument/completion", at(uri, line, dot))
			if m.Error == nil && string(m.Result) != "null" {
				c.result(m, &list)
			}
			for _, x := range list.Items {
				labels = append(labels, x.Label)
			}
			if !strings.HasPrefix(it.ref, "v.") {
				// `v.` repeats across owners: asked each time.
				complCache[key] = labels
			}
		}
		sigOK := false
		wantSig := strings.HasSuffix(it.ref, "()")
		if wantSig {
			var sh *signatureHelp
			c.result(c.call("textDocument/signatureHelp", at(uri, line, len(ls[line])-1)), &sh)
			sigOK = sh != nil && len(sh.Signatures) > 0 && strings.Contains(sh.Signatures[0].Label, it.name)
		}
		res.mu.Lock()
		tl := res.kinds[it.kind]
		if tl == nil {
			tl = &covTally{}
			res.kinds[it.kind] = tl
		}
		tl.n++
		if namesWord(hoverText, it.name) {
			tl.hover++
		} else {
			res.miss("hover", it.kind, where)
		}
		if it.doc == "" {
			tl.nodoc++
			res.undoc = append(res.undoc, where)
		}
		if it.doc == "" || strings.Contains(hoverText, it.doc) {
			tl.doc++
		} else {
			res.miss("doc", it.kind, fmt.Sprintf("%s: want %q, hover %q", where, it.doc, covClip(hoverText, 300)))
		}
		if covLands(defs, it, std) {
			tl.def++
		} else {
			got := "none"
			if len(defs) > 0 {
				got = fmt.Sprintf("%s:%d", defs[0].URI, defs[0].Range.Start.Line+1)
			}
			res.miss("definition", it.kind, where+" -> "+got)
		}
		if contains(labels, it.name) {
			tl.compl++
		} else {
			res.miss("completion", it.kind, where)
		}
		if wantSig {
			tl.sigN++
			if sigOK {
				tl.sig++
			} else {
				res.miss("signature", it.kind, where)
			}
		}
		res.mu.Unlock()
	}
	c.notify("textDocument/didClose", doc(uri))
	return len(items)
}

// covClip is s cut to n bytes, for a miss's report.
func covClip(s string, n int) string {
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}

func contains(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}

// covLands reports whether a definition answer's first location is the
// item's declaration: the same file below std (the tree or its read-only
// copy) and line.
func covLands(defs []Location, it covItem, std string) bool {
	if len(defs) == 0 {
		return false
	}
	p, ok := uriPath(defs[0].URI)
	if !ok {
		return false
	}
	rel, err := filepath.Rel(std, it.file)
	if err != nil {
		return false
	}
	return strings.HasSuffix(filepath.ToSlash(p), "/"+filepath.ToSlash(rel)) && defs[0].Range.Start.Line == it.line
}

// covReport logs the counts per kind and the misses, and fails on misses.
func covReport(t *testing.T, res *covResult, full bool) {
	var b strings.Builder
	fmt.Fprintf(&b, "%-8s %6s %6s %6s %6s %6s %9s %6s\n", "kind", "items", "hover", "doc", "def", "compl", "sighelp", "no ///")
	var kinds []string
	for k := range res.kinds {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	var tot covTally
	for _, k := range kinds {
		x := res.kinds[k]
		fmt.Fprintf(&b, "%-8s %6d %6d %6d %6d %6d %4d/%-4d %6d\n", k, x.n, x.hover, x.doc, x.def, x.compl, x.sig, x.sigN, x.nodoc)
		tot.n += x.n
		tot.hover += x.hover
		tot.doc += x.doc
		tot.def += x.def
		tot.compl += x.compl
		tot.sig += x.sig
		tot.sigN += x.sigN
		tot.nodoc += x.nodoc
	}
	fmt.Fprintf(&b, "%-8s %6d %6d %6d %6d %6d %4d/%-4d %6d\n", "total", tot.n, tot.hover, tot.doc, tot.def, tot.compl, tot.sig, tot.sigN, tot.nodoc)
	if full && len(res.undoc) > 0 {
		sort.Strings(res.undoc)
		fmt.Fprintf(&b, "no /// comment (counted as doc: nothing to show): %d\n", len(res.undoc))
		for _, u := range res.undoc {
			fmt.Fprintf(&b, "    %s\n", u)
		}
	}
	var keys []string
	for k := range res.misses {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		ms := res.misses[k]
		fmt.Fprintf(&b, "missing %s: %d\n", k, len(ms))
		for i, m := range ms {
			if i == 15 && !full {
				fmt.Fprintf(&b, "    ... %d more\n", len(ms)-i)
				break
			}
			fmt.Fprintf(&b, "    %s\n", m)
		}
	}
	t.Log("\n" + b.String())
	if out := os.Getenv("MK_LSP_COVERAGE_OUT"); out != "" {
		_ = os.WriteFile(out, []byte(b.String()), 0o644)
	}
	if len(keys) > 0 {
		t.Errorf("%d kinds of std coverage misses", len(keys))
	}
}

// TestLSPLanguageCoverage: every keyword, flow word, primitive type,
// universe name, contextual word, declaration modifier, asm operand word
// and operator of the grammar has a rule hover, and the hover
// request answers it in code.
func TestLSPLanguageCoverage(t *testing.T) {
	var terms []string
	for _, m := range []map[string]bool{keywords, flowWords, primTypes, asmWords, universeWords} {
		terms = append(terms, sortedKeys(m)...)
	}
	// The contextual words and the declaration modifiers.
	terms = append(terms, "self", "Self", "result", "fuel", "from", "into", "ends", "open", "packed", "align", "cpu", "section", "naked", "Scope")
	terms = append(terms, operatorTerms...)
	var missing []string
	for _, w := range terms {
		if _, ok := keywordHover(w); !ok {
			missing = append(missing, w)
		}
	}
	t.Logf("%d of %d terms have a rule hover", len(terms)-len(missing), len(terms))
	if len(missing) > 0 {
		t.Errorf("no rule hover for %v (regenerate: go test ./internal/lsp -run TestKeywordTable -update)", missing)
	}
	// The request path: a keyword, operators (compound assignments too), a
	// universe name, a primitive type, `result` in a contract, `fuel` in a
	// const, the declaration modifiers `open` and `packed`, an asm operand
	// word, and `result` as a plain local.
	text := "Clamp(x: int): int\n    ensures result >= 0\n{\n    var result := x + 1\n    result += 2\n    result <<= 1\n    Assert(result > 0)\n    defer {}\n    return result\n}\n\nconst N = Clamp(1) fuel 1000\n\nopen enum Color {\n    Red\n    Green\n}\n\npacked struct P {\n    a: u8\n}\n\nPause() {\n    unsafe \"spin\" {\n        asm(\"pause\", options(nomem, nostack))\n    }\n}\n"
	dir := t.TempDir()
	// A manifest of its own, so that no pkg.mk above the temporary
	// directory makes the file part of a larger package.
	must(t, os.WriteFile(filepath.Join(dir, "pkg.mk"), []byte(fixtureManifest), 0o644))
	p := filepath.Join(dir, "main.mk")
	must(t, writeFile(p, text))
	uri := pathURI(p)
	opt := Options{CacheDir: testCache, Jobs: 2}
	if repo, err := filepath.Abs(filepath.Join("..", "..", "std")); err == nil && isDir(repo) {
		opt.StdDir = repo // std from source: no cold build of the embedded copy
	}
	c := startWith(t, opt)
	c.init(true)
	c.open(uri, text)
	c.diagnostics(uri)
	for _, tc := range []struct {
		at   string
		off  int
		rule bool
	}{
		{"ensures result", 9, true},
		{"defer", 1, true},
		{"x + 1", 2, true},
		{"fuel", 1, true},
		{"result += 2", 7, true},
		{"result <<= 1", 8, true},
		{"Assert(", 1, true},
		{"x: int", 4, true},
		{"open enum", 1, true},
		{"packed struct", 1, true},
		{"nomem", 1, true},
		{"return result", 8, false},
	} {
		line, col := lineOf(t, text, tc.at)
		var h *hover
		c.result(c.call("textDocument/hover", at(uri, line, col+tc.off)), &h)
		got := ""
		if h != nil {
			got = h.Contents.Value
		}
		isRule := strings.Contains(got, "reference") || strings.Contains(got, "memory model")
		if got == "" || isRule != tc.rule {
			t.Errorf("hover at %q: %q, want a rule hover: %v", tc.at, got, tc.rule)
		}
	}
	c.shutdown()
}

// TestPreludeNamesQuoteTheReference: preludeNames is the reference's
// prelude list, name for name.
func TestPreludeNamesQuoteTheReference(t *testing.T) {
	raw, err := os.ReadFile("../../doc/reference.md")
	must(t, err)
	const head = "Prelude, no import needed: `"
	text := string(raw)
	i := strings.Index(text, head)
	if i < 0 {
		t.Fatalf("doc/reference.md has no line starting %q", head)
	}
	rest := text[i+len(head):]
	want := setOf(rest[:strings.IndexByte(rest, '`')])
	for n := range want {
		if !preludeNames[n] {
			t.Errorf("%s is in the reference's prelude and not in preludeNames", n)
		}
	}
	for n := range preludeNames {
		if !want[n] {
			t.Errorf("%s is in preludeNames and not in the reference's prelude", n)
		}
	}
}

// TestLSPLatencyLargeFile logs hover, completion, definition and
// signature help latency (p50 and p95) on a checked file of about 10,000
// lines, at positions spread over the file. It asserts no time: timing
// runs only with MK_TIMING=1, and the numbers are this machine's.
func TestLSPLatencyLargeFile(t *testing.T) {
	const fns = 1500
	var b strings.Builder
	for i := 0; i < fns; i++ {
		fmt.Fprintf(&b, "Sum%d(xs: []int, s: str): int {\n    var t := s.Len()\n    for x in xs {\n        t += x\n    }\n    return t\n}\n\n", i)
	}
	b.WriteString("export Main(sys: &System) {\n    _ = Sum0([1, 2][0..2], \"ab\")\n}\n")
	text := b.String()
	dir := t.TempDir()
	must(t, os.WriteFile(filepath.Join(dir, "pkg.mk"), []byte(fixtureManifest), 0o644))
	p := filepath.Join(dir, "main.mk")
	must(t, writeFile(p, text))
	uri := pathURI(p)
	opt := Options{CacheDir: testCache, Jobs: 2}
	if repo, err := filepath.Abs(filepath.Join("..", "..", "std")); err == nil && isDir(repo) {
		opt.StdDir = repo
	}
	c := startWith(t, opt)
	c.init(true)
	begin := time.Now()
	c.open(uri, text)
	d := c.diagnostics(uri)
	open := time.Since(begin)
	if len(d.Diagnostics) > 0 {
		t.Fatalf("the large file does not check: %v", d.Diagnostics[0].Message)
	}
	lines := strings.Count(text, "\n")
	measure := func(method string, line, col int) (time.Duration, time.Duration) {
		var ds []time.Duration
		for k := 0; k < 30; k++ {
			fn := (k * 97) % fns
			params := at(uri, fn*8+line, col)
			s := time.Now()
			if m := c.call(method, params); m.Error != nil {
				t.Fatalf("%s: %v", method, m.Error)
			}
			ds = append(ds, time.Since(s))
		}
		sort.Slice(ds, func(i, j int) bool { return ds[i] < ds[j] })
		return ds[len(ds)/2], ds[len(ds)*95/100]
	}
	// Line 1 of each function is `    var t := s.Len()`.
	type row struct {
		what, method string
		line, col    int
	}
	var out []string
	for _, r := range []row{
		{"hover on a std method (s.Len)", "textDocument/hover", 1, 17},
		{"hover on a local (t)", "textDocument/hover", 5, 12},
		{"completion after s.", "textDocument/completion", 1, 16},
		{"definition of s.Len", "textDocument/definition", 1, 17},
		{"signature help in s.Len(", "textDocument/signatureHelp", 1, 20},
	} {
		p50, p95 := measure(r.method, r.line, r.col)
		out = append(out, fmt.Sprintf("%s: p50 %v, p95 %v", r.what, p50.Round(time.Microsecond), p95.Round(time.Microsecond)))
	}
	t.Logf("%d lines; open to diagnostics %v\n%s", lines, open.Round(time.Millisecond), strings.Join(out, "\n"))
	c.shutdown()
}
