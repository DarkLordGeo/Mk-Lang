// Why: running every dogfood on every gate is slow; the quick tier runs
// only the dogfoods whose package, std dependencies or compiler changed
// since they last passed.
//
// What: State and BenchBase (one file per dogfood under
// <work>/dogfood-state/), readState, writeState, selectChanged (-changed),
// digest and stdClosure.
//
// How: the digest of the compiler (its Go sources when mk is built from
// the tree, compilerID; else -mk's build ID), of a dogfood's files and of the std
// modules it reaches (its imports, renamed or not, closed over std's own
// imports, plus the runtime modules every program links) is kept for each
// (dogfood, target, profile) that last passed in a run of the whole folder
// (the gate's; an -only run records nothing), one file per dogfood
// under <work>/dogfood-state/, as tools/gate keeps fuzz-state.json; run
// -changed selects the dogfoods with a run whose digest differs. mk is in
// the digest because every dogfood must pass with the compiler the gate
// checks, so a compiler change selects every dogfood. The same file keeps
// the bench baselines.

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// runtimeModules are linked into every hosted program, whatever it imports.
var runtimeModules = []string{"std.core", "std.raw", "std.rt", "std.sys", "std.task"}

// State is one dogfood's state file.
type State struct {
	Updated time.Time             `json:"updated"`
	Passed  map[string]string     `json:"passed"`          // stateKey -> digest
	Bench   map[string]*BenchBase `json:"bench,omitempty"` // stateKey -> baseline
}

// BenchBase is the bench baseline of one (dogfood, target, profile): the
// best time seen since the bench case's files (Files, their digest) last
// changed, with the mk that took it and whether the machine lock was held.
type BenchBase struct {
	Seconds float64   `json:"wall_seconds"`
	Locked  bool      `json:"machine_locked"`
	When    time.Time `json:"when"`
	Mk      string    `json:"mk"`
	Files   string    `json:"case_files"`
}

// benchFiles is the digest of the bench case's files (cases/bench.*).
func benchFiles(d *Dogfood) string {
	fs, _ := filepath.Glob(filepath.Join(d.Dir, "cases", "bench.*"))
	sort.Strings(fs)
	sum := sha256.New()
	for _, f := range fs {
		b, _ := os.ReadFile(f)
		x := sha256.Sum256(b)
		fmt.Fprintf(sum, "%s\x00%x\n", filepath.Base(f), x)
	}
	return hex.EncodeToString(sum.Sum(nil))
}

func stateKey(name, target, profile string) string { return name + " " + target + " " + profile }

// statePath is one dogfood's state file, so harnesses that run different
// dogfoods at once never write the same file.
func (h *harness) statePath(name string) string {
	return filepath.Join(h.work, "dogfood-state", name+".json")
}

func (h *harness) readState(name string) State {
	var s State
	if b, err := os.ReadFile(h.statePath(name)); err == nil {
		json.Unmarshal(b, &s)
	}
	if s.Passed == nil {
		s.Passed = map[string]string{}
	}
	if s.Bench == nil {
		s.Bench = map[string]*BenchBase{}
	}
	return s
}

// writeState records the runs that passed in this run and the bench
// baselines, per dogfood.
func (h *harness) writeState() error {
	byDog := map[string]bool{}
	for k := range h.passed {
		name, _, _ := strings.Cut(k, " ")
		byDog[name] = true
	}
	for k := range h.bench {
		name, _, _ := strings.Cut(k, " ")
		byDog[name] = true
	}
	for name := range byDog {
		s := h.readState(name)
		for k, v := range h.passed {
			if n, _, _ := strings.Cut(k, " "); n == name {
				s.Passed[k] = v
			}
		}
		for k, v := range h.bench {
			if n, _, _ := strings.Cut(k, " "); n == name {
				s.Bench[k] = v
			}
		}
		s.Updated = time.Now()
		b, err := json.MarshalIndent(s, "", "  ")
		if err != nil {
			return err
		}
		if err := writeAtomic(h.statePath(name), append(b, '\n')); err != nil {
			return err
		}
	}
	return nil
}

// selectChanged keeps the dogfoods with a selected (target, profile) run
// whose digest differs from the state file's, and records why.
func (h *harness) selectChanged(dogs []*Dogfood) []*Dogfood {
	var out []*Dogfood
	for _, d := range dogs {
		st := h.readState(d.Name)
		dg := h.digest(d)
		var stale []string
		for _, t := range d.Targets() {
			if !contains(h.targets, t) {
				continue
			}
			for _, p := range h.profiles {
				if st.Passed[stateKey(d.Name, t, p)] != dg {
					stale = append(stale, t+" "+p)
				}
			}
		}
		if len(stale) > 0 {
			h.why[d.Name] = "changed since its last pass (" + strings.Join(stale, ", ") + ")"
			out = append(out, d)
		} else {
			h.rep.Notes = append(h.rep.Notes, d.Name+": unchanged since its last pass, not run")
		}
	}
	return out
}

// digest is the SHA-256 of the compiler's identity (h.mkID), then of d's files and of the
// files of every std module it reaches, each as "path\x00sha256\n" in path
// order.
func (h *harness) digest(d *Dogfood) string {
	if v, ok := h.digests[d.Name]; ok {
		return v
	}
	var files []string
	walk := func(dir string) {
		filepath.WalkDir(dir, func(p string, e fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if e.IsDir() {
				if n := e.Name(); p != dir && (strings.HasPrefix(n, ".") || n == "gen") {
					return filepath.SkipDir
				}
				return nil
			}
			files = append(files, p)
			return nil
		})
	}
	walk(d.Dir)
	imports := d.Imports
	if hasPtyCase(d.Dir) {
		// A terminal case runs under ptyrun, so its sources and the std
		// modules they import are the case's too.
		walk(h.ptyrunDir())
		imports = append(append([]string(nil), imports...), ptyrunImports(h.ptyrunDir())...)
	}
	for _, m := range h.stdClosure(imports) {
		walk(h.modDir(m))
	}
	sort.Strings(files)
	sum := sha256.New()
	fmt.Fprintf(sum, "mk\x00%s\n", h.mkID)
	for _, f := range files {
		b, err := os.ReadFile(f)
		s := "unreadable"
		if err == nil {
			x := sha256.Sum256(b)
			s = hex.EncodeToString(x[:])
		}
		rel, _ := filepath.Rel(h.root, f)
		fmt.Fprintf(sum, "%s\x00%s\n", filepath.ToSlash(rel), s)
	}
	v := hex.EncodeToString(sum.Sum(nil))
	h.digests[d.Name] = v
	return v
}

// modDir is a std module's directory: std.db.sqlite is std/db/sqlite.
func (h *harness) modDir(mod string) string {
	return filepath.Join(append([]string{h.root}, strings.Split(mod, ".")...)...)
}

// stdClosure is the std modules reached from imports: the runtime modules
// and every std module imported, closed over the imports of std's files.
func (h *harness) stdClosure(imports []string) []string {
	seen := map[string]bool{}
	work := append(append([]string(nil), runtimeModules...), imports...)
	for len(work) > 0 {
		m := work[len(work)-1]
		work = work[:len(work)-1]
		if seen[m] || !strings.HasPrefix(m, "std.") {
			continue
		}
		seen[m] = true
		ents, err := os.ReadDir(h.modDir(m))
		if err != nil {
			continue
		}
		for _, e := range ents {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".mk") {
				continue
			}
			b, err := os.ReadFile(filepath.Join(h.modDir(m), e.Name()))
			if err != nil {
				continue
			}
			for _, im := range importLine.FindAllSubmatch(b, -1) {
				work = append(work, string(im[1]))
			}
		}
	}
	var out []string
	for m := range seen {
		out = append(out, m)
	}
	sort.Strings(out)
	return out
}
