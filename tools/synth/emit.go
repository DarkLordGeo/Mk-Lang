// Why: an edit-loop measurement is only meaningful on code that looks like
// real Mk, using the features that cost the compiler time.
//
// What: generate, which turns a wsPlan and an edit state into the files of
// one workspace (out): manifest, primText, gensText, mainText, emitModule
// and emitUnit, plus bodyEditSite.
//
// How: every module is ordinary Mk: structs, enums, an interface of its own
// now and then, generic functions over a bound and over Plain, compile-time
// tables, gen lines (std.derive's and a workspace generator,
// base.gens.Weigh), and calls into the modules it imports. All choices come
// from the plan's seeded rng.

package main

import (
	"fmt"
	"sort"
	"strings"
)

// out collects the files of one workspace, by slash-separated path relative
// to the root.
type out struct {
	files map[string]string
	lines int
}

func (o *out) add(path, text string) {
	o.files[path] = text
	o.lines += strings.Count(text, "\n")
}

// paths returns the file paths in byte order.
func (o *out) paths() []string {
	ps := make([]string, 0, len(o.files))
	for p := range o.files {
		ps = append(ps, p)
	}
	sort.Strings(ps)
	return ps
}

// buf is a text builder that counts its lines.
type buf struct {
	b     strings.Builder
	lines int
}

func (b *buf) f(format string, args ...any) {
	s := fmt.Sprintf(format, args...)
	b.b.WriteString(s)
	b.b.WriteByte('\n')
	b.lines += 1 + strings.Count(s, "\n")
}

// The edit sites (applyEdit in main.go): the body line of base's first module, and
// prim.SigProbe with its callers, one in each ordinary module.
const (
	bodyLine0 = 7
)

// generate builds the whole workspace in memory.
func generate(cfg config, st state) *out {
	w := plan(cfg)
	o := &out{files: map[string]string{}}
	base := w.pkgs[0]
	o.add(base.name+"/pkg.mk", manifest(base.name, nil, w, false))
	o.add(base.name+"/prim/prim.mk", primText(st))
	o.add(base.name+"/gens/gens.mk", gensText())
	for _, p := range w.pkgs {
		if p.index != 0 {
			o.add(p.name+"/pkg.mk", manifest(p.name, p.deps, w, false))
		}
		for _, m := range p.modules {
			emitModule(o, w, m, st)
		}
	}
	all := make([]int, len(w.pkgs))
	for i := range all {
		all[i] = i
	}
	o.add("pkg.mk", manifest(w.root, all, w, true))
	o.add("app/main.mk", mainText(w))
	return o
}

func manifest(name string, deps []int, w *wsPlan, root bool) string {
	var b buf
	b.f("// The manifest of %s, a package of the synthetic workspace (tools/synth).", name)
	if root && w.cfg.Workspace {
		// The 1.3 workspace form: empty fields left out, members listed.
		b.f("const Package = pkg.Package(")
		b.f("    name: %q,", name)
		b.f("    mk: \"1.3\",")
		b.f("    bins: [pkg.Bin(name: %q, root: \"app\", subsystem: \"console\")],", name)
		b.f("    deps: [")
		for _, d := range deps {
			b.f("        pkg.Dep(name: %q, path: %q),", w.pkgs[d].name, w.pkgs[d].name)
		}
		b.f("    ],")
		b.f("    members: [")
		for _, p := range w.pkgs {
			b.f("        %q,", p.name)
		}
		b.f("    ],")
		b.f(")")
		return b.b.String()
	}
	b.f("const Package = pkg.Package(")
	b.f("    name: %q,", name)
	if root {
		b.f("    bins: [pkg.Bin(name: %q, root: \"app\", link_script: \"\", cpu: \"\", subsystem: \"console\")],", name)
	} else {
		b.f("    bins: [],")
	}
	if len(deps) == 0 {
		b.f("    deps: [],")
	} else {
		b.f("    deps: [")
		for _, d := range deps {
			path := "../" + w.pkgs[d].name
			if root {
				path = w.pkgs[d].name
			}
			b.f("        pkg.Dep(name: %q, path: %q, url: \"\", hash: \"\"),", w.pkgs[d].name, path)
		}
		b.f("    ],")
	}
	for _, f := range []string{"c_sources", "c_imports", "asm_sources", "targets", "unsafe_modules", "links"} {
		b.f("    %s: [],", f)
	}
	b.f(")")
	return b.b.String()
}

// primText is base.prim, the module every ordinary module imports. SigProbe
// is the signature edit's target.
func primText(st state) string {
	var b buf
	b.f("// base.prim: the interfaces and helpers every module of the workspace uses.")
	b.f("")
	b.f("/// Scorer is implemented by every record type of the workspace.")
	b.f("export interface Scorer {")
	b.f("    Score(&self): i64")
	b.f("}")
	b.f("")
	b.f("/// Weighed is implemented by the records base.gens.Weigh derives.")
	b.f("export interface Weighed {")
	b.f("    Mass(&self): i64")
	b.f("}")
	b.f("")
	b.f("/// Mix folds two values into a small one.")
	b.f("export Mix(a: i64, b: i64): i64 {")
	b.f("    return (a %% 1000003 * 31 + b %% 1000003) %% 1000003")
	b.f("}")
	b.f("")
	b.f("/// SigProbe is called once from every ordinary module (the E2 signature edit).")
	if st.Sig {
		b.f("export SigProbe(x: i64, k: i64): i64 {")
		b.f("    return x %% 1000 + k")
	} else {
		b.f("export SigProbe(x: i64): i64 {")
		b.f("    return x %% 1000 + 1")
	}
	b.f("}")
	return b.b.String()
}

// gensText is base.gens, a workspace generator run at compile time.
func gensText() string {
	return `// base.gens: a generator of the workspace. Weigh emits impl prim.Weighed
// for a struct, summing its i64 fields.
import prim

/// Weigh emits impl prim.Weighed for t: the sum of its i64 fields.
export Weigh(g: mut Gen, t: Type) {
    if t.Kind() != TypeKind.Struct {
        g.FailOn(t, "gens.Weigh: only a struct can be weighed")
        return
    }
    var body := g.Block()
    for f in t.Fields() {
        if f.Type().Text() == "i64" {
            body.Add(quote {
                w += self.$(f.Name()) % 1000
            })
        }
    }
    g.Emit(quote {
        impl prim.Weighed for $(t.Name()) {
            Mass(&self): i64 {
                var w: i64 = 0
                $body
                return w
            }
        }
    })
}
`
}

func mainText(w *wsPlan) string {
	var b buf
	b.f("// The program of the synthetic workspace: it calls every module's All,")
	b.f("// so a build compiles every function.")
	var uses []*modPlan
	for _, p := range w.pkgs {
		uses = append(uses, p.modules...)
	}
	for _, m := range uses {
		b.f("import %s.%s", m.pkg.name, m.name)
	}
	b.f("")
	b.f("export Main(sys: &System): WriteError!void {")
	b.f("    n := Cast[i64](sys.args.Len())")
	b.f("    var acc: i64 = 0")
	for _, m := range uses {
		b.f("    acc = (acc + %s.All(n)) %% 1000003", m.name)
	}
	b.f("    try sys.Print(\"{acc}\\n\", (acc))")
	b.f("}")
	return b.b.String()
}

// emitModule writes one ordinary module: its files, each a run of units.
// A file ends near its share of the module's lines (half a unit either way),
// and its header imports only what its units use, since an unused import is
// E0208.
func emitModule(o *out, w *wsPlan, m *modPlan, st state) {
	r := (&rng{s: w.cfg.Seed}).sub(uint64(m.pkg.index)<<20 | uint64(len(m.name))<<16 | hashName(m.name))
	u, done := 0, 0
	for fi := 0; fi < m.files; fi++ {
		target := (fi+1)*m.lines/m.files - done
		var body buf
		var use uses
		first := u
		for {
			emitUnit(&body, m, u, u == first, r.sub(uint64(u)+1), st, &use)
			u++
			avg := (body.lines + 1) / (u - first)
			if body.lines+avg/2 >= target {
				break
			}
		}
		var b buf
		b.f("// %s.%s, file %d of %d: synthetic code (tools/synth).", m.pkg.name, m.name, fi+1, m.files)
		if use.derive {
			b.f("import std.derive")
		}
		prefix := "base."
		if m.pkg.index == 0 {
			prefix = ""
		}
		b.f("import %sprim", prefix)
		if use.gens {
			b.f("import %sgens", prefix)
		}
		for _, im := range m.imports {
			b.f("import %s", im.importPath(m.pkg))
		}
		if fi == m.files-1 {
			// All runs every unit of the module, so the program reaches all of
			// it; no Run calls another module's Run, so a run stays linear.
			body.f("")
			body.f("/// All runs every unit of %s.", m.name)
			body.f("export All(n: i64): i64 {")
			body.f("    var acc: i64 = 0")
			for i := 0; i < u; i++ {
				body.f("    acc = (acc + Run%d(n + %d)) %% 1000003", i, i%7)
			}
			body.f("    return acc")
			body.f("}")
		}
		text := b.b.String() + body.b.String()
		done += b.lines + body.lines
		o.add(fmt.Sprintf("%s/%s/%s_%d.mk", m.pkg.name, m.name, m.name, fi), text)
	}
	m.units = u
}

// uses records what a file's units need imported.
type uses struct{ derive, gens bool }

func hashName(s string) uint64 {
	var h uint64 = 14695981039346656037
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= 1099511628211
	}
	return h & 0xffff
}

// emitUnit writes unit u of module m: a record, an enum, their functions,
// generic helpers, maybe an interface, a const table and gen lines, and
// Run<u>, which calls into the imported modules.
func emitUnit(b *buf, m *modPlan, u int, first bool, r *rng, st state, use *uses) {
	nf := 1 + r.intn(4)
	nv := 2 + r.intn(4)
	b.f("")
	b.f("/// Rec%d is a record of %s.%s.", u, m.pkg.name, m.name)
	b.f("export struct Rec%d {", u)
	b.f("    export id: i64")
	for i := 1; i <= nf; i++ {
		b.f("    export f%d: i64", i)
	}
	b.f("    export live: bool")
	b.f("}")
	b.f("")
	b.f("/// Kind%d classifies a value.", u)
	b.f("export enum Kind%d {", u)
	for v := 1; v < nv; v++ {
		b.f("    V%d(%s)", v, payload(v))
	}
	b.f("    Empty")
	b.f("}")
	b.f("")
	gEq := r.intn(2) == 0 || u == 0
	gWeigh := r.intn(3) == 0 || u == 0
	gFmt := r.intn(4) == 0
	if gEq {
		b.f("gen derive.Eq(Rec%d)", u)
	}
	if gWeigh {
		b.f("gen gens.Weigh(Rec%d)", u)
	}
	if gFmt {
		b.f("gen derive.Format(Kind%d)", u)
	}
	if gEq || gWeigh || gFmt {
		b.f("")
	}
	use.derive = use.derive || gEq || gFmt
	use.gens = use.gens || gWeigh
	b.f("impl prim.Scorer for Rec%d {", u)
	b.f("    Score(&self): i64 {")
	b.f("        if self.live {")
	b.f("            return prim.Mix(self.id, self.f%d)", nf)
	b.f("        }")
	b.f("        return self.f1 %% 1000")
	b.f("    }")
	b.f("}")
	b.f("")
	b.f("export Rec%d.New(id: i64): Rec%d {", u, u)
	var fs []string
	for i := 1; i <= nf; i++ {
		fs = append(fs, fmt.Sprintf("f%d: id * %d + %d", i, i+1, r.intn(50)))
	}
	b.f("    return Rec%d(id: id, %s, live: id %% 2 == 0)", u, strings.Join(fs, ", "))
	b.f("}")
	b.f("")
	b.f("export Rec%d.Bump(mut self, by: i64) {", u)
	b.f("    self.f1 = (self.f1 + by) %% 1000003")
	b.f("    self.live = !self.live")
	b.f("}")
	b.f("")
	b.f("export Kind%dOf(n: i64): Kind%d {", u, u)
	for v := 1; v < nv; v++ {
		kw := "if"
		if v > 1 {
			kw = "} else if"
		}
		b.f("    %s n %% %d < %d {", kw, nv*10, v*10)
		b.f("        return Kind%d.V%d(%s)", u, v, payloadArgs(v))
	}
	b.f("    }")
	b.f("    return Kind%d.Empty", u)
	b.f("}")
	b.f("")
	b.f("export Kind%dWeight(k: Kind%d): i64 {", u, u)
	b.f("    match k {")
	for v := 1; v < nv; v++ {
		b.f("        .V%d(%s) {", v, payloadNames(v))
		b.f("            return %s", payloadSum(v))
		b.f("        }")
	}
	b.f("        .Empty {")
	b.f("            return 0")
	b.f("        }")
	b.f("    }")
	b.f("}")
	b.f("")
	b.f("export Total%d[T: prim.Scorer](xs: []T): i64 {", u)
	b.f("    var sum: i64 = 0")
	b.f("    for x in &xs {")
	b.f("        sum = (sum + x.Score()) %% 1000003")
	b.f("    }")
	b.f("    return sum")
	b.f("}")
	b.f("")
	b.f("export Pick%d[T: Plain](c: bool, a: T, b: T): T {", u)
	b.f("    if c {")
	b.f("        return a")
	b.f("    }")
	b.f("    return b")
	b.f("}")
	b.f("")
	b.f("Steps%d(xs: []i64, limit: i64): i64 {", u)
	b.f("    var acc: i64 = 0")
	b.f("    for i, x in xs {")
	b.f("        if x > limit {")
	b.f("            acc += x %% %d", 7+r.intn(90))
	b.f("        } else if i %% 2 == 0 {")
	b.f("            acc += Cast[i64](i)")
	b.f("        } else {")
	b.f("            acc -= 1")
	b.f("        }")
	b.f("    }")
	b.f("    return acc")
	b.f("}")
	iface := r.intn(4) == 0
	if iface {
		b.f("")
		b.f("/// Probe%d is an interface of this module.", u)
		b.f("export interface Probe%d {", u)
		b.f("    Probe(&self, n: i64): i64")
		b.f("}")
		b.f("")
		b.f("impl Probe%d for Rec%d {", u, u)
		b.f("    Probe(&self, n: i64): i64 {")
		b.f("        return (self.id + n) %% %d", 11+r.intn(200))
		b.f("    }")
		b.f("}")
		b.f("")
		b.f("export Apply%d[T: Probe%d](x: &T, n: i64): i64 {", u, u)
		b.f("    return x.Probe(n) + x.Probe(n + 1)")
		b.f("}")
	}
	table := r.intn(4) == 0
	if table {
		b.f("")
		b.f("Table%d(): [8]i64 {", u)
		b.f("    var t: [8]i64 = [0; 8]")
		b.f("    for i in 0..8 {")
		b.f("        t[i] = prim.Mix(Cast[i64](i), %d)", u+r.intn(1000))
		b.f("    }")
		b.f("    return t")
		b.f("}")
		b.f("")
		b.f("const Tab%d = Table%d()", u, u)
	}
	// Run<u>: the module's own pieces, then calls into its imports.
	b.f("")
	b.f("export Run%d(n: i64): i64 {", u)
	b.f("    var r := Rec%d.New(n)", u)
	b.f("    q := Rec%d.New(n + 1)", u)
	if bodyEditSite(m, u) {
		b.f("    var acc: i64 = %d", bodyLine0+st.Body)
	} else {
		b.f("    var acc: i64 = %d", r.intn(100))
	}
	b.f("    acc += Kind%dWeight(Kind%dOf(n))", u, u)
	if gEq {
		b.f("    if r.Eq(&q) {")
		b.f("        acc += 1")
		b.f("    }")
	}
	if gWeigh {
		b.f("    acc += r.Mass()")
	}
	if iface {
		b.f("    acc += Apply%d(&q, n)", u)
	}
	b.f("    r.Bump(n)")
	b.f("    xs := [r, q]")
	b.f("    acc += Total%d(xs[..])", u)
	b.f("    ys := [n, n + 1, n * 2, %d]", r.intn(1000))
	b.f("    acc += Steps%d(ys[..], %d)", u, r.intn(20))
	if table {
		b.f("    acc += Pick%d(n > 5, Tab%d[3], Tab%d[4])", u, u, u)
	} else {
		b.f("    acc += Pick%d(n > 5, %d, %d)", u, r.intn(9), r.intn(9))
	}
	if u == 0 {
		if st.Sig {
			b.f("    acc += prim.SigProbe(n, 1)")
		} else {
			b.f("    acc += prim.SigProbe(n)")
		}
	}
	// Calls into imported modules: their records, enums and generics, the
	// generics instantiated with this unit's record.
	for i, im := range m.imports {
		// A file's first unit calls every import, so none is unused.
		if !first && r.intn(2) == 0 {
			continue
		}
		j := r.intn(im.units)
		if u == 0 {
			j = 0
		}
		b.f("    d%d := %s.Rec%d.New(n + %d)", i, im.name, j, i)
		b.f("    acc += d%d.Score()", i)
		b.f("    acc += %s.Kind%dWeight(%s.Kind%dOf(n))", im.name, j, im.name, j)
		b.f("    acc += %s.Total%d(xs[..])", im.name, j)
		b.f("    acc += %s.Pick%d(n > 2, n, acc %% 1000)", im.name, j)
	}
	b.f("    return acc %% 1000003")
	b.f("}")
}

// bodyEditSite is the line the body edit changes: unit 0 of base's first
// ordinary module, a leaf of the import graph (it imports only prim, gens
// and std), in a function no compile-time evaluation runs.
func bodyEditSite(m *modPlan, u int) bool {
	return m.pkg.index == 0 && m == m.pkg.modules[0] && u == 0
}

func payload(v int) string {
	var ps []string
	for i := 0; i < v && i < 3; i++ {
		ps = append(ps, fmt.Sprintf("%s: i64", "abc"[i:i+1]))
	}
	return strings.Join(ps, ", ")
}

func payloadArgs(v int) string {
	var ps []string
	for i := 0; i < v && i < 3; i++ {
		ps = append(ps, fmt.Sprintf("%s: n + %d", "abc"[i:i+1], i))
	}
	return strings.Join(ps, ", ")
}

func payloadNames(v int) string {
	var ps []string
	for i := 0; i < v && i < 3; i++ {
		ps = append(ps, "abc"[i:i+1])
	}
	return strings.Join(ps, ", ")
}

func payloadSum(v int) string {
	return strings.Join(strings.Split(payloadNames(v), ", "), " + ")
}
