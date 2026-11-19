// Why: process start-up, the runtime and std blur what a whole-program table
// says about Mk's code generation; in-process kernels linked against the C
// baseline compiled by the same clang 21 isolate it (tests/bench/README.md,
// "The codegen table").
//
// What: loadKernels, codegenCCommand, codegenMkCommand, parseRounds,
// checkRounds, mergeCodegen and renderCodegen.
//
// How: a kernel is tests/bench/codegen/<kernel>/: a Mk package whose Main is
// the in-process harness, its C side as one freestanding C file, and
// codegen.json. tools/bench compiles the C file by clang 21 into a native
// object, links it into the Mk program through the --llvm shim of shim.go,
// runs the program, and reads one line per round:
//
//	round <r> mk <ns> <checksum> c <ns> <checksum>
//
// The program alternates the order (even rounds Mk first, odd rounds C
// first); each side fills its own 64-byte-aligned buffer untimed and times
// only its Run. The table is Mk against C of the same LLVM and never feeds a
// cross-language ratio.

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"
)

// kernel is one codegen.json.
type kernel struct {
	Name      string   `json:"name"`
	Algorithm string   `json:"algorithm"`
	Measures  string   `json:"measures"` // what the ratio prices (a check, a loop shape)
	C         string   `json:"c"`        // the C file, relative to the kernel directory
	Args      []string `json:"args"`     // publish size, before the round count
	Quick     []string `json:"quick"`
	Checksum  string   `json:"checksum"`
	QuickSum  string   `json:"quick_checksum"`
	Rounds    int      `json:"rounds"` // per process, at least 21
	Notes     []string `json:"notes,omitempty"`

	Dir string `json:"-"`
}

// codegenRounds is the minimum number of rounds per process.
const codegenRounds = 21

// cAllowedUndefined are the symbols a C kernel may leave undefined: the ones
// the Mk runtime defines (internal/codegen support.go).
var cAllowedUndefined = []string{"memcpy", "memmove", "memset", "memcmp", "bcmp", "__chkstk", "_fltused"}

func loadKernels(root string, names []string) ([]*kernel, error) {
	matches, _ := filepath.Glob(filepath.Join(root, "tests", "bench", "codegen", "*", "codegen.json"))
	var ks []*kernel
	for _, m := range matches {
		b, err := os.ReadFile(m)
		if err != nil {
			return nil, err
		}
		var k kernel
		dec := json.NewDecoder(strings.NewReader(string(b)))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&k); err != nil {
			return nil, fmt.Errorf("%s: %w", m, err)
		}
		k.Dir = filepath.Dir(m)
		if k.Name == "" {
			k.Name = filepath.Base(k.Dir)
		}
		if k.Rounds < codegenRounds {
			return nil, fmt.Errorf("%s: rounds %d is under the minimum of %d rounds", m, k.Rounds, codegenRounds)
		}
		if _, err := os.Stat(filepath.Join(k.Dir, k.C)); err != nil {
			return nil, fmt.Errorf("%s: C side: %w", m, err)
		}
		ks = append(ks, &k)
	}
	slices.SortFunc(ks, func(a, b *kernel) int { return strings.Compare(a.Name, b.Name) })
	if len(names) == 0 {
		return ks, nil
	}
	var out []*kernel
	for _, n := range names {
		i := slices.IndexFunc(ks, func(k *kernel) bool { return k.Name == n })
		if i < 0 {
			return nil, fmt.Errorf("no codegen kernel %q", n)
		}
		out = append(out, ks[i])
	}
	return out, nil
}

// cTriple is the triple mk passes clang for x86_64 Windows, so both objects
// are compiled for the same target.
const cTriple = "x86_64-pc-windows-msvc19.33.0"

// codegenCCommand compiles a kernel's C side to a native object (no LTO
// for it). Freestanding, so it needs no headers but clang's own.
func codegenCCommand(clang, src, obj string) []string {
	return []string{clang, "--target=" + cTriple, "-std=c17", "-O3", "-march=x86-64-v2",
		"-ffp-contract=off", "-fno-math-errno", "-ffreestanding", "-c", "-o", obj, src}
}

// codegenMkCommand builds the kernel's Mk package with the shim directory as
// its LLVM. The flags are the same-algorithm table's.
func codegenMkCommand(mk, shim, out, pkg string) []string {
	return []string{mk, "build", "--release", "--pgo=off", "--cpu", "x86-64-v2", "--no-cache",
		"--llvm", shim, "-o", out, pkg}
}

// undefinedSymbols lists what obj leaves undefined (llvm-nm), less the
// symbols the Mk runtime defines.
func undefinedSymbols(nm, obj string) ([]string, error) {
	b, err := exec.Command(nm, "--undefined-only", "--format=just-symbols", obj).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("llvm-nm: %v\n%s", err, firstLines(string(b), 5))
	}
	var bad []string
	for _, s := range strings.Fields(string(b)) {
		if !slices.Contains(cAllowedUndefined, s) {
			bad = append(bad, s)
		}
	}
	return bad, nil
}

type codegenBuild struct {
	K        *kernel
	Exe      string
	Commands [][]string
	Err      error
}

// buildKernel builds one kernel into <work>/codegen/<kernel>.
func buildKernel(t *toolchain, k *kernel, log func(string)) codegenBuild {
	start := time.Now()
	exe, cmds, err := buildWithC(t, filepath.Join(t.Work, "codegen", k.Name), k.Name, k.Dir, filepath.Join(k.Dir, k.C))
	if err == nil {
		log(fmt.Sprintf("built codegen %s in %s", k.Name, time.Since(start).Round(100*time.Millisecond)))
	}
	return codegenBuild{K: k, Exe: exe, Commands: cmds, Err: err}
}

// buildWithC compiles cFile, writes the shim directory under dir and builds
// the Mk package pkg into dir/<name>.exe with the object linked, then checks
// the link line named the object. It returns the executable and every
// command it ran.
func buildWithC(t *toolchain, dir, name, pkg, cFile string) (string, [][]string, error) {
	var cmds [][]string
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", nil, err
	}
	if t.Clang == "" || t.Mk == "" {
		return "", nil, errors.New("clang or mk not found")
	}
	obj := filepath.Join(dir, name+"-c.obj")
	cc := codegenCCommand(t.Clang, cFile, obj)
	cmds = append(cmds, cc)
	if out, err := exec.Command(cc[0], cc[1:]...).CombinedOutput(); err != nil {
		return "", cmds, fmt.Errorf("clang: %v\n%s", err, firstLines(string(out), 10))
	}
	nm := filepath.Join(filepath.Dir(t.Clang), "llvm-nm"+filepath.Ext(t.Clang))
	bad, err := undefinedSymbols(nm, obj)
	if err != nil {
		return "", cmds, err
	}
	if len(bad) > 0 {
		return "", cmds, fmt.Errorf("the C side needs symbols the Mk runtime does not define: %s", strings.Join(bad, " "))
	}
	shim := filepath.Join(dir, "llvm")
	linkLog := filepath.Join(dir, "shim.log")
	os.Remove(linkLog)
	cfg := shimConfig{RealDir: filepath.Dir(t.Clang), Objects: []string{obj}, Log: linkLog}
	if err := writeShimDir(shim, cfg); err != nil {
		return "", cmds, err
	}
	exe := filepath.Join(dir, name+".exe")
	os.Remove(exe)
	mc := codegenMkCommand(t.Mk, shim, exe, pkg)
	cmds = append(cmds, mc)
	cmd := exec.Command(mc[0], mc[1:]...)
	cmd.Dir = pkg
	cmd.Env = append(os.Environ(), "MK_CACHE="+filepath.Join(t.Work, "mkcache"), "MK_STD="+t.MkStd)
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", cmds, fmt.Errorf("mk build: %v\n%s", err, firstLines(string(out), 12))
	}
	lines, _ := os.ReadFile(linkLog)
	if !linkedObject(string(lines), obj) {
		return "", cmds, fmt.Errorf("the link line in %s does not name %s", linkLog, obj)
	}
	return exe, cmds, nil
}

// linkedObject reports whether a shim log holds a linker command naming obj.
func linkedObject(shimLog, obj string) bool {
	for _, l := range strings.Split(shimLog, "\n") {
		f := strings.Fields(l)
		if len(f) == 0 {
			continue
		}
		if !strings.Contains(strings.ToLower(l), "lld") {
			continue
		}
		if strings.HasSuffix(strings.TrimSpace(l), obj) || strings.Contains(l, " "+obj+" ") {
			return true
		}
	}
	return false
}

// kernelRound is one round line.
type kernelRound struct {
	MkNs, CNs   int64
	MkSum, CSum string
}

// parseRounds reads the round lines of one process.
func parseRounds(out string) ([]kernelRound, error) {
	var rs []kernelRound
	for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
		f := strings.Fields(l)
		if len(f) != 8 || f[0] != "round" || f[2] != "mk" || f[5] != "c" {
			return nil, fmt.Errorf("not a round line: %q", l)
		}
		mk, err1 := strconv.ParseInt(f[3], 10, 64)
		c, err2 := strconv.ParseInt(f[6], 10, 64)
		if err := errors.Join(err1, err2); err != nil {
			return nil, fmt.Errorf("round line %q: %w", l, err)
		}
		rs = append(rs, kernelRound{MkNs: mk, CNs: c, MkSum: f[4], CSum: f[7]})
	}
	if len(rs) == 0 {
		return nil, errors.New("no round lines")
	}
	return rs, nil
}

// checkRounds requires every checksum of both sides to equal want.
func checkRounds(rs []kernelRound, want string) error {
	for i, r := range rs {
		if r.MkSum != want || r.CSum != want {
			return fmt.Errorf("round %d: checksums mk %s c %s, want %s", i, r.MkSum, r.CSum, want)
		}
	}
	return nil
}

// codegenCell is one kernel of the published codegen table. Times are
// seconds per Run.
type codegenCell struct {
	Kernel   string     `json:"kernel"`
	Measures string     `json:"measures"`
	Args     []string   `json:"args"`
	Checksum string     `json:"checksum"`
	Mk       []float64  `json:"mk"`
	C        []float64  `json:"c"`
	MkSum    summary    `json:"mk_summary"`
	CSum     summary    `json:"c_summary"`
	Ratio    ratioEntry `json:"ratio"` // median(mk) / median(c) with its bootstrap interval
	Builds   [][]string `json:"builds"`
	Excluded string     `json:"excluded,omitempty"`
}

type codegenResults struct {
	Run    facts         `json:"run"`
	Procs  int           `json:"procs"`
	Rounds string        `json:"rounds"`
	Cells  []codegenCell `json:"cells"`
}

type codegenOptions struct {
	Root     string
	Work     string
	Mk       string
	Names    []string
	Publish  bool
	Quick    bool
	Procs    int
	LockPath string
	Mask     uint64
	Date     string
	Log      func(string)
}

// codegen builds and runs the kernels. With Publish it holds the machine
// lock, pins the affinity, runs the publish size Procs times and writes
// results/<date>/codegen/results.json and RESULTS.md; otherwise it checks the
// quick size once and prints provisional numbers.
func codegen(o codegenOptions, w func(string)) error {
	if o.Publish {
		if err := refuseExperiment(o.Mk, ""); err != nil {
			return err
		}
	}
	if runtime.GOOS != "windows" {
		return errors.New("the codegen table runs on Windows (the Linux table is the same-algorithm set)")
	}
	ks, err := loadKernels(o.Root, o.Names)
	if err != nil {
		return err
	}
	if len(ks) == 0 {
		return errors.New("no codegen kernels under tests/bench/codegen")
	}
	if o.Publish {
		lock, err := acquireLock(o.LockPath, o.Log)
		if err != nil {
			return err
		}
		defer lock.Release()
		o.Log("holding the machine lock " + lock.Path())
	}
	t, err := findToolchain(o.Work, o.Mk)
	if err != nil {
		return err
	}
	var bs []codegenBuild
	for _, k := range ks {
		bs = append(bs, buildKernel(t, k, o.Log))
	}
	res := codegenResults{Procs: o.Procs}
	if o.Publish {
		res.Run = gatherFacts(o.Root, t)
		res.Run.Date = time.Now().Format("2006-01-02 15:04") // when measured; o.Date names the directory
		res.Run.MkBinary, res.Run.MkSHA256 = o.Mk, fileSHA(o.Mk)
		if err := setAffinity(o.Mask); err != nil {
			return err
		}
		res.Run.Affinity = fmt.Sprintf("%#x (one logical CPU, set on tools/bench and inherited)", o.Mask)
	}
	failed := 0
	for _, b := range bs {
		k := b.K
		args, want, procs := k.Args, k.Checksum, o.Procs
		if !o.Publish {
			procs = 1
			if o.Quick {
				args, want = k.Quick, k.QuickSum
			}
		}
		cell := codegenCell{Kernel: k.Name, Measures: k.Measures, Args: args, Checksum: want, Builds: b.Commands}
		err := b.Err
		for p := 0; err == nil && p < procs; p++ {
			full := append(append([]string{}, args...), strconv.Itoa(k.Rounds))
			var out string
			out, _, err = runOnce(b.Exe, full)
			var rs []kernelRound
			if err == nil {
				rs, err = parseRounds(out)
			}
			if err == nil {
				err = checkRounds(rs, want)
			}
			for _, r := range rs {
				cell.Mk = append(cell.Mk, float64(r.MkNs)/1e9)
				cell.C = append(cell.C, float64(r.CNs)/1e9)
			}
		}
		if err != nil {
			failed++
			cell.Excluded = firstLines(err.Error(), 6)
			cell.Mk, cell.C = nil, nil
			w(fmt.Sprintf("FAIL %s: %s", k.Name, cell.Excluded))
			res.Cells = append(res.Cells, cell)
			continue
		}
		cell.MkSum, cell.CSum = summarize(cell.Mk), summarize(cell.C)
		cell.Ratio = ratioEntry{Ratio: cell.MkSum.Median / cell.CSum.Median, CI: ratioCI(cell.Mk, cell.C)}
		res.Cells = append(res.Cells, cell)
		tag := "PROVISIONAL (no machine lock)"
		if o.Publish {
			tag = "published"
		}
		w(fmt.Sprintf("ok %s args %q checksum %s: mk median %s min %s mad %s, c median %s min %s mad %s, mk / c %s [%s]",
			k.Name, args, want, fmtSec(cell.MkSum.Median), fmtSec(cell.MkSum.Min), fmtSec(cell.MkSum.MAD),
			fmtSec(cell.CSum.Median), fmtSec(cell.CSum.Min), fmtSec(cell.CSum.MAD), fmtRatio(cell.Ratio), tag))
	}
	if o.Publish {
		res.Rounds = fmt.Sprintf("%d processes of the kernel's rounds (at least %d each), pooled", o.Procs, codegenRounds)
		dir := filepath.Join(o.Root, "tests", "bench", "results", o.Date, "codegen")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		if prev, err := loadCodegen(filepath.Join(dir, "results.json")); err == nil {
			res.Cells = mergeCodegen(prev.Cells, res.Cells)
		}
		b, err := json.MarshalIndent(res, "", "  ")
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, "results.json"), append(b, '\n'), 0o644); err != nil {
			return err
		}
		if err := writeResultsMD(o.Root, o.Date); err != nil {
			return err
		}
	}
	if failed > 0 {
		return fmt.Errorf("%d of %d kernels failed", failed, len(bs))
	}
	return nil
}

func loadCodegen(path string) (*codegenResults, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var r codegenResults
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &r, nil
}

// mergeCodegen keeps the kernels of an earlier publish that this one did
// not run. The run facts are this publish's; a merged older cell keeps its
// own builds.
func mergeCodegen(prev, next []codegenCell) []codegenCell {
	out := append([]codegenCell{}, next...)
	for _, p := range prev {
		if !slices.ContainsFunc(next, func(c codegenCell) bool { return c.Kernel == p.Kernel }) {
			out = append(out, p)
		}
	}
	slices.SortFunc(out, func(a, b codegenCell) int { return strings.Compare(a.Kernel, b.Kernel) })
	return out
}

// renderCodegen is the codegen section of RESULTS.md.
func renderCodegen(res *codegenResults, date string) string {
	var w strings.Builder
	fmt.Fprintf(&w, "## Table \"codegen\"\n\nFrom `tests/bench/results/%s/codegen/results.json`. In-process kernels: Mk against C compiled by the same LLVM (%s), the C side a freestanding native object linked into the Mk program (README \"The codegen table\"). Each side fills its own 64-byte-aligned buffer untimed and times only its `Run`; the order alternates by round. Times are per `Run`: %s. This table never feeds a cross-language ratio.\n\n",
		date, orDash(res.Run.Versions["c"]), res.Rounds)
	busy := "none"
	if len(res.Run.Busy) > 0 {
		busy = strings.Join(res.Run.Busy, ", ")
	}
	fmt.Fprintf(&w, "Machine: %s; %s; power scheme %s; affinity %s. mk `%s` (sha256 `%s`), tree at `%s`; measured %s. Other compilers and test binaries running at the start: %s.\n\n",
		orDash(res.Run.CPU), orDash(res.Run.OS), orDash(res.Run.Power), orDash(res.Run.Affinity), orDash(binName(res.Run.MkBinary)), short(res.Run.MkSHA256), orDash(res.Run.Commit), orDash(res.Run.Date), busy)
	w.WriteString("| Kernel | Args | mk median (min, MAD) | c median (min, MAD) | mk / c [95%] | Against 1.10 | Prices |\n|---|---|---|---|---|---|---|\n")
	var ratios []float64
	for _, c := range res.Cells {
		if c.Excluded != "" {
			fmt.Fprintf(&w, "| %s | %s | excluded: %s | | | | %s |\n", c.Kernel, strings.Join(c.Args, " "), cellText(c.Excluded), cellText(c.Measures))
			continue
		}
		ratios = append(ratios, c.Ratio.Ratio)
		fmt.Fprintf(&w, "| %s | %s | %s (%s, %s) | %s (%s, %s) | %s | %s | %s |\n", c.Kernel, strings.Join(c.Args, " "),
			fmtSec(c.MkSum.Median), fmtSec(c.MkSum.Min), fmtSec(c.MkSum.MAD),
			fmtSec(c.CSum.Median), fmtSec(c.CSum.Min), fmtSec(c.CSum.MAD),
			fmtRatio(c.Ratio), classify(c.Ratio.CI, 1.10), cellText(c.Measures))
	}
	if len(ratios) > 0 {
		fmt.Fprintf(&w, "\nMedian of mk / c over these kernels: %.2f (%d kernels).\n", median(ratios), len(ratios))
	}
	w.WriteString("\n")
	return w.String()
}

// codegenPath is where a date's codegen results live.
func codegenPath(root, date string) string {
	return filepath.Join(root, "tests", "bench", "results", date, "codegen", "results.json")
}

// loadCodegenIfAny is loadCodegen with a missing file as nil, nil.
func loadCodegenIfAny(root, date string) (*codegenResults, error) {
	r, err := loadCodegen(codegenPath(root, date))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	return r, err
}
