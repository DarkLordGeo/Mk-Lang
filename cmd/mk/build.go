// Why: mk build and mk run share one flag set and one path into the driver;
// keeping them together keeps the two commands' flags identical.
//
// What: nativeFlags and newNativeFlagSet (-o, --bin, --release, --cpu,
// --profile, --symbols, --frame-pointers and the hidden benchmark flags),
// config, runBuild and runRun.
//
// How: config turns the flags into a driver.Config. mk run builds, then runs
// the program and returns its exit status as mk's; --json diagnostics go to
// stderr, so stdout stays the program's.

package main

import (
	"errors"
	"flag"
	"fmt"
	"io"

	"mk/internal/driver"
)

// nativeFlags are the flags build and run add to the shared ones.
type nativeFlags struct {
	compileFlags
	out     string
	bin     string
	release bool
	cpu     string
	pgoGen  bool
	pgo     string
	debug   bool
	// objects are --object files (driver.Config.ExtraObjects), in order.
	objects []string
	// noBounds and noOverflow are the hidden benchmark-only flags
	// --no-bounds-checks and --no-overflow-checks (driver.TestHooks
	// NoBoundsChecks and NoOverflowChecks): tools/bench prices the checks
	// with them. They are left out of -h, and a program built with them is
	// not a Mk program's semantics.
	noBounds   bool
	noOverflow bool
	// emitLL is the hidden --emit-ll <dir> (driver.TestHooks.LLDir):
	// each bin's pack IR is written to <dir>/<bin>/<pack>.ll, for
	// reading Mk's IR beside a C twin's (tools/bench ir). Benchmark and
	// tooling only, like the two above; the build's outputs are unchanged.
	emitLL string

	// --profile size and --symbols (profile.go).
	profile string
	symbols bool
	// --frame-pointers (framepointers.go).
	framePointers string
}

// hiddenFlags are registered but not printed by -h (benchmark and tooling
// only).
var hiddenFlags = map[string]bool{"no-bounds-checks": true, "no-overflow-checks": true, "emit-ll": true}

func newNativeFlagSet(name string, stderr io.Writer, nf *nativeFlags) *flag.FlagSet {
	fs := newFlagSet(name, stderr, &nf.compileFlags)
	if name == "build" {
		fs.StringVar(&nf.out, "o", "", "output executable (one bin) or directory (several)")
	}
	fs.StringVar(&nf.bin, "bin", "", "build or run only the bin of this name")
	fs.BoolVar(&nf.release, "release", false, "release profile")
	fs.StringVar(&nf.cpu, "cpu", "", "target CPU (default: the target's baseline)")
	fs.BoolVar(&nf.pgoGen, "pgo-gen", false, "release: instrument for a profile")
	fs.StringVar(&nf.pgo, "pgo", "", "release: use this .mkprof, or off (default: default.mkprof in the package root)")
	fs.BoolVar(&nf.debug, "debug", false, "emit full debug info (variables and types), dev or release")
	fs.StringVar(&nf.profile, "profile", "", "size: a release build with every pack at -Oz; checked: optimized as release, with contracts and build.Profile Checked")
	fs.BoolVar(&nf.symbols, "symbols", false, "release: keep the symbols and debug information (PDB, DWARF) for profilers")
	fs.StringVar(&nf.framePointers, "frame-pointers", "", "on or off: keep every function's frame pointer (default: MK_FRAME_POINTERS, else off for x86_64-windows and on for every other target)")
	if name == "build" {
		fs.Func("object", "link this compiled object (COFF on Windows, ELF on Linux) into every executable; repeatable", func(v string) error {
			if v == "" {
				return errors.New("empty object path")
			}
			nf.objects = append(nf.objects, v)
			return nil
		})
		fs.BoolVar(&nf.noBounds, "no-bounds-checks", false, "benchmark only: build without bounds checks")
		fs.BoolVar(&nf.noOverflow, "no-overflow-checks", false, "benchmark only: build without overflow checks")
		fs.Func("emit-ll", "tooling only: also write each bin's pack IR to <dir>/<bin>/<pack>.ll", func(v string) error {
			if v == "" {
				return errors.New("empty directory")
			}
			nf.emitLL = v
			return nil
		})
		fs.Usage = func() { printVisibleFlags(fs, stderr) }
	}
	return fs
}

// printVisibleFlags is -h for a flag set with hidden flags: the usual
// listing of every flag but hiddenFlags.
func printVisibleFlags(fs *flag.FlagSet, w io.Writer) {
	vis := flag.NewFlagSet(fs.Name(), flag.ContinueOnError)
	vis.SetOutput(w)
	fs.VisitAll(func(f *flag.Flag) {
		if !hiddenFlags[f.Name] {
			vis.Var(f.Value, f.Name, f.Usage)
		}
	})
	fmt.Fprintf(w, "Usage of %s:\n", fs.Name())
	vis.PrintDefaults()
}

func (nf *nativeFlags) config(path string, stderr io.Writer) (driver.Config, bool) {
	cfg, ok := nf.compileFlags.config(path, stderr)
	cfg.Bin, cfg.CPU, cfg.Debug = nf.bin, nf.cpu, nf.debug
	cfg.ExtraObjects = nf.objects
	cfg.TestHooks.NoBoundsChecks, cfg.TestHooks.NoOverflowChecks = nf.noBounds, nf.noOverflow
	cfg.TestHooks.LLDir = nf.emitLL
	if nf.release {
		cfg.Profile = driver.ProfileRelease
	}
	if !profileFlags(&cfg, nf.profile, nf.symbols, stderr) || !framePointersFlag(&cfg, nf.framePointers, stderr) {
		return cfg, false
	}
	switch {
	case nf.pgoGen && nf.pgo != "" && nf.pgo != "off":
		fmt.Fprintln(stderr, "mk: --pgo-gen and --pgo=<file> cannot be combined")
		return cfg, false
	case (nf.pgoGen || nf.pgo != "") && !nf.release:
		fmt.Fprintln(stderr, "mk: --pgo-gen and --pgo apply to --release builds only")
		return cfg, false
	case nf.pgoGen:
		cfg.PGO = driver.PGOGen
	case nf.pgo == "off":
		cfg.PGO = driver.PGOOff
	case nf.pgo != "":
		cfg.PGO, cfg.PGOFile = driver.PGOUse, nf.pgo
	}
	return cfg, ok
}

// runBuild is `mk build`: diagnostics like check's (JSON on stdout, text on
// stderr); a clean build prints nothing and leaves the executables at -o or
// ./<bin>[.exe].
func runBuild(args []string, stdout, stderr io.Writer) int {
	var nf nativeFlags
	fs := newNativeFlagSet("build", stderr, &nf)
	path, _, err := parseArgs(fs, args, false)
	if err != nil {
		return usageErr(stderr, fs, err)
	}
	cfg, ok := nf.config(path, stderr)
	if !ok {
		return 2
	}
	s, err := driver.NewSession(cfg)
	if err != nil {
		fmt.Fprintf(stderr, "mk build: %v\n", err)
		return 2
	}
	res, _, err := s.Build(nf.out)
	if err != nil {
		fmt.Fprintf(stderr, "mk build: %v\n", err)
		return 2
	}
	out := stderr
	if nf.json {
		out = stdout
	}
	if err := res.WriteReport(out, nf.json); err != nil {
		fmt.Fprintf(stderr, "mk build: %v\n", err)
		return 2
	}
	_ = res.WriteTimings(cfg.Timings)
	return driver.ExitStatus(res.Report)
}

// runRun is `mk run`: diagnostics on stderr in both forms (stdout is the
// program's), then the program's exit status.
func runRun(args []string, stdout, stderr io.Writer) int {
	var nf nativeFlags
	fs := newNativeFlagSet("run", stderr, &nf)
	path, rest, err := parseArgs(fs, args, true)
	if err != nil {
		return usageErr(stderr, fs, err)
	}
	cfg, ok := nf.config(path, stderr)
	if !ok {
		return 2
	}
	s, err := driver.NewSession(cfg)
	if err != nil {
		fmt.Fprintf(stderr, "mk run: %v\n", err)
		return 2
	}
	res, status, err := s.Run(rest, driver.RunIO{Stdout: stdout, Stderr: stderr})
	if err != nil {
		fmt.Fprintf(stderr, "mk run: %v\n", err)
		return 2
	}
	// The report first, then the timings: with --json
	// stderr holds one JSON document, then the stage lines.
	if len(res.Report.Diags) > 0 {
		if err := res.WriteReport(stderr, nf.json); err != nil {
			fmt.Fprintf(stderr, "mk run: %v\n", err)
			return 2
		}
		_ = res.WriteTimings(cfg.Timings)
		return driver.ExitStatus(res.Report)
	}
	if len(res.Notes) > 0 && !nf.json {
		// The manifest step's notes, as mk check and mk build print them;
		// --json keeps stderr empty on a clean run.
		_ = res.WriteReport(stderr, false)
	}
	_ = res.WriteTimings(cfg.Timings)
	return status
}
