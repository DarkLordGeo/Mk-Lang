// Why: whether mk builds with frame pointers by default depends on what
// they cost; this measures that price on the benchmark rows.
//
// What: the `tools/bench framepointers` subcommand: fpRows, buildFP, the
// mk-fpoff and mk-fpon columns, fpDefault's verdict and renderFP's report.
//
// How: `tools/bench framepointers` builds each selected row's Mk program twice
// with one mk executable, in release, with --frame-pointers=off
// (`mk-fpoff`) and --frame-pointers=on (`mk-fpon`); both flags are given
// explicitly, so mk's default and MK_FRAME_POINTERS cannot skew either
// side. The two programs must print the row's checksum. Then they run in
// alternating pairs (ABBA order, as compare runs them), and each row's
// price is the median of its paired ratios, on over off, with its
// bootstrap interval. The figure is the geometric mean of the rows'
// prices; the threshold (on by default when the geometric mean
// slowdown is at most 2%) is printed against it.
//
// -count instructions (Linux only) replaces the clock with
// `perf stat -e instructions:u` of each run: the better evidence on a
// busy machine. It fails when perf cannot count (`<not
// supported>` or `<not counted>`, common under WSL2), never falls back to
// time.
//
// The Linux leg: on Windows, -target x86_64-linux
// cross-builds both programs of every row into <work>/bin/fp-linux and
// runs nothing; inside WSL, a Linux build of tools/bench runs
// `framepointers -prebuilt <that directory> -count instructions`, which
// copies them instead of building, as publish-linux does.
//
// This is a development measurement: no machine lock, nothing written to
// tests/bench/results, every figure provisional.
//
//	tools/bench framepointers [-mk M] [-pairs 15] [-quick] [-table same] [-count time|instructions] [rows]
//	tools/bench framepointers -target x86_64-linux [-mk M] [rows]          cross-build only
//	tools/bench framepointers -prebuilt DIR -count instructions [rows]     inside WSL

package main

import (
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
)

// The two columns of the measurement. Like mk-pgo they are not in columns, so
// no other table or subcommand builds them; only buildFP does.
var (
	fpOffColumn = column{ID: "mk-fpoff", Lang: "mk", Bounds: "checked", Overflow: "trap", Panic: "abort (exit 101)", LTO: "ThinLTO", Linker: "lld-link", CRT: "none (Mk runtime, kernel32 only)", StdV2: "yes (built with the program)"}
	fpOnColumn  = column{ID: "mk-fpon", Lang: "mk", Bounds: "checked", Overflow: "trap", Panic: "abort (exit 101)", LTO: "ThinLTO", Linker: "lld-link", CRT: "none (Mk runtime, kernel32 only)", StdV2: "yes (built with the program)"}
)

// fpFlags is the mk build flag of each frame-pointer column.
var fpFlags = map[string]string{"mk-fpoff": "--frame-pointers=off", "mk-fpon": "--frame-pointers=on"}

// fpThreshold is the default's threshold:
// on by default when the geometric mean of on/off is at most 1.02.
const fpThreshold = 1.02

type fpOptions struct {
	Root, Work string
	Mk         string
	Rows       []string
	Table      string // with no rows: every row of this table with an mk source
	Pairs      int
	Quick      bool
	Count      string // "time" or "instructions"
	Target     string // cross-build only, for this target
	Prebuilt   string // copy the programs from here instead of building
	Log        func(string)
}

// fpCell is one row's paired measurements.
type fpCell struct {
	Row      string
	Off, On  []float64 // seconds, or instructions
	Ratios   []float64 // on / off, pair by pair
	Excluded string
}

// fpRows is the rows the measurement takes: those named, else every row of
// the table with an mk source under tests/bench itself, not
// tests/bench-lanes (the suite is tests/bench).
func fpRows(all []*row, table string, names []string) ([]*row, error) {
	if len(names) > 0 {
		rows, err := selectRows(all, "", names)
		if err != nil {
			return nil, err
		}
		for _, r := range rows {
			if r.Sources["mk"] == "" {
				return nil, fmt.Errorf("row %s has no mk source", r.id())
			}
		}
		return rows, nil
	}
	var out []*row
	for _, r := range all {
		if r.Broken == "" && r.Lane == "" && r.Table == table && r.Sources["mk"] != "" {
			out = append(out, r)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no row of table %q has an mk source", table)
	}
	return out, nil
}

// buildFP builds a row's two programs, frame pointers off and on.
func buildFP(t *toolchain, tag string, r *row, log func(string)) (off, on *built) {
	one := func(c column) *built {
		b := &built{Row: r, Col: c, Exe: exeName(t.Work, tag, r, c)}
		if b.Cmd, b.Err = t.command(r, c, b.Exe); b.Err == nil {
			b.Output, b.Err = buildOne(b, log)
		}
		if b.Err != nil {
			if strings.Contains(b.Err.Error(), "frame-pointers") {
				b.Err = fmt.Errorf("this mk does not take %s: %w", fpFlags[c.ID], b.Err)
			}
			log(fmt.Sprintf("build %s %s: %v", r.id(), c.ID, b.Err))
		}
		return b
	}
	return one(fpOffColumn), one(fpOnColumn)
}

// fpMeasure is one run's measure: wall seconds, or user-space instructions.
func fpMeasure(count string, b *built, args []string) (float64, error) {
	run, err := timedRun(b, args)
	if err != nil {
		return 0, err
	}
	if count == "instructions" {
		return perfInstructions(b.Exe, run.Args, run.Env)
	}
	_, d, err := runOnceEnv(b.Exe, run.Args, run.Env)
	return d.Seconds(), err
}

// perfInstructions runs exe under `perf stat -x, -e instructions:u` and
// returns the count.
func perfInstructions(exe string, args, env []string) (float64, error) {
	f, err := os.CreateTemp("", "fp-perf-*.csv")
	if err != nil {
		return 0, err
	}
	name := f.Name()
	f.Close()
	defer os.Remove(name)
	cmd := exec.Command("perf", append([]string{"stat", "-x,", "-e", "instructions:u", "-o", name, "--", exe}, args...)...)
	cmd.Env = env
	if out, err := cmd.CombinedOutput(); err != nil {
		return 0, fmt.Errorf("perf stat %s: %v: %s", filepath.Base(exe), err, firstLines(string(out), 6))
	}
	b, err := os.ReadFile(name)
	if err != nil {
		return 0, err
	}
	return parsePerfInstructions(string(b))
}

// parsePerfInstructions reads perf stat's CSV output (-x,): the line whose
// third field is the instructions event. A count perf could not take is an
// error, never a zero.
func parsePerfInstructions(text string) (float64, error) {
	for _, l := range strings.Split(text, "\n") {
		fs := strings.Split(strings.TrimSpace(l), ",")
		if len(fs) < 3 || !strings.HasPrefix(fs[2], "instructions") {
			continue
		}
		if strings.HasPrefix(fs[0], "<") {
			return 0, fmt.Errorf("perf could not count instructions (%s): no figure, and no fallback to time", fs[0])
		}
		v, err := strconv.ParseFloat(fs[0], 64)
		if err != nil || v <= 0 {
			return 0, fmt.Errorf("perf's instruction count %q", fs[0])
		}
		return v, nil
	}
	return 0, fmt.Errorf("perf stat printed no instructions line: %s", firstLines(text, 4))
}

// framePointers is `tools/bench framepointers`.
func framePointers(o fpOptions) ([]fpCell, error) {
	if o.Count != "time" && o.Count != "instructions" {
		return nil, fmt.Errorf("-count %q: time or instructions", o.Count)
	}
	if o.Count == "instructions" && runtime.GOOS != "linux" && o.Target == "" {
		return nil, errors.New("-count instructions needs Linux perf (the evidence10 linux.sh runs it)")
	}
	if o.Pairs < 1 {
		return nil, errors.New("-pairs must be at least 1")
	}
	all, err := discover(o.Root)
	if err != nil {
		return nil, err
	}
	logBroken(all, o.Log)
	rows, err := fpRows(all, o.Table, o.Rows)
	if err == nil {
		err = prepareRows(o.Root, rows)
	}
	if err != nil {
		return nil, err
	}
	t, err := findToolchain(o.Work, o.Mk)
	if err != nil {
		return nil, err
	}
	tag := "fp"
	if o.Target != "" {
		// Cross-build only: the programs land in <work>/bin/fp-linux,
		// the -prebuilt directory of the run inside WSL.
		t.MkTarget = o.Target
		failed := 0
		for _, r := range rows {
			off, on := buildFP(t, "fp-linux", r, o.Log)
			for _, b := range []*built{off, on} {
				if b.Err != nil {
					failed++
				} else {
					o.Log("built " + b.Exe)
				}
			}
		}
		if failed > 0 {
			return nil, fmt.Errorf("%d cross-builds failed", failed)
		}
		return nil, nil
	}
	if o.Prebuilt != "" {
		t.MkPrebuilt = o.Prebuilt
		tag = "fp-prebuilt"
	}
	var out []fpCell
	for _, r := range rows {
		cc := fpCell{Row: r.id()}
		off, on := buildFP(t, tag, r, o.Log)
		if off.Err != nil || on.Err != nil {
			cc.Excluded = fmt.Sprintf("build failed: off %v, on %v", off.Err, on.Err)
			out = append(out, cc)
			if strings.Contains(cc.Excluded, "does not take") {
				return out, errors.New(cc.Excluded)
			}
			continue
		}
		args, want := r.runArgs, r.Checksum
		if o.Quick {
			args, want = r.runQuick, r.QuickSum
		}
		checks, agreed, ok := verify([]*built{off, on}, args, want)
		if !ok {
			cc.Excluded = fmt.Sprintf("checksums off %q on %q, expected %q (%v %v)", checks[0].Sum, checks[1].Sum, agreed, checks[0].Err, checks[1].Err)
			out = append(out, cc)
			continue
		}
		if err := fpPairs(&cc, o.Pairs, off, on, func(x *built) (float64, error) { return fpMeasure(o.Count, x, args) }); err != nil {
			return out, fmt.Errorf("%s: %w", r.id(), err)
		}
		o.Log(fmt.Sprintf("%s: on/off %.4f over %d pairs", cc.Row, median(cc.Ratios), len(cc.Ratios)))
		out = append(out, cc)
	}
	return out, nil
}

// fpPairs runs n pairs in ABBA order (off first in even pairs, on first in
// odd ones, so neither side always runs first) and records each pair's two
// measures and its on/off ratio in c.
func fpPairs(c *fpCell, n int, off, on *built, measure func(*built) (float64, error)) error {
	for p := range n {
		order := []*built{off, on}
		if p%2 == 1 {
			order = []*built{on, off}
		}
		var v [2]float64
		for k, x := range order {
			m, err := measure(x)
			if err != nil {
				return fmt.Errorf("%s: %w", x.Col.ID, err)
			}
			v[k] = m
		}
		if p%2 == 1 {
			v[0], v[1] = v[1], v[0]
		}
		c.Off, c.On = append(c.Off, v[0]), append(c.On, v[1])
		c.Ratios = append(c.Ratios, v[1]/v[0])
	}
	return nil
}

// geoMean is the geometric mean of xs (NaN for none or a non-positive x).
func geoMean(xs []float64) float64 {
	if len(xs) == 0 {
		return math.NaN()
	}
	var s float64
	for _, x := range xs {
		if !(x > 0) {
			return math.NaN()
		}
		s += math.Log(x)
	}
	return math.Exp(s / float64(len(xs)))
}

// fpSummary is the rows' prices (the median paired ratio of each measured
// row) and their geometric mean.
func fpSummary(cs []fpCell) ([]float64, float64) {
	var prices []float64
	for _, c := range cs {
		if c.Excluded == "" && len(c.Ratios) > 0 {
			prices = append(prices, median(c.Ratios))
		}
	}
	return prices, geoMean(prices)
}

// fpDefault is the default rule (on at a slowdown of at most 2%) applied to a
// geometric mean. With any row excluded there is no verdict: the mean is not
// over the suite.
func fpDefault(g float64, excluded int) string {
	switch {
	case excluded > 0:
		return fmt.Sprintf("no verdict, since %d rows are excluded and the mean is not over the suite", excluded)
	case math.IsNaN(g):
		return "no figure"
	case g <= fpThreshold:
		return "on (the geometric mean slowdown is at most 2%)"
	}
	return "off, with the switch documented (the geometric mean slowdown is over 2%)"
}

// renderFP is the provisional frame-pointer report.
func renderFP(w io.Writer, cs []fpCell, mk, mkLabel, count, by string) {
	unit := "wall time"
	if count == "instructions" {
		unit = "user-space instructions (perf stat -e instructions:u)"
	}
	fmt.Fprintf(w, "E1, frame pointers: PROVISIONAL (measured by %s; %s; no machine lock, shared machine; never published)\n", orDash(by), unit)
	if mk == "" {
		mk = orDash(mkLabel) + " (the mk that cross-built the -prebuilt programs)"
	} else {
		mk += " (" + short(fileSHA(mk)) + ")"
	}
	fmt.Fprintf(w, "mk %s\nbuilds: mk build --release --pgo=off --no-cache, with --frame-pointers=off and --frame-pointers=on\n\n", mk)
	fmt.Fprintf(w, "| row | off median | on median | on/off (median of pairs) | 95%% interval | pairs |\n|---|---|---|---|---|---|\n")
	f := func(x []float64) string {
		if count == "instructions" {
			return fmt.Sprintf("%.0f", median(x))
		}
		return fmtSec(median(x))
	}
	for _, c := range cs {
		if c.Excluded != "" {
			fmt.Fprintf(w, "| %s | excluded: %s | | | | |\n", c.Row, cellText(c.Excluded))
			continue
		}
		ci := pairedCI(c.Ratios)
		fmt.Fprintf(w, "| %s | %s | %s | %.4f | [%.4f, %.4f] | %d |\n", c.Row, f(c.Off), f(c.On), median(c.Ratios), ci.Lo, ci.Hi, len(c.Ratios))
	}
	prices, g := fpSummary(cs)
	excluded := len(cs) - len(prices)
	fmt.Fprintf(w, "\nGeometric mean of on/off over %d rows (%d excluded): %.4f (%+.2f%%). Default frame pointers by the 2%% threshold: %s.\n",
		len(prices), excluded, g, (g-1)*100, fpDefault(g, excluded))
	if excluded > 0 {
		fmt.Fprintf(w, "Rows excluded: %s.\n", strings.Join(slices.Collect(func(yield func(string) bool) {
			for _, c := range cs {
				if c.Excluded != "" && !yield(c.Row) {
					return
				}
			}
		}), ", "))
	}
}
