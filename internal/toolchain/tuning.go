// Why: pass-pipeline and LTO options (unroll thresholds, vectorizers, full
// LTO) need A/B measurement against the default build without a flag that
// users could set or that could leak into a default build's keys.
//
// What: Tuning, ParseTuning, Baked and the option lists each compile and
// link step takes (ltoOptions, prelinkOptions, backendOptions).
//
// How: a Tuning is baked into an mk binary at build time and never set by
// a flag of mk itself:
//
//	go build -ldflags "-X mk/internal/toolchain.experiment=unroll=300,noslp" -o mk-x.exe ./cmd/mk
//
// The zero Tuning, which every default build has, gives the default command
// lines byte for byte (TestZeroTuningArgs). A binary with an experiment
// has its own build ID, and the driver hashes a non-zero Tuning into its
// keys, so no cache entry is shared with a default binary; tools/bench
// compares two such binaries as an A/B of one option. Key isolation in
// detail, and where each option acts, are set out above ltoOptions.

package toolchain

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// experiment is the Tuning this binary was built with, set only by the Go
// linker's -X (above); "" in every default build.
var experiment string

// Tuning is one experiment configuration. The zero value is the
// default build.
type Tuning struct {
	// Unroll is the unroll threshold: 0 keeps ltoUnroll (400) at every
	// LTO step and none at the pack compiles; a positive N passes
	// -unroll-threshold=N to every LTO step and every release pack
	// compile; UnrollLLVM passes no threshold anywhere, so LLVM's -O3
	// default (300) applies.
	Unroll int
	// NoLoopVec turns the loop vectorizer off (-vectorize-loops=false: it
	// vectorizes only a loop whose metadata forces it, and codegen writes
	// none).
	NoLoopVec bool
	// NoSLP turns the SLP vectorizer off (-vectorize-slp=false).
	NoSLP bool
	// ExtraVec runs LLVM's cleanup passes after the loop vectorizer
	// (-extra-vectorizer-passes).
	ExtraVec bool
	// Interleave forces the loop vectorizer's interleave count
	// (-force-vector-interleave=N); 0 lets the cost model choose.
	Interleave int
	// FatLTO compiles release packs to full-LTO bitcode (-flto=full) and
	// links them as one module with the linker's own LTO (lld-link
	// /opt:lldlto=3 on COFF, never thinLink; ld.lld --lto-O3 on ELF).
	// lld-link's LTO, full or thin, runs no SLP vectorizer, so on COFF SLP
	// runs only in each pack's -flto=full pre-link compile; ld.lld's full
	// LTO runs it after the link too.
	FatLTO bool
}

// UnrollLLVM is Tuning.Unroll for "no threshold option": LLVM's default.
const UnrollLLVM = -1

// IsZero reports whether t is the default build.
func (t Tuning) IsZero() bool { return t == Tuning{} }

// String is t in ParseTuning's syntax, in a fixed order, "" for the zero
// Tuning. It is the form the driver hashes into its keys.
func (t Tuning) String() string {
	var parts []string
	switch {
	case t.Unroll == UnrollLLVM:
		parts = append(parts, "unroll=llvm")
	case t.Unroll > 0:
		parts = append(parts, "unroll="+strconv.Itoa(t.Unroll))
	}
	if t.NoLoopVec {
		parts = append(parts, "novec")
	}
	if t.NoSLP {
		parts = append(parts, "noslp")
	}
	if t.ExtraVec {
		parts = append(parts, "extravec")
	}
	if t.Interleave > 0 {
		parts = append(parts, "interleave="+strconv.Itoa(t.Interleave))
	}
	if t.FatLTO {
		parts = append(parts, "fatlto")
	}
	return strings.Join(parts, ",")
}

// errIRCE is ParseTuning's error for irce: no pass pipeline clang or lld
// runs has that pass.
var errIRCE = errors.New("irce: InductiveRangeCheckElimination is in no pass pipeline clang 21 or lld runs, " +
	"and no option of either adds it (clang -mllvm -help-hidden lists only irce-* tuning options of the pass); " +
	"mk runs no opt, so the experiment cannot run")

// ParseTuning reads a comma-separated list of experiment names:
// unroll=<N> (1 to 100000) or unroll=llvm, novec, noslp, extravec,
// interleave=<N> (1 to 16) and fatlto. Each name at most once; "" is the
// zero Tuning.
func ParseTuning(s string) (Tuning, error) {
	var t Tuning
	if s == "" {
		return t, nil
	}
	seen := map[string]bool{}
	for _, f := range strings.Split(s, ",") {
		name, val, hasVal := strings.Cut(f, "=")
		if seen[name] {
			return Tuning{}, fmt.Errorf("experiment %q: %s given twice", s, name)
		}
		seen[name] = true
		num := func(lo, hi int) (int, error) {
			n, err := strconv.Atoi(val)
			if !hasVal || err != nil || n < lo || n > hi {
				return 0, fmt.Errorf("experiment %q: %s takes a number from %d to %d", s, name, lo, hi)
			}
			return n, nil
		}
		flag := func() error {
			if hasVal {
				return fmt.Errorf("experiment %q: %s takes no value", s, name)
			}
			return nil
		}
		var err error
		switch name {
		case "unroll":
			if val == "llvm" {
				t.Unroll = UnrollLLVM
			} else {
				t.Unroll, err = num(1, 100000)
			}
		case "novec":
			err, t.NoLoopVec = flag(), true
		case "noslp":
			err, t.NoSLP = flag(), true
		case "extravec":
			err, t.ExtraVec = flag(), true
		case "interleave":
			t.Interleave, err = num(1, 16)
		case "fatlto":
			err, t.FatLTO = flag(), true
		case "irce":
			err = errIRCE
		default:
			err = fmt.Errorf("experiment %q: unknown name %q (unroll=<N>|llvm, novec, noslp, extravec, interleave=<N>, fatlto)", s, name)
		}
		if err != nil {
			return Tuning{}, err
		}
	}
	return t, nil
}

// Baked is the Tuning this binary was built with (experiment); an error
// when the -X value does not parse, which every native command reports.
func Baked() (Tuning, error) { return ParseTuning(experiment) }

// Key isolation in detail: a binary with an experiment has its own
// build ID (the SHA-256 of the executable), so no object or link key is
// shared with the default binary's (thinLink's backend cache is keyed by the
// whole backend line, which holds the options); the driver also hashes a
// non-zero Tuning into the link key, and whatever of it reaches a pack
// compile (fatlto, an unroll threshold) into the object and pack keys
// (CompileJob.CompileKey), for tests that set it in a Config, and a non-zero
// Tuning drops lld's own ThinLTO cache, whose key does not hash -mllvm
// options. tools/bench takes such a binary as -mk, -base or -head unchanged,
// so `compare -base <default> -head <experiment>` is an A/B of one option.
//
// Where each option acts (clang and lld 21.1.8, -print-pipeline-passes):
//   - Every ThinLTO backend runs the whole -O3 loop pipeline (thinLink's
//     clang on COFF, ld.lld's in-process backend on ELF), and takes every
//     option (backendOptions).
//   - A ThinLTO pack compile (-O3 -flto=thin, the pre-link pipeline) runs
//     the full unroller (loop-unroll-full, at LLVM's 300) and neither
//     vectorizer, so it takes only an unroll=<N> threshold
//     (prelinkOptions): fmtints.mk:40's 21-iteration digit loop is fully
//     unrolled there, before any backend runs.
//   - Under fatlto the packs compile -flto=full, whose pre-link pipeline
//     runs the loop vectorizer, the SLP vectorizer and both unrollers, so
//     those compiles take the backend options; with no unroll option that
//     is ltoUnroll's 400 where a ThinLTO pack compile has 300.
//   - lld-link's own LTO, thin or full, runs no SLP vectorizer, so under
//     COFF fatlto SLP runs only in each pack's pre-link compile, before
//     cross-pack inlining, and the link's /mllvm:-vectorize-slp=false does
//     nothing; ld.lld's full LTO runs SLP after the link as well.
//   - The support pack (memcpy, memmove, memset, memcmp, bcmp and the
//     runtime's tables) compiles native at -O3, with every loop pass, and
//     no experiment reaches it: its vector copy and fill loops stay under
//     novec, and its SLP stores under noslp.

// ltoOptions are the LLVM options of every LTO step, without the -mllvm
// or /mllvm: prefix: the loop alignment, the unroll threshold, then the
// experiment's own, in a fixed order. For the zero Tuning they are
// ltoLoopAlign and ltoUnroll.
func (t Tuning) ltoOptions() []string {
	opts := []string{ltoLoopAlign}
	return append(opts, t.backendOptions()...)
}

// mllvm is ltoOptions as ld.lld's arguments, each after -mllvm.
func (t Tuning) mllvm() []string {
	var args []string
	for _, o := range t.ltoOptions() {
		args = append(args, "-mllvm", o)
	}
	return args
}

// prelinkOptions are the LLVM options of a ThinLTO pack compile (-O3
// -flto=thin), whose pipeline runs the full unroller and neither vectorizer
// (clang 21.1.8, -mllvm -print-pipeline-passes: loop-unroll-full only): an
// unroll=<N> threshold, nothing else. unroll=llvm and the zero Tuning pass
// none, so the compile keeps LLVM's 300 (fmtints.mk:40's 21-iteration digit
// loop is fully unrolled there at 300, and kept rolled with
// -unroll-threshold=100).
func (t Tuning) prelinkOptions() []string {
	if t.Unroll > 0 {
		return []string{"-unroll-threshold=" + strconv.Itoa(t.Unroll)}
	}
	return nil
}

// backendOptions are ltoOptions without the loop alignment, which a
// ThinLTO backend gets as clang's -falign-loops=32.
func (t Tuning) backendOptions() []string {
	var opts []string
	switch {
	case t.Unroll == 0:
		opts = append(opts, ltoUnroll)
	case t.Unroll > 0:
		opts = append(opts, "-unroll-threshold="+strconv.Itoa(t.Unroll))
	}
	if t.NoLoopVec {
		opts = append(opts, "-vectorize-loops=false")
	}
	if t.NoSLP {
		opts = append(opts, "-vectorize-slp=false")
	}
	if t.ExtraVec {
		opts = append(opts, "-extra-vectorizer-passes")
	}
	if t.Interleave > 0 {
		opts = append(opts, "-force-vector-interleave="+strconv.Itoa(t.Interleave))
	}
	return opts
}
