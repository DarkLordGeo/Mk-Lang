// Why: under WSL's mirrored networking a port used on Linux stays taken on
// Windows, so program cases must not share fixed ports across targets.
// What: TestFixedPortsNotSharedAcrossTargets with fixedPortProblems and the
// listenPortArg table.
// How: reads every .cases file and the source of every program that listens.
//
// The fixed-port rule for program cases: on a Windows host whose WSL runs in
// mirrored networking mode, a port a Linux process bound on loopback stays
// unbindable on the Windows host after the process exits cleanly. So a fixed
// port that a case hands to an x86_64-linux run must never be handed to an
// x86_64-windows run, and one case may not run a fixed port on both targets.

package corpus

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// listenPortArg names, for each program that binds a listening port, the
// index in a case's args of that port. TestFixedPortsNotSharedAcrossTargets
// fails a program under tests/programs that calls net.Listen and has no
// entry, so a new server cannot slip past the guard.
var listenPortArg = map[string]int{
	ProgramsDir + "/echo.mk":      0,
	ProgramsDir + "/notes.mk":     noPortArg,
	ProgramsDir + "/supervise.mk": noPortArg,
	ProgramsDir + "/sockets.mk":   noPortArg,
	ProgramsDir + "/httploop":     noPortArg,
}

// noPortArg marks a program that takes no port argument: every net.Listen
// call it makes names a literal address on port 0, so the OS chooses the
// port and none is fixed. TestFixedPortsNotSharedAcrossTargets checks that
// claim against the source.
const noPortArg = -1

// fixedPortProblems checks cs against listenPortArg. A fixed port is the
// port argument when it parses as an integer above 0 (port 0 lets the OS
// choose, which is never shared on purpose). It reports a fixed-port case
// that runs on both targets, and a fixed port that one case runs on
// x86_64-linux and another on x86_64-windows, naming both case lines.
func fixedPortProblems(cs []ProgramCase, portArg map[string]int) []string {
	type use struct{ at, target string }
	byPort := map[int][]use{}
	var problems []string
	for _, c := range cs {
		i, ok := portArg[c.Program]
		if !ok || i < 0 || i >= len(c.Args) {
			continue
		}
		port, err := strconv.Atoi(c.Args[i])
		if err != nil || port <= 0 {
			continue
		}
		if len(c.Targets) != 1 {
			problems = append(problems, fmt.Sprintf("%s: case %s binds fixed port %d on %s; a fixed port must name one target",
				c.At, c.Name, port, strings.Join(c.Targets, " and ")))
		}
		for _, t := range c.Targets {
			byPort[port] = append(byPort[port], use{c.At, t})
		}
	}
	ports := make([]int, 0, len(byPort))
	for p := range byPort {
		ports = append(ports, p)
	}
	slices.Sort(ports)
	for _, p := range ports {
		var linux, windows []string
		for _, u := range byPort[p] {
			switch u.target {
			case "x86_64-linux":
				linux = append(linux, u.at)
			case "x86_64-windows":
				windows = append(windows, u.at)
			}
		}
		if len(linux) > 0 && len(windows) > 0 {
			problems = append(problems, fmt.Sprintf("fixed port %d is bound on x86_64-linux (%s) and on x86_64-windows (%s); under WSL's mirrored networking the Linux bind holds it on the Windows host",
				p, strings.Join(slices.Compact(linux), ", "), strings.Join(slices.Compact(windows), ", ")))
		}
	}
	return problems
}

func TestFixedPortsNotSharedAcrossTargets(t *testing.T) {
	root := repoRoot(t)
	cs, err := LoadCases(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range fixedPortProblems(cs, listenPortArg) {
		t.Error(p)
	}
	// Every program with a fixed-port entry has cases, and every program
	// that listens has an entry.
	programs := map[string]bool{}
	for _, c := range cs {
		programs[c.Program] = true
	}
	for p := range listenPortArg {
		if !programs[p] {
			t.Errorf("listenPortArg names %s, which has no cases", p)
		}
	}
	for p := range programs {
		full := filepath.Join(root, filepath.FromSlash(p))
		if i, ok := listenPortArg[p]; ok {
			if i == noPortArg {
				for _, bad := range listenNotPortZero(t, full) {
					t.Errorf("%s: listenPortArg says it takes no port argument, but it listens on something other than a literal port 0: %s", p, bad)
				}
			}
			continue
		}
		if !listens(t, full) {
			continue
		}
		t.Errorf("%s calls net.Listen but listenPortArg has no entry for it; name the index of its port argument", p)
	}
}

// listenPortZero matches a net.Listen call whose address is a string literal
// ending in port 0.
var listenPortZero = regexp.MustCompile(`net\.Listen\([^,()]*,\s*"[^"]*:0"\)`)

// listenNotPortZero returns the lines of the program at p (a file or a
// directory of .mk files) that call net.Listen with anything but a literal
// port-0 address.
func listenNotPortZero(t *testing.T, p string) []string {
	t.Helper()
	var bad []string
	for _, f := range programFiles(t, p) {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for i, l := range strings.Split(string(b), "\n") {
			if strings.Contains(l, "net.Listen(") && !listenPortZero.MatchString(l) {
				bad = append(bad, fmt.Sprintf("%s:%d: %s", filepath.Base(f), i+1, strings.TrimSpace(l)))
			}
		}
	}
	return bad
}

// programFiles is p itself, or the .mk files of p when it is a directory.
func programFiles(t *testing.T, p string) []string {
	t.Helper()
	files := []string{p}
	if fi, err := os.Stat(p); err == nil && fi.IsDir() {
		files, err = filepath.Glob(filepath.Join(p, "*.mk"))
		if err != nil {
			t.Fatal(err)
		}
	}
	return files
}

// listens reports whether the program at p (a file or a directory of .mk
// files) calls net.Listen.
func listens(t *testing.T, p string) bool {
	t.Helper()
	for _, f := range programFiles(t, p) {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), "net.Listen(") {
			return true
		}
	}
	return false
}

func TestFixedPortProblems(t *testing.T) {
	const p = ProgramsDir + "/echo.cases"
	parse := func(text string) []ProgramCase {
		t.Helper()
		cs, err := ParseCases(p, text)
		if err != nil {
			t.Fatal(err)
		}
		return cs
	}
	ok := parse("case w\ntarget x86_64-windows\nargs 18220 0\n\ncase l\ntarget x86_64-linux\nargs 18218 0\n\ncase zero\nargs 0 0\n\ncase bad\nargs x 1\nexit 1\n")
	if got := fixedPortProblems(ok, listenPortArg); len(got) != 0 {
		t.Errorf("disjoint ports, port 0 and a non-number: %q, want none", got)
	}
	shared := parse("case w\ntarget x86_64-windows\nargs 18218 0\n\ncase l\ntarget x86_64-linux\nargs 18218 0\n")
	got := fixedPortProblems(shared, listenPortArg)
	if len(got) != 1 || !strings.Contains(got[0], "fixed port 18218") || !strings.Contains(got[0], p+":1") || !strings.Contains(got[0], p+":5") {
		t.Errorf("one port on both targets: %q, want one problem naming %s:1 and %s:5", got, p, p)
	}
	both := parse("case b\nargs 18218 0\n")
	got = fixedPortProblems(both, listenPortArg)
	if len(got) != 2 || !strings.Contains(got[0], "must name one target") || !strings.Contains(got[1], "fixed port 18218") {
		t.Errorf("one case on both targets: %q, want the one-target problem and the shared-port problem", got)
	}
	// Two Windows cases may share a port: they run one after the other.
	twoWin := parse("case a\ntarget x86_64-windows\nargs 18220 0\n\ncase b\ntarget x86_64-windows\nprofile release\nargs 18220 0\n")
	if got := fixedPortProblems(twoWin, listenPortArg); len(got) != 0 {
		t.Errorf("two Windows cases on one port: %q, want none", got)
	}
}
