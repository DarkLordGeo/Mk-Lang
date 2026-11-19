// Why: the dogfood programs exercise the compiler and std as real users do;
// the gate runs them like every other tool, as a separate process.
//
// What: dogfoodSteps and dogfoodPlan, which plan the stage's steps (dfStep)
// for a host: run, cover, and the Linux run, with their skips.
//
// How: the quick tier runs only the dogfoods whose package, std
// dependencies or compiler changed since their last whole-folder pass
// (run -changed); the full and soak tiers run all of them, then cover, which
// measures execution on Windows. The Windows and freestanding runs go under
// the tripwire; the Linux run goes in a second process without it, because
// tools/dogfood takes the WSL lock itself (tools/wsl-lock.ps1 around its
// exec-jobs child, -p 1 by construction), so the gate must not take it too.

package main

import (
	"path/filepath"
	"runtime"
)

// dfStep is one planned step of the dogfood stage: its command, or why it
// does not run.
type dfStep struct {
	name string
	skip string // why it does not run; "" when it runs
	sp   spec
	note string
}

// dogfoodSteps runs tools/dogfood's run (and cover in the full and soak
// tiers).
func (g *gate) dogfoodSteps() {
	for _, st := range g.dogfoodPlan(runtime.GOOS) {
		if st.skip != "" {
			g.skipStep(st.name, st.skip)
			continue
		}
		s := g.newStep(st.name)
		g.plain(s, st.sp)
		s.Notes = append(s.Notes, st.note)
	}
}

// dogfoodPlan is the dogfood stage for a gate on goos: the steps, their
// argv and environment, and the skips (-skip dogfood, -only, -skip linux,
// a WSL that did not answer the gate's probe).
func (g *gate) dogfoodPlan(goos string) []dfStep {
	if g.skip["dogfood"] {
		return []dfStep{{name: "dogfood", skip: "-skip dogfood"}}
	}
	if len(g.only) > 0 {
		return []dfStep{{name: "dogfood", skip: "-only limits the gate to packages; the dogfoods are not one"}}
	}
	base := []string{g.goExe, "run", "./tools/dogfood"}
	common := []string{"-work", g.work}
	var set []string
	if g.lock != nil {
		set = append(set, "MK_MACHINE_LOCKED=1") // the harness must not wait for the gate's own lock
	}
	runArgs := []string{"run"}
	if g.tier == "quick" {
		runArgs = append(runArgs, "-changed")
	}
	argv := func(target, out string) []string {
		a := append(append([]string{}, base...), runArgs...)
		a = append(a, "-target", target, "-json", out)
		return append(a, common...)
	}
	host := "x86_64-windows,x86_64-freestanding"
	if goos != "windows" {
		host = "x86_64-linux,x86_64-freestanding"
	}
	last := filepath.Join(g.work, "dogfood-last.json")
	out := []dfStep{{name: "dogfood run " + host, sp: spec{argv: argv(host, last), set: set, tripwire: true}, note: "report " + last}}
	if goos == "windows" {
		const name = "dogfood run x86_64-linux (the harness takes the WSL lock)"
		switch {
		case g.skip["linux"]:
			out = append(out, dfStep{name: name, skip: "-skip linux"})
		case g.wslDown != "":
			out = append(out, dfStep{name: name, skip: "WSL did not answer: " + g.wslDown})
		default:
			lx := filepath.Join(g.work, "dogfood-last-linux.json")
			out = append(out, dfStep{name: name, sp: spec{argv: argv("x86_64-linux", lx), set: set}, note: "report " + lx})
		}
	}
	if g.tier == "quick" {
		return out
	}
	// cover measures execution on Windows only (run has checked every case
	// on both hosted targets), so
	// it runs under the tripwire and starts no wsl.exe.
	cov := append(append(append([]string{}, base...), "cover"), common...)
	return append(out, dfStep{name: "dogfood cover", sp: spec{argv: cov, set: set, tripwire: true},
		note: "Dogfoods/COVERAGE.md; report " + filepath.Join(g.work, "dogfood-cover.json")})
}
