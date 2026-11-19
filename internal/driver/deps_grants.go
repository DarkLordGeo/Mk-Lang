// Why: a fetched package that asks for unsafe code (unsafe modules,
// assembly, C) must not join a build unless the user grants it, and the
// user needs one list of what every package asks for.
//
// What: Asks and asksOf (what a manifest asks for), checkGrants (E0215
// grant-missing), grantLine, the URL redaction redactURL and redactDiags,
// and the perimeter (PerimeterEntry, perimeter, perimeterLines,
// perimeterAnswer).
//
// How: a fetched package (a url package or a source package, vendored or
// not: a vendored copy keeps its source's identity) whose manifest asks for
// unsafe code, that is unsafe_modules, asm_sources, c_sources, c_imports or
// links, takes part in the build only when the root manifest's unsafe_deps
// names it, by source (the url for a url package), or by local name for a
// direct dependency. Otherwise the build stops with E0215 grant-missing at
// the root manifest's Dep that brings the package in, listing what it asks
// for and the line to add. Path packages, workspace members and the root are
// the user's own code and need no grant.
//
// The perimeter (mk query perimeter) is the same list for every package
// of the build: its local names, source and version, and what it asks for.

package driver

import (
	"cmp"
	"fmt"
	"maps"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"mk/internal/diag"
)

// fetched reports whether p is a fetched package (url or source), the
// packages a grant governs.
func (p *depPkg) fetched() bool {
	return strings.HasPrefix(p.identity, "url ") || strings.HasPrefix(p.identity, "source ")
}

// Asks is what one package's manifest asks for beyond safe Mk: its unsafe
// modules, assembly files, C files, C imports and links, each in its
// manifest's order.
type Asks struct {
	UnsafeModules []string `json:"unsafe_modules,omitempty"`
	AsmSources    []string `json:"asm_sources,omitempty"`
	CSources      []string `json:"c_sources,omitempty"`
	CImports      []string `json:"c_imports,omitempty"`
	Links         []string `json:"links,omitempty"`
}

// asksOf is man's Asks.
func asksOf(man *Manifest) Asks {
	var a Asks
	if man == nil {
		return a
	}
	for _, m := range man.UnsafeModules {
		a.UnsafeModules = append(a.UnsafeModules, cmp.Or(m, ".")) // the root module
	}
	for _, s := range man.AsmSources {
		a.AsmSources = append(a.AsmSources, s.Path)
	}
	for _, s := range man.CSources {
		a.CSources = append(a.CSources, s.Path)
	}
	for _, c := range man.CImports {
		a.CImports = append(a.CImports, c.Name)
	}
	for _, l := range man.Links {
		a.Links = append(a.Links, l.Name)
	}
	return a
}

// empty reports whether a asks for nothing.
func (a Asks) empty() bool {
	return len(a.UnsafeModules)+len(a.AsmSources)+len(a.CSources)+len(a.CImports)+len(a.Links) == 0
}

// lines is a, one "<kind> <item>" per item, in the perimeter's order.
func (a Asks) lines() []string {
	var out []string
	add := func(kind string, items []string) {
		for _, it := range items {
			out = append(out, kind+" "+it)
		}
	}
	add("unsafe module", a.UnsafeModules)
	add("assembly file", a.AsmSources)
	add("C file", a.CSources)
	add("C import", a.CImports)
	add("link", a.Links)
	return out
}

// grantSource is the text unsafe_deps names p by: its source, or its url.
func (p *depPkg) grantSource() string {
	if p.source != "" {
		return p.source
	}
	if f := strings.Fields(p.identity); len(f) >= 2 && f[0] == "url" {
		return f[1]
	}
	return ""
}

// rootDep is the root manifest's Dep through which p came into the build:
// the manifest and field to report at.
func (p *depPkg) rootDep() (*Manifest, string) {
	q := p
	// A cycle is refused before grants are checked; the bound keeps a
	// parent chain finite regardless.
	for n := 0; q.parent != nil && n < 10_000; n++ {
		q = q.parent
	}
	return q.viaMan, q.via
}

// checkGrants is E0215 for every fetched package of the build that asks
// for unsafe code the grant manifest (the root's, or the workspace root's)
// does not grant: grants is its grantSet.
func (dw *depWalk) checkGrants(root *Manifest, grants map[string]bool) {
	for _, p := range dw.order {
		if !p.fetched() || grants[p.grantSource()] || p.depTestOnly {
			continue
		}
		a := asksOf(p.man)
		if a.empty() {
			continue
		}
		man, field := p.rootDep()
		if man == nil {
			man, field = root, "deps"
		}
		src := p.grantSource()
		if shown := redactURL(src); shown != src {
			// The url holds credentials, never printed: a direct dependency
			// is granted by its local name; another is named by its url as
			// written, which the message cannot show.
			src = shown
			if n := dw.rootNameOf(p); n != "" {
				src = n
			}
		}
		line := grantLine(root, src)
		what := p.display(p.localName()) // "toml (github.com/someone/toml v1.0.0)"
		d := diag.New(diag.E0215, man.spanOf(field),
			fmt.Sprintf("dependency %s asks for %s, and the root manifest's unsafe_deps does not grant it; to grant it, %s",
				what, strings.Join(a.lines(), ", "), line)).
			Text("package", p.localName()).Text("source", redactURL(p.grantSource())).Alt(line)
		dw.diags = append(dw.diags, d)
	}
}

// rootNameOf is the root manifest's local name for p, "" when p is no
// direct dependency of the root.
func (dw *depWalk) rootNameOf(p *depPkg) string {
	for _, n := range slices.Sorted(maps.Keys(dw.rootLocal)) {
		if dw.rootLocal[n] == p {
			return n
		}
	}
	return ""
}

// redactURL is raw with its user information, query and fragment replaced by
// "***": what a url package's url looks like wherever mk prints or writes
// it. Any other text is returned as is.
func redactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return raw
	}
	if u.User == nil && u.RawQuery == "" && !u.ForceQuery && u.Fragment == "" && !strings.Contains(raw, "#") {
		return raw
	}
	out := u.Scheme + "://"
	if u.User != nil {
		out += "***@"
	}
	out += u.Host + u.EscapedPath()
	if u.RawQuery != "" || u.ForceQuery {
		out += "?***"
	}
	if u.Fragment != "" || strings.Contains(raw, "#") {
		out += "#***"
	}
	return out
}

// redactDiags replaces each url of urls that redactURL changes, in the
// messages, text facts and alternatives of ds and in notes.
func redactDiags(urls map[string]bool, ds []*diag.Diagnostic, notes []string) {
	if len(urls) == 0 {
		return
	}
	// Longest first, so a url that holds another is replaced whole.
	keys := slices.SortedFunc(maps.Keys(urls), func(a, b string) int { return len(b) - len(a) })
	red := func(s string) string {
		for _, k := range keys {
			s = strings.ReplaceAll(s, k, redactURL(k))
		}
		return s
	}
	for _, d := range ds {
		d.Message = red(d.Message)
		for i := range d.Facts {
			if d.Facts[i].Value.Kind == diag.FactText {
				d.Facts[i].Value.Str = red(d.Facts[i].Value.Str)
			}
		}
		for i := range d.Alternatives {
			d.Alternatives[i] = red(d.Alternatives[i])
		}
	}
	for i := range notes {
		notes[i] = red(notes[i])
	}
}

// grantLine is the edit that grants src: the unsafe_deps field to add, or
// the entry to add to it.
func grantLine(root *Manifest, src string) string {
	if len(root.UnsafeDeps) == 0 {
		return "add unsafe_deps: [" + strconv.Quote(src) + "] to pkg.Package"
	}
	return "add " + strconv.Quote(src) + " to unsafe_deps"
}

// PerimeterEntry is one package of the build in mk query perimeter: its
// local names, source (a path package's directory relative to the
// workspace root) and version, and what it asks for. Granted is true for
// a fetched package the root grants, and for every package that needs no
// grant.
type PerimeterEntry struct {
	Names   []string `json:"names"`
	Source  string   `json:"source"`
	Version string   `json:"version,omitempty"`
	Fetched bool     `json:"fetched"`
	Granted bool     `json:"granted"`
	Asks
}

// perimeter is the build's perimeter after a check: the root first (when
// it has a manifest), then every dependency in build order.
func (s *Session) perimeter() []PerimeterEntry {
	var out []PerimeterEntry
	root := s.walkedMan
	if root != nil {
		out = append(out, PerimeterEntry{Names: []string{root.Name}, Source: ".", Granted: true, Asks: asksOf(root)})
	}
	for _, p := range s.walked {
		if p.depTestOnly {
			continue // never granted or linked by this build
		}
		e := PerimeterEntry{Names: slices.Clone(p.names), Source: redactURL(p.grantSource()), Version: p.version,
			Fetched: p.fetched(), Asks: asksOf(cmp.Or(p.wholeMan, p.man))}
		slices.Sort(e.Names)
		if !e.Fetched {
			e.Source = p.rel
			e.Granted = true
		} else {
			e.Granted = s.walkedGrants[p.grantSource()]
		}
		out = append(out, e)
	}
	return out
}

// perimeterLines is the perimeter as text, one block per package: a head
// line `<names> (<source> v<version>)` and one indented line per item it
// asks for, or "  (nothing unsafe)".
func perimeterLines(es []PerimeterEntry) []string {
	var out []string
	for _, e := range es {
		head := strings.Join(e.Names, ", ") + " (" + e.Source
		if e.Version != "" {
			head += " v" + e.Version
		}
		head += ")"
		if e.Fetched && !e.Asks.empty() && !e.Granted {
			head += " not granted"
		}
		out = append(out, head)
		ls := e.Asks.lines()
		if len(ls) == 0 {
			out = append(out, "  (nothing unsafe)")
		}
		for _, l := range ls {
			out = append(out, "  "+l)
		}
	}
	return out
}

// perimeterAnswer is mk query perimeter's answer: every line, never
// capped, and the entries for --json.
func perimeterAnswer(s *Session) *QueryAnswer {
	pe := s.perimeter()
	return &QueryAnswer{Query: "perimeter", Found: true, Answer: perimeterLines(pe), Perimeter: pe}
}

// localName is the name a reader knows p by: the first of its local names
// in name order (deterministic), else its first name.
func (p *depPkg) localName() string {
	if len(p.names) == 0 {
		return p.name
	}
	return slices.Min(p.names)
}
