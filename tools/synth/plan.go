// Why: a synthetic workspace must be the same on every run with the same
// flags, so its shape is decided before any text is written.
//
// What: config (the flags), state (the edit applied), rng, and plan, which
// lays out the packages (pkgPlan), modules (modPlan) and import graph of a
// wsPlan.
//
// How: a seeded rng with sub-streams per tag picks sizes and imports, so
// one set of flags always gives one workspace.

package main

import "fmt"

// config is everything the output depends on. Two runs with equal configs
// write byte-identical trees.
type config struct {
	Lines     int    // the target total of .mk lines, every file counted
	Packages  int    // the number of packages, the root included
	Seed      uint64 // the choices: sizes, imports, fields, variants, gen lines
	Workspace bool   // write the 1.3 workspace form (a root members list)
}

// state is the edit applied on top of the baseline (applyEdit). The zero
// value is the baseline.
type state struct {
	Body int  // the leaf body edit's revision; 0 is the baseline
	Sig  bool // the public signature edit is applied
}

// rng is SplitMix64: a fixed algorithm, so the output never depends on the
// Go release.
type rng struct{ s uint64 }

func (r *rng) next() uint64 {
	r.s += 0x9e3779b97f4a7c15
	z := r.s
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
	z = (z ^ (z >> 27)) * 0x94d049bb133111eb
	return z ^ (z >> 31)
}

// intn returns a value in [0, n); n > 0.
func (r *rng) intn(n int) int { return int(r.next() % uint64(n)) }

// sub derives an independent stream, so a choice made for one module does
// not shift the choices of every module after it.
func (r *rng) sub(tag uint64) *rng {
	return &rng{s: r.s ^ (tag * 0xd6e8feb86659fd93) ^ 0x5851f42d4c957f2d}
}

// pkgNames follow the layout of LuxJS, a JavaScript engine written in Mk
// (its src/ directories): base is the leaf every other package reaches, lux
// the root that holds the program.
var pkgNames = []string{"base", "heap", "object", "syntax", "bytecode", "runtime", "interp"}

// pkgWeights give a few large packages and some small ones (per mille of
// the lines, per package position; the root takes rootWeight).
var pkgWeights = []int{60, 110, 190, 120, 80, 210, 230}

const rootWeight = 0 // the root holds only the program's main module

// Sizes of the pieces, in lines (targets: units end where they end).
const (
	moduleLines = 2400
	fileLines   = 600
	layerWidth  = 4
)

type pkgPlan struct {
	index   int
	name    string // the package name and its directory under the root
	deps    []int  // indexes of the packages its manifest names
	lines   int    // target lines of its ordinary modules
	modules []*modPlan
}

type modPlan struct {
	pkg     *pkgPlan
	name    string // the directory and the last import segment, unique in the workspace
	layer   int
	imports []*modPlan // ordinary modules this one imports (prim and gens apart)
	files   int
	lines   int // target lines
	units   int // filled when the module is emitted
}

// importPath is the module's import path from a module of package from.
func (m *modPlan) importPath(from *pkgPlan) string {
	if m.pkg == from {
		return m.name
	}
	return m.pkg.name + "." + m.name
}

type wsPlan struct {
	cfg  config
	pkgs []*pkgPlan // every non-root package; pkgs[0] is base
	root string     // the root package's name
}

func pkgName(i int) string {
	if i < len(pkgNames) {
		return pkgNames[i]
	}
	return fmt.Sprintf("pkg%02d", i)
}

func pkgWeight(i int) int {
	if i < len(pkgWeights) {
		return pkgWeights[i]
	}
	return pkgWeights[1+i%(len(pkgWeights)-1)]
}

// plan lays the workspace out: packages 0..P-2 are libraries, the root is
// the P-th. Package i names base, i-1 and sometimes i-2 as dependencies,
// so the package graph is a deep chain with a common leaf.
func plan(cfg config) *wsPlan {
	r := &rng{s: cfg.Seed}
	nlib := cfg.Packages - 1
	w := &wsPlan{cfg: cfg, root: "lux"}
	total := 0
	for i := 0; i < nlib; i++ {
		total += pkgWeight(i)
	}
	total += rootWeight
	// Overhead: manifests, prim, gens and the main module.
	budget := cfg.Lines - fixedLines(nlib)
	if budget < nlib*moduleLines/8 {
		budget = nlib * moduleLines / 8
	}
	for i := 0; i < nlib; i++ {
		p := &pkgPlan{index: i, name: pkgName(i)}
		p.lines = budget * pkgWeight(i) / total
		if i > 0 {
			p.deps = append(p.deps, 0)
			if i-1 != 0 {
				p.deps = append(p.deps, i-1)
			}
			if i-2 > 0 && r.intn(2) == 0 {
				p.deps = append(p.deps, i-2)
			}
		}
		w.pkgs = append(w.pkgs, p)
	}
	for _, p := range w.pkgs {
		planModules(w, p, r.sub(uint64(p.index)+1))
	}
	return w
}

// fixedLines estimates the lines outside ordinary modules.
func fixedLines(nlib int) int {
	return 14*(nlib+1) + 120 + nlib*30
}

func planModules(w *wsPlan, p *pkgPlan, r *rng) {
	n := (p.lines + moduleLines/2) / moduleLines
	if n < 1 {
		n = 1
	}
	// LuxJS's names are unique in four letters; pkgNN names are kept whole,
	// so every module name is unique in the workspace.
	short := p.name
	if p.index < len(pkgNames) && len(short) > 4 {
		short = short[:4]
	}
	for j := 0; j < n; j++ {
		m := &modPlan{pkg: p, name: fmt.Sprintf("%s%03d", short, j), layer: j / layerWidth}
		m.lines = p.lines / n
		m.files = (m.lines + fileLines - 1) / fileLines
		if m.files < 1 {
			m.files = 1
		}
		p.modules = append(p.modules, m)
	}
	for j, m := range p.modules {
		mr := r.sub(uint64(j) + 1)
		if m.layer > 0 {
			lo := (m.layer - 1) * layerWidth
			hi := lo + layerWidth
			// One fixed edge covers each module of the layer below; a second,
			// chosen, makes the graph more than a tree.
			a := p.modules[lo+j%layerWidth]
			m.imports = append(m.imports, a)
			b := p.modules[lo+mr.intn(hi-lo)]
			if b != a {
				m.imports = append(m.imports, b)
			}
		}
		// Edges into the packages this one depends on.
		for di, d := range p.deps {
			dp := w.pkgs[d]
			if m.layer == 0 || mr.intn(3) == 0 || (di == len(p.deps)-1 && j%2 == 0) {
				m.imports = append(m.imports, dp.modules[mr.intn(len(dp.modules))])
			}
		}
	}
}
