// Why: every token that names something must get a typed hover, a definition and
// an implementation, across std and the test programs.
// What: TestLSPStdSweep and its tallies by kind and module.
// How: the default run sweeps a fixed sample; MK_LSP_FULL=1 sweeps every std file
// and tests/programs. A miss fails unless p2Allowed lists it with its reason.

package lsp

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// p2Sample are the default run's files: one or two per kind of module.
var p2Sample = []string{
	"std/compress/compress.mk", "std/compress/inflate.mk", "std/http/parse.mk",
	"std/core/core_str.mk", "std/core/coll_list.mk", "std/core/core_iface.mk",
	"std/rt/region.mk", "std/atomic/order.mk", "std/atomic/atomic_t.mk",
	"std/fs/fs.mk", "std/cli/args.mk", "std/sys/windows/mem.mk",
	"tests/programs/hello.mk", "tests/programs/copy.mk",
}

// p2Allowed are the misses that are not defects, by kind and the name, or
// by module and kind, each with its reason.
var p2Allowed = map[string]string{
	"definition builtin-method":                               "the methods of ?T and E!T (Must, Or, IsSome, IsNone) have no source declaration; hover shows the reference's row",
	"typed std/sys/linux local":                               "a linux sys file is not loaded by a windows check, so its locals have only the text's types",
	"typed std/sys/linux param":                               "as above",
	"typed std/free/sys local":                                "freestanding sys files are not loaded by a hosted check",
	"typed std/free/sys param":                                "as above",
	"typed std/sys/linux field":                               "as above",
	"definition std/sys/linux unresolved member":              "as above: a member of a value only the checker types",
	"implementation std/sys/linux unresolved member":          "as above",
	"hover std/sys/linux unresolved member":                   "as above",
	"definition std/free/sys unresolved member":               "as above",
	"implementation std/free/sys unresolved member":           "as above",
	"hover std/free/sys unresolved member":                    "as above",
	"definition std/free unresolved member":                   "std.free is the freestanding target's, which a hosted check does not load: a member of a value only the checker types has no answer",
	"implementation std/free unresolved member":               "as above",
	"hover std/free unresolved member":                        "as above",
	"typed std/free local":                                    "as above",
	"typed std/free param":                                    "as above",
	"definition tests unresolved member":                      "a std test file (tests_*.mk) is not loaded by the editor's check of std, which builds no tests, so a member of a value only the checker types has no answer",
	"implementation tests unresolved member":                  "as above",
	"hover tests unresolved member":                           "as above",
	"typed std/db/sqlite local":                               "std.db.sqlite is importable only by a package that declares the C import sqlite3 (E0202 needs-cimport), so the std check does not load it: its locals have the text's types only",
	"typed std/db/sqlite param":                               "as above",
	"typed std/db/sqlite field":                               "as above",
	"definition std/db/sqlite unresolved member":              "as above: a member of a value the text does not type",
	"implementation std/db/sqlite unresolved member":          "as above",
	"hover std/db/sqlite unresolved member":                   "as above",
	"typed tests/programs/notesdb local":                      "its pkg.mk names the C source sqlite3.c, which is not in the repository (E0202), so the package does not check: its locals have the text's types only",
	"typed tests/programs/notesdb param":                      "as above",
	"definition tests/programs/notesdb unresolved member":     "as above",
	"implementation tests/programs/notesdb unresolved member": "as above",
	"hover tests/programs/notesdb unresolved member":          "as above",
	"definition universe":                                     "universe names (true, some, Assert, ...) are the language's, declared nowhere",
	"definition keyword":                                      "a keyword has its rule, not a declaration",
	"definition operator":                                     "an operator has its rule, not a declaration",
	"definition primitive":                                    "a primitive type is the language's, declared nowhere",
	"implementation universe":                                 "as definition",
	"implementation builtin-method":                           "as definition",
	"definition quoted":                                       "a quote block is a template: its names are bound where the generator emits the code",
	"implementation quoted":                                   "as definition",
	"hover quoted":                                            "as definition",
	"typed quoted":                                            "as definition",
	"definition c-import":                                     "a C import's names are translated from C headers by mk cimport, so no .mk file declares them",
	"implementation c-import":                                 "as definition",
	"typed c-import":                                          "as definition",
	"hover c-import":                                          "as definition",
	"implementation keyword":                                  "as definition",
	"implementation operator":                                 "as definition",
	"implementation primitive":                                "as definition",
}

// p2Tally counts one kind's tokens and what answered.
type p2Tally struct{ n, def, hover, typed, impl, typedef int }

type p2Result struct {
	mu       sync.Mutex
	kinds    map[string]*p2Tally
	modules  map[string]*p2Tally
	fails    map[string][]string // by "what kind" key
	allowed  map[string]int
	files    int
	elapsed  time.Duration
	firstAns []string
	all      []string // every miss, written to MK_LSP_MISSES
}

func (r *p2Result) tally(m map[string]*p2Tally, k string) *p2Tally {
	t := m[k]
	if t == nil {
		t = &p2Tally{}
		m[k] = t
	}
	return t
}

// miss records a missing answer unless it is allowed.
func (r *p2Result) miss(what, kind, module, where string) {
	keys := []string{what + " " + kind, what + " " + module + " " + kind}
	if strings.Contains(where, "/tests_") {
		keys = append(keys, what+" tests "+kind)
	}
	for _, key := range keys {
		if _, ok := p2Allowed[key]; ok {
			r.allowed[key]++
			return
		}
	}
	k := what + " " + kind
	r.all = append(r.all, k+" "+where)
	if len(r.fails[k]) < 8 {
		r.fails[k] = append(r.fails[k], where)
	} else {
		r.fails[k] = append(r.fails[k][:8], where)[:8]
	}
	r.tally(r.kinds, "~misses "+k).n++
}

func TestLSPStdSweep(t *testing.T) {
	repo, err := filepath.Abs(filepath.Join("..", ".."))
	must(t, err)
	std := filepath.Join(repo, "std")
	if _, err := os.Stat(filepath.Join(std, "compress", "inflate.mk")); err != nil {
		t.Skip("no std in this tree")
	}
	var files []string
	if os.Getenv("MK_LSP_FULL") != "" {
		for _, root := range []string{"std", filepath.Join("tests", "programs")} {
			_ = filepath.WalkDir(filepath.Join(repo, root), func(p string, e os.DirEntry, err error) error {
				if err == nil && e.IsDir() && strings.HasPrefix(e.Name(), ".") {
					return filepath.SkipDir
				}
				if err == nil && !e.IsDir() && strings.HasSuffix(p, ".mk") {
					files = append(files, p)
				}
				return nil
			})
		}
	} else {
		for _, f := range p2Sample {
			files = append(files, filepath.Join(repo, filepath.FromSlash(f)))
		}
	}
	// By module directory, spread over workers so each opens one module at
	// a time (a std root's check imports the open files' modules).
	byMod := map[string][]string{}
	for _, f := range files {
		byMod[filepath.Dir(f)] = append(byMod[filepath.Dir(f)], f)
	}
	var mods []string
	for m := range byMod {
		mods = append(mods, m)
	}
	sort.Strings(mods)
	workers := 4
	res := &p2Result{kinds: map[string]*p2Tally{}, modules: map[string]*p2Tally{}, fails: map[string][]string{}, allowed: map[string]int{}}
	start := time.Now()
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		var mine []string
		for i, m := range mods {
			if i%workers == w {
				mine = append(mine, m)
			}
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			c := startWith(t, Options{StdDir: std, CacheDir: testCache, Jobs: 2})
			c.init(true)
			for _, m := range mine {
				p2Module(t, c, repo, byMod[m], res)
			}
			c.shutdown()
		}()
	}
	wg.Wait()
	res.elapsed = time.Since(start)
	p2Report(t, res)
}

// p2Module opens a module's files, sweeps each, and closes them.
func p2Module(t *testing.T, c *client, repo string, files []string, res *p2Result) {
	sort.Strings(files)
	texts := map[string]string{}
	for i, p := range files {
		raw, err := os.ReadFile(p)
		must(t, err)
		texts[p] = string(raw)
		begin := time.Now()
		c.open(pathURI(p), texts[p])
		c.diagnostics(pathURI(p))
		if i == 0 {
			rel, _ := filepath.Rel(repo, p)
			res.mu.Lock()
			res.firstAns = append(res.firstAns, fmt.Sprintf("%s: open to diagnostics %v", filepath.ToSlash(rel), time.Since(begin).Round(time.Millisecond)))
			res.mu.Unlock()
		}
	}
	for _, p := range files {
		p2File(t, c, repo, p, texts[p], res)
	}
	for _, p := range files {
		c.notify("textDocument/didClose", doc(pathURI(p)))
	}
}

// p2File asks every name-bearing token of one file.
func p2File(t *testing.T, c *client, repo, p, text string, res *p2Result) {
	rel, _ := filepath.Rel(repo, p)
	rel = filepath.ToSlash(rel)
	module := filepath.ToSlash(filepath.Dir(rel))
	uri := pathURI(p)
	toks := scan(text)
	ct := code(toks)
	ls := newLines(text)
	ds := decls(text, toks)
	declAt := map[int]decl{}
	var walk func(ds []decl)
	walk = func(xs []decl) {
		for _, d := range xs {
			declAt[d.NameStart] = d
			walk(d.Children)
		}
	}
	walk(ds)
	textOf := func(path string) string {
		if pathKey(path) == pathKey(p) {
			return text
		}
		raw, _ := os.ReadFile(path)
		return string(raw)
	}
	quotes := quoteRanges(text, ct)
	cBinds := map[string]bool{}
	for alias, mod := range imports(text, ct) {
		if strings.HasPrefix(mod, "c.") {
			cBinds[alias] = true
		}
	}
	res.mu.Lock()
	res.files++
	res.mu.Unlock()
	for i, tk := range ct {
		w := text[tk.Start:tk.End]
		syntactic := ""
		switch {
		case tk.Kind == tIdent && asmWords[w] && insideAsm(text, ct, i):
			syntactic = "keyword" // an asm operand word: its rule, no declaration
		case tk.Kind == tIdent && (keywords[w] || flowWords[w]):
			syntactic = "keyword"
		case tk.Kind == tIdent && primTypes[w]:
			syntactic = "primitive"
		case tk.Kind == tIdent && p2Universe[w]:
			if w == "_" {
				continue
			}
			syntactic = "universe"
		case tk.Kind == tIdent:
		case tk.Kind == tPunct:
			if _, ok := keywordDocs[w]; !ok {
				continue
			}
			syntactic = "operator"
		default:
			continue
		}
		pos := ls.pos(tk.Start, true)
		where := fmt.Sprintf("%s:%d:%d %s", rel, pos.Line+1, pos.Character+1, w)
		params := map[string]any{"textDocument": map[string]any{"uri": uri}, "position": pos}
		var hv *hover
		c.result(c.call("textDocument/hover", params), &hv)
		hoverText := ""
		if hv != nil {
			hoverText = hv.Contents.Value
		}
		var defs, impls []Location
		if syntactic == "" {
			c.result(c.call("textDocument/definition", params), &defs)
			c.result(c.call("textDocument/implementation", params), &impls)
		}
		kind := syntactic
		if inQuote(quotes, tk.Start) && syntactic == "" {
			kind = "quoted"
		} else if syntactic == "" && cImport(text, ct, i, cBinds) {
			kind = "c-import"
		} else if kind == "" {
			kind = p2KindOf(text, ct, i, declAt, defs, textOf, hoverText)
		}
		res.mu.Lock()
		for _, tl := range []*p2Tally{res.tally(res.kinds, kind), res.tally(res.modules, module)} {
			tl.n++
			if len(defs) > 0 || syntactic != "" {
				tl.def++
			}
			if strings.TrimSpace(hoverText) != "" {
				tl.hover++
			}
			if p2Typed(kind, w, hoverText) {
				tl.typed++
			}
			if len(impls) > 0 || syntactic != "" {
				tl.impl++
			}
		}
		if strings.TrimSpace(hoverText) == "" {
			res.miss("hover", kind, module, where)
		} else if !p2Typed(kind, w, hoverText) {
			res.miss("typed", kind, module, where)
		}
		if syntactic == "" {
			if len(defs) == 0 {
				res.miss("definition", kind, module, where)
			}
			if len(impls) == 0 {
				res.miss("implementation", kind, module, where)
			}
			// The first answer is the declaration; a generated one's then
			// lists its gen line and the generator (genLines).
			if len(defs) > 0 && !p2Lands(defs[0], w, textOf) {
				l := defs[0]
				res.miss("lands", kind, module, fmt.Sprintf("%s -> %s:%d", where, l.URI, l.Range.Start.Line+1))
			}
		}
		res.mu.Unlock()
	}
}

// p2KindOf names what a resolved name is, from the declaration its
// definition lands on, its hover and its syntax.
func p2KindOf(text string, ct []token, i int, declAt map[int]decl, defs []Location, textOf func(string) string, hv string) string {
	w := text[ct[i].Start:ct[i].End]
	line := text[strings.LastIndexByte(text[:ct[i].Start], '\n')+1 : ct[i].Start]
	switch {
	case strings.HasPrefix(strings.TrimSpace(line), "import"):
		return "import"
	case strings.HasPrefix(hv, "```mk\nimport "):
		return "import"
	case strings.HasPrefix(hv, "```mk\n(param) "):
		return "param"
	case strings.HasPrefix(hv, "```mk\n(local) "):
		return "local"
	case strings.HasPrefix(hv, "```mk\n(label) "):
		return "label"
	case strings.HasPrefix(hv, "```mk\n(record field) "):
		return "field"
	}
	if _, ok := builtinMembers[w]; ok && isMemberAt(text, ct, i) && len(defs) == 0 {
		return "builtin-method"
	}
	chain := isMemberAt(text, ct, i)
	if len(defs) == 0 {
		if chain {
			return "unresolved member"
		}
		return "unresolved name"
	}
	p, ok := uriPath(defs[0].URI)
	if !ok {
		return "unresolved name"
	}
	dt := textOf(p)
	dl := newLines(dt)
	off := dl.off(defs[0].Range.Start, true)
	if off == 0 && defs[0].Range.Start.Line == 0 {
		return "import"
	}
	for _, d := range decls(dt, scan(dt)) {
		if k := p2DeclKind(d, off); k != "" {
			if chain && k == "field" {
				return "field"
			}
			if strings.HasPrefix(p, filepath.Join("std", "core")) || strings.Contains(filepath.ToSlash(p), "/std/core/") {
				if k == "method" && chain {
					return "method"
				}
			}
			return k
		}
	}
	// No declaration of the scan at the target: a generic parameter, a
	// label, a pattern binding, a payload field.
	rest := strings.TrimLeft(dt[min(len(dt), off+len(w)):], " \t")
	switch {
	case strings.HasPrefix(rest, ":"):
		return "field"
	case strings.HasPrefix(strings.TrimSpace(dt[max(0, off-1):off]), "[") || strings.HasPrefix(strings.TrimSpace(dt[max(0, off-2):off]), ","):
		return "generic"
	}
	return "binding"
}

// p2DeclKind is the kind of the declaration (or child) of d named at off.
func p2DeclKind(d decl, off int) string {
	if d.NameStart == off {
		switch {
		case d.Detail == "error":
			return "error"
		case d.Kind == symField:
			return "field"
		case d.Kind == symEnumMember:
			return "variant"
		case d.Kind == symMethod:
			return "method"
		case d.Kind == symFunction:
			return "function"
		case d.Detail == "":
			return "decl"
		}
		return d.Detail
	}
	for _, ch := range d.Children {
		if k := p2DeclKind(ch, off); k != "" {
			return k
		}
	}
	return ""
}

// p2Typed reports whether a hover says what the name is, for its kind: a
// local, a parameter or a field with its type; a function or a method
// with its signature; a type with its declaration.
func p2Typed(kind, name, hv string) bool {
	switch kind {
	case "local", "param":
		_, t, _ := strings.Cut(strings.SplitN(hv, "\n", 3)[1], name+": ")
		return strings.TrimSpace(t) != ""
	case "field":
		return strings.Contains(hv, name+": ") || strings.Contains(hv, name+":") || strings.Contains(hv, "(record field) "+name)
	case "function", "method", "builtin-method":
		return strings.Contains(hv, name) && strings.Contains(hv, "(")
	case "struct", "enum", "error", "interface", "type", "variant":
		return strings.Contains(hv, name)
	}
	return strings.TrimSpace(hv) != ""
}

func p2Report(t *testing.T, res *p2Result) {
	t.Logf("swept %d files in %v (this test's run)", res.files, res.elapsed.Round(time.Second))
	sort.Strings(res.firstAns)
	for _, l := range res.firstAns {
		t.Logf("  %s", l)
	}
	row := func(k string, tl *p2Tally) {
		t.Logf("%-26s %6d: hover %6d, typed %6d, definition %6d, implementation %6d", k, tl.n, tl.hover, tl.typed, tl.def, tl.impl)
	}
	var ks []string
	for k := range res.kinds {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	t.Logf("per kind:")
	for _, k := range ks {
		if !strings.HasPrefix(k, "~") {
			row(k, res.kinds[k])
		}
	}
	var ms []string
	for m := range res.modules {
		ms = append(ms, m)
	}
	sort.Strings(ms)
	t.Logf("per module:")
	for _, m := range ms {
		row(m, res.modules[m])
	}
	var as []string
	for k, n := range res.allowed {
		as = append(as, fmt.Sprintf("%s: %d (%s)", k, n, p2Allowed[k]))
	}
	sort.Strings(as)
	for _, a := range as {
		t.Logf("allowed %s", a)
	}
	var fk []string
	for k := range res.fails {
		fk = append(fk, k)
	}
	sort.Strings(fk)
	if out := os.Getenv("MK_LSP_MISSES"); out != "" {
		sort.Strings(res.all)
		must(t, os.WriteFile(out, []byte(strings.Join(res.all, "\n")+"\n"), 0o644))
	}
	for _, k := range fk {
		t.Errorf("%s: %d misses, for example %s", k, res.kinds["~misses "+k].n, strings.Join(res.fails[k], "; "))
	}
}

// p2Universe are names the language gives (the universe
// names and contextual words), which no declaration spells.
var p2Universe = setOf("true false some none ok err fields Assert Panic Cast Trunc SizeOf AlignOf Target Plain Send Share Fields EvalArena self Self result fuel packed naked open cpu section align _")

// p2Lands reports whether a definition's range spells name, or is a
// module's start (an import binding's definition).
func p2Lands(l Location, name string, textOf func(string) string) bool {
	p, ok := uriPath(l.URI)
	if !ok {
		return false
	}
	ls := newLines(textOf(p))
	a := ls.off(l.Range.Start, true)
	b := ls.off(l.Range.End, true)
	if a == b && l.Range.Start.Line == 0 && l.Range.Start.Character == 0 {
		return true
	}
	if b <= len(ls.s) && a < b && strings.HasPrefix(ls.s[a:b], "gen ") {
		return true // the gen line that emits a generated declaration
	}
	return b <= len(ls.s) && a < b && ls.s[a:b] == name
}

// quoteRanges are the byte ranges of a file's `quote { }` blocks.
func quoteRanges(text string, ct []token) [][2]int {
	var out [][2]int
	for i := range ct {
		if word(text, ct, i) == "quote" && word(text, ct, i+1) == "{" {
			if j := matching(text, ct, i+1); j > i {
				out = append(out, [2]int{ct[i+1].Start, ct[j].End})
			}
		}
	}
	return out
}

func inQuote(qs [][2]int, off int) bool {
	for _, q := range qs {
		if q[0] <= off && off < q[1] {
			return true
		}
	}
	return false
}

// cImport reports whether ct[i] is a C import's name: on an `import c.`
// line, a binding of one, or a member of one (`sqlite3.Open`).
func cImport(text string, ct []token, i int, binds map[string]bool) bool {
	line := text[strings.LastIndexByte(text[:ct[i].Start], '\n')+1:]
	if j := strings.IndexByte(line, '\n'); j >= 0 {
		line = line[:j]
	}
	if strings.HasPrefix(strings.TrimSpace(line), "import c.") {
		return true
	}
	w := word(text, ct, i)
	if binds[w] && word(text, ct, i+1) == "." {
		return true
	}
	return i >= 2 && word(text, ct, i-1) == "." && binds[word(text, ct, i-2)]
}
