// Why: the dogfood stage's plan decides what runs where and under which lock.
// What: TestGateDogfoodPlan, a table over host, tier, -skip and -only.
// How: checks each planned step's argv, skip and tripwire: quick runs
// `run -changed`, full runs `run` then `cover`; the Linux run is a second
// process outside the tripwire, skipped by -skip linux or a silent WSL; the
// machine lock reaches the harness as MK_MACHINE_LOCKED=1.

package main

import (
	"slices"
	"strings"
	"testing"

	"mk/tools/machinelock"
)

func TestGateDogfoodPlan(t *testing.T) {
	type want struct {
		name     string
		skip     string // a substring of the skip reason, or ""
		args     string // the argv after "go run ./tools/dogfood", joined
		tripwire bool
	}
	for _, tc := range []struct {
		name   string
		goos   string
		tier   string
		skip   []string
		only   []string
		wsl    string
		locked bool
		want   []want
	}{
		{name: "quick windows", goos: "windows", tier: "quick", want: []want{
			{"dogfood run x86_64-windows,x86_64-freestanding", "", "run -changed -target x86_64-windows,x86_64-freestanding -json W/dogfood-last.json -work W", true},
			{"dogfood run x86_64-linux (the harness takes the WSL lock)", "", "run -changed -target x86_64-linux -json W/dogfood-last-linux.json -work W", false},
		}},
		{name: "full windows locked", goos: "windows", tier: "full", locked: true, want: []want{
			{"dogfood run x86_64-windows,x86_64-freestanding", "", "run -target x86_64-windows,x86_64-freestanding -json W/dogfood-last.json -work W", true},
			{"dogfood run x86_64-linux (the harness takes the WSL lock)", "", "run -target x86_64-linux -json W/dogfood-last-linux.json -work W", false},
			{"dogfood cover", "", "cover -work W", true},
		}},
		{name: "quick windows -skip linux", goos: "windows", tier: "quick", skip: []string{"linux"}, want: []want{
			{"dogfood run x86_64-windows,x86_64-freestanding", "", "run -changed -target x86_64-windows,x86_64-freestanding -json W/dogfood-last.json -work W", true},
			{"dogfood run x86_64-linux (the harness takes the WSL lock)", "-skip linux", "", false},
		}},
		{name: "soak windows, WSL down", goos: "windows", tier: "soak", wsl: "no answer", want: []want{
			{"dogfood run x86_64-windows,x86_64-freestanding", "", "run -target x86_64-windows,x86_64-freestanding -json W/dogfood-last.json -work W", true},
			{"dogfood run x86_64-linux (the harness takes the WSL lock)", "WSL did not answer: no answer", "", false},
			{"dogfood cover", "", "cover -work W", true},
		}},
		{name: "quick linux host", goos: "linux", tier: "quick", want: []want{
			{"dogfood run x86_64-linux,x86_64-freestanding", "", "run -changed -target x86_64-linux,x86_64-freestanding -json W/dogfood-last.json -work W", true},
		}},
		{name: "-only", goos: "windows", tier: "full", only: []string{"./internal/check"}, want: []want{{"dogfood", "-only", "", false}}},
		{name: "-skip dogfood", goos: "windows", tier: "full", skip: []string{"dogfood"}, want: []want{{"dogfood", "-skip dogfood", "", false}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := &gate{tier: tc.tier, skip: map[string]bool{}, only: tc.only, work: "W", goExe: "go", wslDown: tc.wsl}
			for _, s := range tc.skip {
				g.skip[s] = true
			}
			if tc.locked {
				g.lock = &machinelock.Lock{}
			}
			got := g.dogfoodPlan(tc.goos)
			if len(got) != len(tc.want) {
				t.Fatalf("%d steps, want %d: %+v", len(got), len(tc.want), got)
			}
			for i, w := range tc.want {
				st := got[i]
				if st.name != w.name {
					t.Errorf("step %d: %q, want %q", i, st.name, w.name)
				}
				if w.skip != "" {
					if !strings.Contains(st.skip, w.skip) {
						t.Errorf("%s: skip %q, want %q", st.name, st.skip, w.skip)
					}
					continue
				}
				if st.skip != "" {
					t.Errorf("%s: skipped (%s)", st.name, st.skip)
					continue
				}
				if !slices.Equal(st.sp.argv[:3], []string{"go", "run", "./tools/dogfood"}) {
					t.Errorf("%s: argv %q", st.name, st.sp.argv)
				}
				args := strings.ReplaceAll(strings.Join(st.sp.argv[3:], " "), `\`, "/")
				if args != w.args {
					t.Errorf("%s: args %q, want %q", st.name, args, w.args)
				}
				if st.sp.tripwire != w.tripwire || st.sp.lock {
					t.Errorf("%s: tripwire %v lock %v, want tripwire %v and no gate-held WSL lock", st.name, st.sp.tripwire, st.sp.lock, w.tripwire)
				}
				if locked := slices.Contains(st.sp.set, "MK_MACHINE_LOCKED=1"); locked != tc.locked {
					t.Errorf("%s: MK_MACHINE_LOCKED=1 set %v, want %v", st.name, locked, tc.locked)
				}
			}
		})
	}
}
