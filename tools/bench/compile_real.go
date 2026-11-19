// Why: a generated program shows the compiler's speed on one shape; real
// programs show what a user waits for, and whether a new mk is faster than
// an older one on them.
//
// What: the real-program compile table (tests/bench/README.md "Compile
// speed"): compileReal, measureReal, realSetup, copyPackage and renderReal.
// Real Mk programs, dogfoods of the repository used unchanged, are built
// cold (a fresh MK_CACHE, so std is checked and compiled again), after a
// one-body edit and with no change, in the dev and release profiles, and
// `mk check` runs after a body edit in the dev cache.
//
// How: Mk only, so the table compares mk binaries: the measured mk and any
// older ones (-vs), each in its own caches, interleaved per program and
// profile. Every build is checked by running the program: the edit extends
// one string literal in a function body with a token that is new at every
// build, and the run must print it, so a stale build fails the cell instead
// of being timed.
//
//	tools/bench compile -real [-programs kvd,lox] [-vs v1.2.0=<mk>,...]          provisional times
//	tools/bench compile -real -publish [-date D] [-vs ...]                       results/<date>/compile-real

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

// realProgram is one program of the table and its edit site.
type realProgram struct {
	ID   string
	Path string // repository-relative package directory, with one bin
	// File is the edited file, package-relative and slash-separated.
	// Context is found in it exactly once, inside a function body, and
	// Literal is a string literal found in Context exactly once, which the
	// run Args prints (exit status 0).
	File, Context, Literal string
	Args                   []string
}

// realPrograms are the table's programs: kvd and lox (compile.md's
// profiles), and orderbook and pack, the two other single-bin dogfoods
// that real.md built in release. Each edit site is the format string or
// text of the program's help output, written in its Main or a function
// Main calls, so the edit is a body edit and the help run prints it.
var realPrograms = []realProgram{
	{ID: "kvd", Path: "Dogfoods/kvd", File: "src/main/main.mk",
		Context: `sys.Print("{h}", (h: Help))`, Literal: `"{h}"`, Args: []string{"--help"}},
	{ID: "lox", Path: "Dogfoods/lox", File: "src/main/main.mk",
		Context: `try t.out.Str("usage: lox [options] [script]\n\n")`, Literal: `"usage: lox [options] [script]\n\n"`, Args: []string{"--help"}},
	{ID: "orderbook", Path: "Dogfoods/orderbook", File: "src/main/main.mk",
		Context: `sys.Print("{text}", (text: Usage))`, Literal: `"{text}"`, Args: []string{"--help"}},
	{ID: "pack", Path: "Dogfoods/pack", File: "src/main/main.mk",
		Context: `try sys.Print("usage: pack {command} [options]\n\n", (command))`, Literal: `"usage: pack {command} [options]\n\n"`, Args: []string{"create", "--help"}},
}

func findRealProgram(id string) (realProgram, bool) {
	for _, p := range realPrograms {
		if p.ID == id {
			return p, true
		}
	}
	return realProgram{}, false
}

// token is the text edit n adds to the literal; undefined is the unknown
// name a check row's bad edit n writes in its place.
func (p realProgram) token(n int) string     { return fmt.Sprintf("mkbench-edit-%d", n) }
func (p realProgram) undefined(n int) string { return fmt.Sprintf("mkbenchUndefined%d", n) }

// editText is File's text (orig, the package's own) at edit n: Literal
// with the token and a line feed added before its closing quote, or, when
// bad, Literal replaced by the unknown name.
func (p realProgram) editText(orig string, n int, bad bool) (string, error) {
	if c := strings.Count(orig, p.Context); c != 1 {
		return "", fmt.Errorf("%s: the edit site %q occurs %d times in %s, want once", p.ID, p.Context, c, p.File)
	}
	if c := strings.Count(p.Context, p.Literal); c != 1 || !strings.HasPrefix(p.Literal, `"`) || !strings.HasSuffix(p.Literal, `"`) || len(p.Literal) < 2 {
		return "", fmt.Errorf("%s: the literal %q is not one string literal of the edit site", p.ID, p.Literal)
	}
	lit := p.Literal[:len(p.Literal)-1] + p.token(n) + `\n"`
	if bad {
		lit = p.undefined(n)
	}
	return strings.Replace(orig, p.Context, strings.Replace(p.Context, p.Literal, lit, 1), 1), nil
}

// realMk is one mk binary of the table.
type realMk struct {
	Label   string `json:"label"`
	Path    string `json:"path"`
	SHA256  string `json:"sha256"`
	Version string `json:"version"` // mkIdentity: the commit it was built from, and its LLVM
}

// realProgramFact is what results.json records of a program.
type realProgramFact struct {
	ID     string   `json:"id"`
	Path   string   `json:"path"`
	Commit string   `json:"commit"` // the last commit that touched the package
	Dirty  bool     `json:"dirty"`  // the tree differs from that commit there
	Lines  int      `json:"lines"`  // lines of the package's .mk files
	Edit   string   `json:"edit"`   // the edit site, File: Literal
	Args   []string `json:"args"`   // the run that checks each build
}

// realCell is one program, mk, profile and kind.
type realCell struct {
	Program  string     `json:"program"`
	Mk       string     `json:"mk"` // a realMk label
	Profile  string     `json:"profile"`
	Kind     string     `json:"kind"` // "clean", "edit", "warm" or "check-body"
	Times    []float64  `json:"times"`
	Sum      summary    `json:"summary"`
	Builds   []buildCmd `json:"builds"`
	Excluded string     `json:"excluded,omitempty"`
}

type realResults struct {
	Run      facts             `json:"run"`
	Mks      []realMk          `json:"mks"`
	Programs []realProgramFact `json:"programs"`
	Jobs     int               `json:"jobs"`
	Runs     string            `json:"runs"`
	Cells    []realCell        `json:"cells"`
}

type realOptions struct {
	Root, Work                               string
	Mks                                      []realMk // Mks[0] is the mk measured; the others are compared with it
	Programs                                 []string
	Publish                                  bool
	CleanRuns, EditRuns, WarmRuns, CheckRuns int
	LockPath                                 string
	Mask                                     uint64
	Date                                     string
	Log                                      func(string)
}

// parseVs is -vs: label=path pairs, comma-separated.
func parseVs(s string) ([]realMk, error) {
	var out []realMk
	if s == "" {
		return nil, nil
	}
	for _, kv := range strings.Split(s, ",") {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || k == "" || v == "" {
			return nil, fmt.Errorf("-vs %q: want label=path", kv)
		}
		a, err := filepath.Abs(v)
		if err != nil {
			return nil, err
		}
		out = append(out, realMk{Label: k, Path: a})
	}
	return out, nil
}

// realSetup is one program, mk and profile's directories.
type realSetup struct {
	P       realProgram
	Mk      realMk
	Profile string
	Pkg     string // the package in the repository
	Src     string // the build copy: pkg.mk and src/
	State   string // the profile's MK_CACHE, deleted for a clean build
	Out     string
}

func newRealSetup(root, work string, p realProgram, m realMk, profile string) realSetup {
	base := filepath.Join(work, "compile-real", m.Label, p.ID, profile)
	return realSetup{P: p, Mk: m, Profile: profile, Pkg: filepath.Join(root, filepath.FromSlash(p.Path)),
		Src: filepath.Join(base, "src"), State: filepath.Join(base, "state"), Out: filepath.Join(base, "out"+exeSuffix())}
}

func exeSuffix() string {
	if os.PathSeparator == '\\' {
		return ".exe"
	}
	return ""
}

// copyPackage copies the package's pkg.mk and src/ into dst, without the
// gen/ directories that mk build writes views into (git-ignored, never
// read by the compiler) and without a PGO profile, and returns the
// lines of its .mk files. The repository's package is never edited.
func copyPackage(pkg, dst string) (int, error) {
	if err := os.RemoveAll(dst); err != nil {
		return 0, err
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return 0, err
	}
	lines := 0
	count := func(p string) error {
		if strings.HasSuffix(p, ".mk") {
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			lines += strings.Count(string(b), "\n")
		}
		return nil
	}
	if err := copyVendorFile(filepath.Join(pkg, "pkg.mk"), filepath.Join(dst, "pkg.mk")); err != nil {
		return 0, err
	}
	if err := count(filepath.Join(pkg, "pkg.mk")); err != nil {
		return 0, err
	}
	src := filepath.Join(pkg, "src")
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(pkg, p)
		if d.IsDir() {
			if p != src && d.Name() == "gen" {
				return filepath.SkipDir
			}
			return os.MkdirAll(filepath.Join(dst, rel), 0o755)
		}
		if strings.HasSuffix(p, ".mkprof") {
			return nil
		}
		if err := count(p); err != nil {
			return err
		}
		return copyVendorFile(p, filepath.Join(dst, rel))
	})
	return lines, err
}

// write puts edit n (bad: the check row's unknown name) into the copy.
func (s realSetup) write(n int, bad bool) error {
	orig, err := os.ReadFile(filepath.Join(s.Pkg, filepath.FromSlash(s.P.File)))
	if err != nil {
		return err
	}
	text, err := s.P.editText(string(orig), n, bad)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(s.Src, filepath.FromSlash(s.P.File)), []byte(text), 0o644)
}

func (s realSetup) env(mkStd string) []string {
	return []string{"MK_CACHE=" + filepath.Join(s.State, "mkcache"), "MK_STD=" + mkStd}
}

// buildCmd is the program's build, compile.go's Mk line: mk build -j 8 at
// x86-64-v2, release with PGO off.
func (s realSetup) buildCmd(mkStd string) buildCmd {
	args := []string{s.Mk.Path, "build", "-j", strconv.Itoa(compileJobs), "--cpu", cpus["x86-64-v2"].Mk}
	if s.Profile == "release" {
		args = append(args, "--release", "--pgo=off")
	}
	return buildCmd{Dir: s.Src, Env: s.env(mkStd), Args: append(args, "-o", s.Out, s.Src)}
}

func (s realSetup) checkCmd(mkStd string) buildCmd {
	return buildCmd{Dir: s.Src, Env: s.env(mkStd), Args: []string{s.Mk.Path, "check", "-j", strconv.Itoa(compileJobs), s.Src}}
}

// hasToken reports whether out holds tok not followed by a digit, so
// mkbench-edit-1 is not found in mkbench-edit-12.
func hasToken(out, tok string) bool {
	for i := 0; ; {
		j := strings.Index(out[i:], tok)
		if j < 0 {
			return false
		}
		end := i + j + len(tok)
		if end == len(out) || out[end] < '0' || out[end] > '9' {
			return true
		}
		i = end
	}
}

// verifyRun runs the built program and checks that it prints edit n's
// token: a stale build prints an older one.
func (s realSetup) verifyRun(n int) error {
	out, _, err := runOnce(s.Out, s.P.Args)
	if err != nil {
		return err
	}
	if !hasToken(out, s.P.token(n)) {
		return fmt.Errorf("after edit %d, %s %s did not print %s (a stale build): %s", n, s.P.ID, strings.Join(s.P.Args, " "), s.P.token(n), firstLines(out, 4))
	}
	return nil
}

// measureReal runs one program, mk and profile's cells: clean builds,
// then edits (two untimed), then no-change builds (one untimed), then, in
// dev, the check row. A failed kind excludes its own cell and every later
// one, since they build on its state.
func measureReal(s realSetup, o realOptions, mkStd string) []realCell {
	mk := func(kind string) realCell {
		return realCell{Program: s.P.ID, Mk: s.Mk.Label, Profile: s.Profile, Kind: kind}
	}
	kinds := []string{"clean", "edit"}
	if o.WarmRuns > 0 {
		kinds = append(kinds, "warm")
	}
	if o.CheckRuns > 0 && s.Profile == "dev" {
		kinds = append(kinds, "check-body")
	}
	var cells []realCell
	fail := func(from int, err error) []realCell {
		msg := firstLines(err.Error(), 8)
		for _, k := range kinds[from:] {
			c := mk(k)
			c.Excluded = msg
			cells = append(cells, c)
		}
		return cells
	}
	build := func(c *realCell, n int, timed bool) error {
		cmd := s.buildCmd(mkStd)
		d, err := runSteps([][]buildCmd{{cmd}})
		if err != nil {
			return err
		}
		if err := s.verifyRun(n); err != nil {
			return err
		}
		if timed {
			if c.Builds == nil {
				c.Builds = []buildCmd{cmd}
			}
			c.Times = append(c.Times, d.Seconds())
		}
		return nil
	}
	clean := mk("clean")
	for r := 0; r < o.CleanRuns; r++ {
		if err := os.RemoveAll(s.State); err != nil {
			return fail(0, err)
		}
		os.Remove(s.Out)
		if _, err := copyPackage(s.Pkg, s.Src); err != nil {
			return fail(0, err)
		}
		if err := s.write(0, false); err != nil {
			return fail(0, err)
		}
		if err := build(&clean, 0, true); err != nil {
			return fail(0, fmt.Errorf("clean build: %w", err))
		}
	}
	clean.Sum = summarize(clean.Times)
	cells = append(cells, clean)
	edit := mk("edit")
	last := o.EditRuns + 2
	for n := 1; n <= last; n++ {
		if err := s.write(n, false); err != nil {
			return fail(1, err)
		}
		if err := build(&edit, n, n > 2); err != nil {
			return fail(1, fmt.Errorf("edit build %d: %w", n, err))
		}
	}
	edit.Sum = summarize(edit.Times)
	cells = append(cells, edit)
	next := 2
	if o.WarmRuns > 0 {
		warm := mk("warm")
		for r := 0; r <= o.WarmRuns; r++ {
			if err := build(&warm, last, r > 0); err != nil {
				return fail(next, fmt.Errorf("no-change build: %w", err))
			}
		}
		warm.Sum = summarize(warm.Times)
		cells = append(cells, warm)
		next++
	}
	if o.CheckRuns > 0 && s.Profile == "dev" {
		chk := mk("check-body")
		cmd := s.checkCmd(mkStd)
		chk.Builds = []buildCmd{cmd}
		for k := 1; k <= o.CheckRuns+1; k++ {
			n := last + k
			for _, bad := range []bool{false, true} {
				if err := s.write(n, bad); err != nil {
					return fail(next, err)
				}
				d, code, out, err := runCheck(cmd)
				if err == nil {
					err = checkVerdict(code, out, bad, s.P.undefined(n))
				}
				if err != nil {
					return fail(next, fmt.Errorf("check, edit %d: %w", n, err))
				}
				if bad && k > 1 {
					chk.Times = append(chk.Times, d.Seconds())
				}
			}
		}
		chk.Sum = summarize(chk.Times)
		cells = append(cells, chk)
		if err := s.write(last+o.CheckRuns+1, false); err != nil {
			return cells
		}
	}
	return cells
}

// compileReal is `tools/bench compile -real`.
func compileReal(o realOptions, w func(string)) error {
	if len(o.Mks) == 0 {
		return errors.New("compile -real: no mk")
	}
	seen := map[string]bool{}
	for _, m := range o.Mks {
		if seen[m.Label] {
			return fmt.Errorf("compile -real: two mk binaries labelled %q", m.Label)
		}
		seen[m.Label] = true
		if strings.ContainsAny(m.Label, `/\: `) {
			return fmt.Errorf("compile -real: the label %q names a directory: use letters, digits, dots and hyphens", m.Label)
		}
	}
	var progs []realProgram
	for _, id := range o.Programs {
		p, ok := findRealProgram(id)
		if !ok {
			var ids []string
			for _, q := range realPrograms {
				ids = append(ids, q.ID)
			}
			return fmt.Errorf("compile -real: no program %q (%s)", id, strings.Join(ids, ", "))
		}
		progs = append(progs, p)
	}
	if o.Publish {
		lock, err := acquireLock(o.LockPath, o.Log)
		if err != nil {
			return err
		}
		defer lock.Release()
		o.Log("holding the machine lock " + lock.Path())
	}
	t, err := findToolchain(o.Work, o.Mks[0].Path)
	if err != nil {
		return err
	}
	if len(o.Mks) > 1 && t.MkStd != "" {
		// One MK_STD for every mk would build each older compiler with a
		// std that is not its own.
		return fmt.Errorf("compile -real: -mk-std (MK_STD %s) with -vs would build every mk with that std, not its own: leave it unset to use each mk's embedded std", t.MkStd)
	}
	res := realResults{Jobs: compileJobs,
		Runs: fmt.Sprintf("%d clean builds, %d one-body-edit builds (after 2 untimed) and %d no-change builds (after 1 untimed) per cell; %d `mk check` runs after a body edit (after 1 untimed pair), dev only", o.CleanRuns, o.EditRuns, o.WarmRuns, o.CheckRuns)}
	for i := range o.Mks {
		m := &o.Mks[i]
		m.SHA256 = fileSHA(m.Path)
		m.Version = mkIdentity(m.Path, t.version("mk", m.Path))
	}
	res.Mks = o.Mks
	for _, p := range progs {
		pf := packageFact(o.Root, &pkgSpec{Path: p.Path})
		lines, err := copyPackage(filepath.Join(o.Root, filepath.FromSlash(p.Path)), filepath.Join(o.Work, "compile-real", "lines", p.ID))
		if err != nil {
			return err
		}
		res.Programs = append(res.Programs, realProgramFact{ID: p.ID, Path: p.Path, Commit: pf.Commit, Dirty: pf.Dirty,
			Lines: lines, Edit: p.File + ": " + p.Literal, Args: p.Args})
	}
	if o.Publish {
		res.Run = gatherFacts(o.Root, t)
		res.Run.Date = time.Now().Format("2006-01-02 15:04")
		res.Run.MkBinary, res.Run.MkSHA256, res.Run.MkStd = o.Mks[0].Path, o.Mks[0].SHA256, t.MkStd
		if err := setAffinity(o.Mask); err != nil {
			return err
		}
		res.Run.Affinity = fmt.Sprintf("%#x (one logical CPU on each of 8 cores, set on tools/bench and inherited by every compiler)", o.Mask)
	}
	tag := "PROVISIONAL (no machine lock)"
	if o.Publish {
		tag = "published"
	}
	failed := 0
	for _, p := range progs {
		for _, profile := range []string{"dev", "release"} {
			for i, m := range o.Mks {
				s := newRealSetup(o.Root, o.Work, p, m, profile)
				for _, c := range measureReal(s, o, t.MkStd) {
					if c.Excluded != "" {
						// An older mk that cannot build today's program is
						// a fact of the table; the measured one failing is
						// a failed run.
						if i == 0 {
							failed++
						}
						w(fmt.Sprintf("FAIL compile-real %s %s %s %s: %s", p.ID, m.Label, profile, c.Kind, c.Excluded))
					} else {
						w(fmt.Sprintf("ok compile-real %s %s %s %s: median %s (min %s), %s", p.ID, m.Label, profile, c.Kind, fmtSec(c.Sum.Median), fmtSec(c.Sum.Min), tag))
					}
					res.Cells = append(res.Cells, c)
				}
			}
		}
	}
	if o.Publish {
		dir := filepath.Join(o.Root, "tests", "bench", "results", o.Date, "compile-real")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		b, err := json.MarshalIndent(res, "", "  ")
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, "results.json"), append(b, '\n'), 0o644); err != nil {
			return err
		}
		if _, err := os.Stat(filepath.Join(o.Root, "tests", "bench", "results", o.Date, "results.json")); err == nil {
			if err := writeResultsMD(o.Root, o.Date); err != nil {
				return err
			}
		} else {
			o.Log("results/" + o.Date + "/results.json is not there yet, so RESULTS.md is rendered by the main publish of that date")
		}
	}
	if failed > 0 {
		return fmt.Errorf("%d compile-real cells of the measured mk failed", failed)
	}
	return nil
}

func loadRealIfAny(root, date string) (*realResults, error) {
	b, err := os.ReadFile(filepath.Join(root, "tests", "bench", "results", date, "compile-real", "results.json"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var r realResults
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, fmt.Errorf("compile-real results of %s: %w", date, err)
	}
	return &r, nil
}

var realKindNames = map[string]string{
	"clean":      "clean (fresh MK_CACHE)",
	"edit":       "one-body edit",
	"warm":       "no change",
	"check-body": "`mk check`, body edit to diagnostic",
}

// renderReal is the real-program compile section of RESULTS.md.
func renderReal(res *realResults, date string) string {
	var w strings.Builder
	head := ""
	if len(res.Mks) > 0 {
		head = res.Mks[0].Label
	}
	fmt.Fprintf(&w, "## Table \"compile-real\"\n\nFrom `tests/bench/results/%s/compile-real/results.json`. Compile speed of real Mk programs: dogfoods of this repository, copied unchanged into the work directory, built by `mk build -j 8 --cpu x86-64-v2` (release: `--release --pgo=off`). Clean is a fresh MK_CACHE, so std is checked and compiled again; edit extends one string literal in a function body with a token that is new at every build; no change rebuilds with nothing changed; the check row is `mk check -j 8` in the dev cache after a valid body edit, timed on the next edit, which writes an unknown name (one diagnostic). Every build is checked by running the program and finding the token, and every check by its verdict, so a stale cache fails the cell instead of being timed. Mk only: the columns are mk binaries, each in its own caches, interleaved per program and profile, and each ratio is to `%s`, the mk measured. %d jobs, %s. Compile speed, never mixed with run time, and no bar (the rank bar is the generated table's).\n\n",
		date, head, res.Jobs, res.Runs)
	busy := "none"
	if len(res.Run.Busy) > 0 {
		busy = strings.Join(res.Run.Busy, ", ")
	}
	fmt.Fprintf(&w, "Machine: %s; %s; power scheme %s; affinity %s; measured %s. Other compilers and test binaries running at the start: %s.\n\n",
		orDash(res.Run.CPU), orDash(res.Run.OS), orDash(res.Run.Power), orDash(res.Run.Affinity), orDash(res.Run.Date), busy)
	w.WriteString("| mk | Binary | Built from | sha256 |\n|---|---|---|---|\n")
	for _, m := range res.Mks {
		fmt.Fprintf(&w, "| %s | `%s` | %s | `%s` |\n", m.Label, m.Path, cellText(m.Version), short(m.SHA256))
	}
	w.WriteString("\n| Program | Package | Last commit | Tree differs | Lines | Edit site | Check run |\n|---|---|---|---|---|---|---|\n")
	for _, p := range res.Programs {
		fmt.Fprintf(&w, "| %s | `%s` | `%s` | %v | %d | `%s` | `%s` |\n", p.ID, p.Path, short(p.Commit), p.Dirty, p.Lines,
			strings.ReplaceAll(p.Edit, "|", `\|`), strings.Join(p.Args, " "))
	}
	w.WriteString("\n")
	for _, p := range res.Programs {
		fmt.Fprintf(&w, "**%s.**\n\n| Profile | Build |", p.ID)
		for i, m := range res.Mks {
			fmt.Fprintf(&w, " %s median (min, MAD) |", m.Label)
			if i > 0 {
				fmt.Fprintf(&w, " %s / %s |", m.Label, head)
			}
		}
		w.WriteString("\n|---|---|")
		for i := range res.Mks {
			w.WriteString("---|")
			if i > 0 {
				w.WriteString("---|")
			}
		}
		w.WriteString("\n")
		for _, profile := range []string{"dev", "release"} {
			for _, kind := range []string{"clean", "edit", "warm", "check-body"} {
				cell := func(label string) *realCell {
					for i := range res.Cells {
						c := &res.Cells[i]
						if c.Program == p.ID && c.Mk == label && c.Profile == profile && c.Kind == kind {
							return c
						}
					}
					return nil
				}
				if !slices.ContainsFunc(res.Mks, func(m realMk) bool { return cell(m.Label) != nil }) {
					continue
				}
				fmt.Fprintf(&w, "| %s | %s |", profile, realKindNames[kind])
				h := cell(head)
				for i, m := range res.Mks {
					c := cell(m.Label)
					text, ratio := "not run", ""
					switch {
					case c == nil:
					case c.Excluded != "":
						text = "excluded: " + cellText(c.Excluded)
					default:
						text = fmt.Sprintf("%s (%s, %s)", fmtSec(c.Sum.Median), fmtSec(c.Sum.Min), fmtSec(c.Sum.MAD))
						if h != nil && h.Excluded == "" && h.Sum.Median > 0 {
							ratio = fmt.Sprintf("%.2f", c.Sum.Median/h.Sum.Median)
						}
					}
					fmt.Fprintf(&w, " %s |", text)
					if i > 0 {
						fmt.Fprintf(&w, " %s |", ratio)
					}
				}
				w.WriteString("\n")
			}
		}
		w.WriteString("\n")
	}
	return w.String()
}
