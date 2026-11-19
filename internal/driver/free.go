// Why: the x86_64-freestanding target has no OS: no std.rt and no task
// runtime; the driver must restrict std, choose other runtime roots, and
// build bins general-regs-only by default (no floating-point registers).
//
// What: freeStd, freeOnly and stdFormed (the std modules a freestanding
// program may import), freeRuntimeRoots and rootsFor, the bin CPU settings
// (generalRegsOnly, validateBins, groJobs, groWalk), cTargetFlags, and
// freeTaskUses (task intrinsics a freestanding program may not use).
//
// How: std modules outside the freestanding list are never formed, so they
// are never checked or rooted; a general-regs-only bin gets its own mono
// walk, whose E1108s mk check reports.

package driver

import (
	"fmt"
	"slices"
	"strings"

	"mk/internal/diag"
	"mk/internal/mir"
	"mk/internal/mono"
	"mk/internal/source"
	"mk/internal/types"
)

// freeStd are the std modules a freestanding build forms and a freestanding
// program may import (doc/std/README.md's layers): layer 0 (target, core, math,
// simd, atomic, mmio, hash), layer 1 (mem, coll, text), std.free, the
// generator modules derive and dyn with dyn.erased, std.raw under its unsafe
// rule, and std.sys, which std/pkg.mk selects as std/free/sys on this target
// (std-internal, as on every target). Any other std module is E0202 reason
// freestanding there, and is not formed, so std.rt, std.task and the hosted
// layers are never checked or rooted. std.simd.kernel is std-only and formed
// here because std.hash, of layer 0, imports it; a freestanding program's
// own import of it is E0202 reason std-internal, as on every target; so is
// std.target, which std.core and std.simd import.
var freeStd = []string{"std.atomic", "std.coll", "std.core", "std.derive", "std.dyn", "std.dyn.erased",
	"std.free", "std.hash", "std.math", "std.mem", "std.mmio", "std.raw", "std.simd", "std.simd.kernel", "std.sys", "std.target", "std.text"}

// freeOnly are the std modules only the freestanding target forms.
var freeOnly = []string{"std.free"}

// isFree reports whether t is the freestanding target (no OS).
func isFree(t types.Target) bool { return t.OS == types.OSFreestanding }

// stdFormed reports whether a std module named name (std.<dir>) is formed
// for target t: on freestanding only freeStd's, elsewhere every one but
// freeOnly's.
func stdFormed(t types.Target, name string) bool {
	if isFree(t) {
		return slices.Contains(freeStd, name)
	}
	return !slices.Contains(freeOnly, name)
}

// freeRuntimeRoots are the runtime roots of a freestanding build: std.free's
// panic hooks, which codegen calls on this target; never std.rt's or
// std.task's. The float remainder functions join them in a bin that is not
// general-regs-only, where a float % may be compiled (a general-regs-only
// bin's float % is E1108 at the operation, and the roots themselves would
// be).
var freeRuntimeRoots = []types.Key{
	types.ItemKey("std.free", "PanicAt"),
	types.ItemKey("std.free", "PanicMsg"),
	types.ItemKey("std.free", "PanicErr"),
}

var floatRemRoots = []types.Key{
	types.ItemKey("std.math", "Rem64"),
	types.ItemKey("std.math", "Rem32"),
}

// rootsFor is the runtime root list of target t, for a bin built
// general-regs-only when gro.
func rootsFor(t types.Target, gro bool) []types.Key {
	if !isFree(t) {
		return runtimeRoots
	}
	if gro {
		return freeRuntimeRoots
	}
	return append(append([]types.Key(nil), freeRuntimeRoots...), floatRemRoots...)
}

// freeCPU is the target-cpu of x86_64-freestanding: plain x86-64 (v1), with
// no hosted floor.
const freeCPU = "x86-64"

// Bin CPU settings (pkg.Bin's cpu).
const (
	binCPUDefault     = ""
	binCPUGeneralRegs = "general-regs-only"
	binCPUBaseline    = "baseline"
)

// generalRegsOnly reports whether a bin with cpu setting cpu on target t is
// built general-regs-only: the default on x86_64-freestanding, and
// "general-regs-only" itself; "baseline" turns it off. A hosted bin is never
// (validateBin refuses the setting there).
func generalRegsOnly(t types.Target, cpu string) bool {
	if !isFree(t) {
		return false
	}
	return cpu == binCPUDefault || cpu == binCPUGeneralRegs
}

// validateBins checks the bins of manifest m built for target tg: a bin's
// cpu is "", "general-regs-only" or "baseline"; on a hosted target its
// link_script is "" and its cpu is not "general-regs-only"; a freestanding
// link_script names a file of the package, relative to its root. Each
// violation is E0202 at the field, with facts field, value and target.
func validateBins(m *Manifest, t *srcTree, tg types.Target) []*diag.Diagnostic {
	var out []*diag.Diagnostic
	fail := func(k int, field, value, format string, args ...any) {
		path := m.binField(k) + "." + field
		out = append(out, diag.New(diag.E0202, m.spanOf(path), fmt.Sprintf(format, args...)).
			Text("manifest", path).Text("reason", "invalid").Text("field", field).Text("value", value).
			Text("target", tg.Name))
	}
	for k, b := range m.Bins {
		switch b.CPU {
		case binCPUDefault, binCPUBaseline:
		case binCPUGeneralRegs:
			if !isFree(tg) {
				fail(k, "cpu", b.CPU, "bin %s: cpu \"general-regs-only\" is for x86_64-freestanding; %s's bins take \"\" or \"baseline\"", b.Name, tg.Name)
			}
		default:
			fail(k, "cpu", b.CPU, "bin %s: cpu %q is not \"\", \"general-regs-only\" or \"baseline\"", b.Name, b.CPU)
		}
		// A bin is a console program (the default) or, on Windows only,
		// a GUI program linked with /subsystem:windows and no console.
		switch b.Subsystem {
		case "", "console":
		case "windows":
			if tg.OS != types.OSWindows {
				fail(k, "subsystem", b.Subsystem, "bin %s: subsystem \"windows\" is for x86_64-windows; %s's bins are console programs", b.Name, tg.Name)
				break
			}
		default:
			fail(k, "subsystem", b.Subsystem, "bin %s: subsystem %q is not \"console\" or \"windows\"", b.Name, b.Subsystem)
		}
		switch {
		case b.LinkScript == "":
		case !isFree(tg):
			fail(k, "link_script", b.LinkScript, "bin %s: a linker script is for x86_64-freestanding; %s links without one", b.Name, tg.Name)
		case strings.HasPrefix(b.LinkScript, "/") || strings.Contains(b.LinkScript, "\\") || strings.Contains(b.LinkScript, ".."):
			fail(k, "link_script", b.LinkScript, "bin %s: link_script %s must be a package-relative path with / separators", b.Name, b.LinkScript)
		default:
			if _, err := t.readFile(b.LinkScript); err != nil && t.osRoot != "" {
				fail(k, "link_script", b.LinkScript, "bin %s: link_script %s does not exist (as spelled)", b.Name, b.LinkScript)
			}
		}
	}
	return out
}

// groName is E1108's catalogue name (mono emits it, 04e; the driver only
// selects mono's E1108s from the collection, so it names no code it does
// not emit, TestDriverEmitsOnlyItsCodes).
const groName = "general-regs-only"

// groJob is one general-regs-only bin the check judges: its name, its root
// module and the modules whose export externs are its roots.
type groJob struct {
	bin, mainMod string
	pkg          []*modNode
}

// groJobs are the session's general-regs-only bins (the default on
// x86_64-freestanding), none on a hosted target. A --bin that names no bin
// gives none here; Build reports it.
func (s *Session) groJobs() []groJob {
	if !isFree(s.cfg.Target) || s.pkgB == nil {
		return nil
	}
	bins, err := s.bins()
	if err != nil {
		return nil
	}
	var out []groJob
	for _, b := range bins {
		if generalRegsOnly(s.cfg.Target, b.cpu) {
			out = append(out, groJob{bin: b.name, mainMod: b.mod.path, pkg: s.binMods(b.mod)})
		}
	}
	return out
}

// groWalk is the general-regs-only check at mk check: for each
// general-regs-only bin, mono's collection over the bin's build roots with
// GeneralRegsOnly set, keeping its E1108s (the instance-limit walk reports
// the rest). mk build runs the check first, so both report this set. Skipped
// as walk is, when ds holds a diagnostic of a phase before types.
func (w *world) groWalk(b *build, jobs []groJob, ds []*diag.Diagnostic) []*diag.Diagnostic {
	if len(jobs) == 0 || w.sem5 == nil || earlierThanTypes(ds) {
		return nil
	}
	env := walkEnv{monoEnv: w.sem5.monoEnv(w), noFlow: w.noFlow, lost: w.lostLiterals(b)}
	var out []*diag.Diagnostic
	for _, j := range jobs {
		opt := w.monoOptions()
		opt.GeneralRegsOnly, opt.Bin = true, j.bin
		var got []*diag.Diagnostic
		if ie := source.Guard("general-regs-only walk of "+j.bin, source.Span{}, func() {
			_, got = mono.Collect(env, w.buildRoots(j.mainMod, j.pkg, false, true), opt)
		}); ie != nil {
			return []*diag.Diagnostic{diag.FromInternal(ie)}
		}
		for _, d := range got {
			if d.Code.Name() == groName { // mono constructs it; the driver only keeps it
				out = append(out, d)
			}
		}
	}
	return out
}

// cTargetFlags are the flags a bin's C jobs (the import shim and every
// c_sources file, not asm_sources) get before the manifest's own: the
// build's CPU as -march on x86_64, so C code is compiled for the CPU the Mk
// code is; on x86_64-freestanding -mno-red-zone, since an interrupt can
// arrive at any instruction, and -mgeneral-regs-only in a general-regs-only
// bin, whose C must not touch the FPU state either. The flags are on the
// job's command line, so they are in its object's cache key.
func cTargetFlags(t types.Target, cpu string, gro bool) []string {
	var out []string
	if t.Arch == types.ArchX64 && cpu != "" {
		out = append(out, "-march="+cpu)
	}
	if isFree(t) {
		out = append(out, "-mno-red-zone")
		if gro {
			out = append(out, "-mgeneral-regs-only")
		}
	}
	return out
}

// freeTaskIntrinsics are the compiler-known intrinsics codegen lowers to a
// std.task entry (internal/codegen spawn.go): the scope forms, Cancel and
// the deadline functions, by key.
var freeTaskIntrinsics = map[string]string{
	"std.core.Io:Scope": "Io.Scope", "std.core.Io:ScopeOf": "Io.ScopeOf", "std.core.Io:ScopeWith": "Io.ScopeWith",
	"std.core.Scope:Cancel": "Scope.Cancel", "std.core.Scope:CancelAfter": "Scope.CancelAfter",
	"std.core.Scope:Expired": "Scope.Expired", "std.core.Scope:Remaining": "Scope.Remaining",
}

// freeTaskUses: on x86_64-freestanding std.task is not formed (freeStd), so
// a scope intrinsic or an s.go that the build reaches, called or named as a
// value, has no runtime entry to lower to. Each such site is E0202 reason
// freestanding naming std.task, at the use, rather than codegen's "missing
// runtime entry" ICE (one reached only through a const's or global's value
// is reported at its declaration). Judged over the build's instances only,
// so a function nothing reaches stays as mk check leaves it. One diagnostic
// per site, in instance order.
func (w *world) freeTaskUses(in *mono.Instances) []*diag.Diagnostic {
	var out []*diag.Diagnostic
	seen := map[source.Span]bool{} // a generic body's sites recur per instance
	report := func(b *mir.Body, span mir.SpanID, what string) {
		at := b.Span(span)
		if seen[at] {
			return
		}
		seen[at] = true
		out = append(out, diag.New(diag.E0202, at, what+" needs std.task, which x86_64-freestanding does not form: "+
			"a freestanding program has no scheduler").Text("module", "std.task").Text("reason", "freestanding"))
	}
	name := func(it types.ItemID) string { return freeTaskIntrinsics[w.items.Get(it).Key.String()] }
	named := map[types.ItemID]bool{} // intrinsics reported at a site
	for i := range in.List {
		b := in.List[i].Body
		if b == nil {
			continue
		}
		for bi := range b.Blocks {
			blk := &b.Blocks[bi]
			for si := range blk.Stmts {
				st := &blk.Stmts[si]
				if st.Kind == mir.StAssign && st.Rvalue.Kind == mir.RvFnRef {
					if n := name(st.Rvalue.Fn.Item); n != "" {
						named[st.Rvalue.Fn.Item] = true
						report(b, st.Span, "`"+n+"`")
					}
				}
			}
			t := &blk.Term
			if t.Kind != mir.TermCall && t.Kind != mir.TermTailCall || t.Callee.Kind != mir.CallDirect {
				continue // a tail call names its callee as a call does
			}
			if n := name(t.Callee.Fn.Item); n != "" {
				named[t.Callee.Fn.Item] = true
				report(b, t.Span, "`"+n+"`")
			} else if t.Spawn != mir.NoLocal {
				report(b, t.Span, "`s.go`")
			}
		}
	}
	// An intrinsic reached only as a fn value inside a const's or global's
	// value (mono's relocation requests) has no site in a body: it is
	// reported at its declaration, so the build still stops before codegen.
	for i := range in.List {
		it := in.List[i].Item
		if n := name(it); n != "" && !named[it] {
			named[it] = true
			out = append(out, diag.New(diag.E0202, w.items.Get(it).Span, "`"+n+"` is reached through the fn value of a const or global, and needs std.task, "+
				"which x86_64-freestanding does not form: a freestanding program has no scheduler").Text("module", "std.task").Text("reason", "freestanding"))
		}
	}
	return out
}
