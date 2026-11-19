// Why: the gate judges tests from go test's live output, and a package that
// times out or crashes must still yield a result per test, not one opaque
// failure.
//
// What: lineWriter (splits any command's output, fuzz progress included, into
// lines as it arrives), testParse (folds go test -json events into counts,
// failures and MK_LINUX skips) and the readers of the testing package's alarm
// panic and go test's kill line.
//
// How: each line is parsed as it arrives. When a package ends before every
// started test has a result, endedEntry records one failure naming the
// package and why ("timed out after 1h0m0s", "ended mid-run"), with the
// running tests from the panic's "running tests:" list, so later steps can
// tell a test that did not run from one that is missing.

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"
)

// The skip reasons of the MK_LINUX and MK_TIMING switches, and wslGuard's panic.
const (
	linuxOff  = "MK_LINUX=1 not set"
	timingOff = "MK_TIMING=1 not set"
	guardHit  = "started without MK_LINUX=1"
	lockHit   = "outside the WSL lock"
)

// lineWriter copies a command's output to its log and hands each complete
// line to line. exec.Cmd calls Write from one goroutine at a time when
// Stdout and Stderr are the same writer.
type lineWriter struct {
	log  io.Writer
	part []byte
	line func(string)
}

func (w *lineWriter) Write(p []byte) (int, error) {
	if w.log != nil {
		w.log.Write(p)
	}
	w.part = append(w.part, p...)
	for {
		i := bytes.IndexByte(w.part, '\n')
		if i < 0 {
			break
		}
		w.line(strings.TrimRight(string(w.part[:i]), "\r"))
		w.part = w.part[i+1:]
	}
	if len(w.part) == 0 {
		w.part = nil
	}
	return len(p), nil
}

func (w *lineWriter) flush() {
	if len(w.part) > 0 {
		w.line(strings.TrimRight(string(w.part), "\r"))
		w.part = nil
	}
}

// testEvent is one line of go test -json (cmd/test2json).
type testEvent struct {
	Action      string
	Package     string
	ImportPath  string
	Test        string
	Output      string
	Elapsed     float64
	FailedBuild string
}

// testParse folds go test -json events into counts, failures and the
// tests that skipped for MK_LINUX.
type testParse struct {
	counts   TestCounts
	outputs  map[string][]string // key: package + " " + test; the last lines
	linuxAt  map[string]bool
	timingAt map[string]bool
	running  map[string]bool
	cached   map[string]bool
	pkgFail  map[string]bool // a test of the package failed
	buildOut map[string][]string
	linux    map[string]map[string]bool // package to top-level tests
	results  map[string]string          // "pkg test" of a top-level test: pass, skip or fail
	reported map[string]bool
	// timeout is the -timeout a package's test binary reported when it
	// timed out ("1h0m0s", from its panic or go test's kill line), and
	// header that panic's first line and its "running tests:" lines,
	// taken as they arrive because the goroutine dump after them is
	// longer than a test's kept lines; inRunning: still reading those.
	timeout   map[string]string
	header    map[string][]string
	inRunning map[string]bool
	// paused are the started tests waiting in t.Parallel ("pause" without
	// "cont"): their output says nothing about why a package ended.
	paused map[string]bool
	// ended maps a package that ended before every started test had a
	// result to why: "timed out after 1h0m0s", "ended mid-run" or "had
	// no result when go test ended".
	ended   map[string]string
	failed  []string
	failOut []string
	other   []string
}

func newTestParse() *testParse {
	return &testParse{outputs: map[string][]string{}, linuxAt: map[string]bool{}, timingAt: map[string]bool{},
		running: map[string]bool{}, cached: map[string]bool{}, pkgFail: map[string]bool{}, buildOut: map[string][]string{},
		linux: map[string]map[string]bool{}, results: map[string]string{}, reported: map[string]bool{},
		timeout: map[string]string{}, header: map[string][]string{}, inRunning: map[string]bool{}, paused: map[string]bool{}, ended: map[string]string{}}
}

// The testing package's alarm panic, printed as output of whichever test
// was running (or of the package), and go test's own kill line when the
// binary did not exit within a minute of its -timeout (cmd/go's
// testKillTimeout is -timeout plus one minute, the line prints it).
var (
	timedOutLine = regexp.MustCompile(`^panic: test timed out after (\S+)$`)
	killedLine   = regexp.MustCompile(`^\*\*\* Test killed.*ran too long \(([^)]+)\)`)
)

// killTimeout is the -timeout behind go test's kill line, which prints the
// kill time: that time less the minute cmd/go adds ("1m2s" gives "2s").
func killTimeout(kill string) string {
	d, err := time.ParseDuration(kill)
	if err != nil || d <= time.Minute {
		return kill + " (go test's kill time)"
	}
	return (d - time.Minute).String()
}

// noteTimeout reads one output line o of package pkg for the alarm panic
// and the "running tests:" list after it.
func (tp *testParse) noteTimeout(pkg, o string) {
	if m := timedOutLine.FindStringSubmatch(o); m != nil {
		tp.timeout[pkg] = m[1]
		tp.header[pkg] = append(tp.header[pkg], o)
		tp.inRunning[pkg] = true
		return
	}
	if m := killedLine.FindStringSubmatch(o); m != nil {
		if tp.timeout[pkg] == "" {
			tp.timeout[pkg] = killTimeout(m[1])
		}
		tp.header[pkg] = append(tp.header[pkg], o)
		return
	}
	if !tp.inRunning[pkg] {
		return
	}
	switch {
	case strings.TrimSpace(o) == "running tests:":
		tp.header[pkg] = append(tp.header[pkg], o)
	case strings.HasPrefix(o, "\t\t") && len(tp.header[pkg]) < 40:
		tp.header[pkg] = append(tp.header[pkg], o)
	case strings.TrimSpace(o) == "" || !strings.HasPrefix(o, "\t"):
		tp.inRunning[pkg] = false
	}
}

const keepLines = 60

func (tp *testParse) line(l string) {
	var e testEvent
	if !strings.HasPrefix(l, "{") || json.Unmarshal([]byte(l), &e) != nil {
		fmt.Printf("   %s\n", l)
		tp.other = append(tp.other, l)
		if len(tp.other) > 400 {
			tp.other = tp.other[len(tp.other)-200:]
		}
		return
	}
	pkg := normPkg(e.Package)
	key := pkg + " " + e.Test
	switch e.Action {
	case "build-output":
		tp.buildOut[e.ImportPath] = append(tp.buildOut[e.ImportPath], strings.TrimRight(e.Output, "\n"))
	case "run":
		tp.running[key] = true
	case "pause":
		tp.paused[key] = true
	case "cont":
		delete(tp.paused, key)
	case "output":
		o := strings.TrimRight(e.Output, "\n")
		if ls := append(tp.outputs[key], o); len(ls) > keepLines {
			tp.outputs[key] = ls[len(ls)-keepLines:]
		} else {
			tp.outputs[key] = ls
		}
		if strings.Contains(o, linuxOff) {
			tp.linuxAt[key] = true
		}
		if strings.Contains(o, timingOff) {
			tp.timingAt[key] = true
		}
		if strings.Contains(o, guardHit) || strings.Contains(o, lockHit) {
			tp.counts.GuardHits = append(tp.counts.GuardHits, key+": "+strings.TrimSpace(o))
		}
		if e.Test == "" && strings.Contains(o, "(cached)") {
			tp.cached[pkg] = true
		}
		tp.noteTimeout(pkg, o)
	case "pass", "skip", "fail":
		if e.Test == "" {
			tp.pkgDone(pkg, e)
			return
		}
		delete(tp.running, key)
		delete(tp.paused, key)
		if !strings.Contains(e.Test, "/") {
			tp.results[key] = e.Action
		}
		switch e.Action {
		case "pass":
			tp.counts.Pass++
			if tp.timingAt[key] {
				tp.counts.TimingLogged++
			}
		case "skip":
			tp.counts.Skip++
			if tp.linuxAt[key] {
				tp.counts.LinuxSkips++
				top, _, _ := strings.Cut(e.Test, "/")
				if tp.linux[pkg] == nil {
					tp.linux[pkg] = map[string]bool{}
				}
				tp.linux[pkg][top] = true
			}
			if tp.timingAt[key] {
				tp.counts.TimingSkips++
			}
		case "fail":
			tp.counts.Fail++
			tp.pkgFail[pkg] = true
			tp.failed = append(tp.failed, key)
			tp.addFailOut(key, tp.outputs[key])
			fmt.Printf("   --- FAIL: %s (%.1fs)\n", key, e.Elapsed)
		}
		delete(tp.outputs, key)
		delete(tp.linuxAt, key)
		delete(tp.timingAt, key)
	}
}

// pkgDone ends package pkg: its result line, and a failure that no test
// accounts for (a build failure, a panic, a timeout) by its own name.
func (tp *testParse) pkgDone(pkg string, e testEvent) {
	tp.reported[pkg] = true
	tp.counts.Packages++
	key := pkg + " "
	switch {
	case e.Action == "fail":
		tp.counts.PackagesFailed++
		var stuck []string
		for k := range tp.running {
			if strings.HasPrefix(k, key) {
				stuck = append(stuck, k)
				delete(tp.running, k)
			}
		}
		sort.Strings(stuck)
		if len(stuck) > 0 || tp.timeout[pkg] != "" {
			why := "ended mid-run"
			if d := tp.timeout[pkg]; d != "" {
				why = "timed out after " + d
			}
			tp.endedEntry(pkg, why, stuck, "started tests without a result")
		}
		if e.FailedBuild != "" {
			tp.failed = append(tp.failed, pkg+" [build failed]")
			tp.addFailOut(pkg+" [build failed]", tp.buildOut[e.FailedBuild])
		} else if !tp.pkgFail[pkg] && len(stuck) == 0 && tp.timeout[pkg] == "" {
			tp.failed = append(tp.failed, pkg+" [package failed]")
			tp.addFailOut(pkg+" [package failed]", tp.outputs[key])
		}
		fmt.Printf("   FAIL %s (%.1fs)\n", pkg, e.Elapsed)
	case tp.cached[pkg]:
		tp.counts.PackagesCached++
		fmt.Printf("   ok   %s (cached)\n", pkg)
	case e.Action == "skip":
		fmt.Printf("   --   %s (no tests)\n", pkg)
	default:
		fmt.Printf("   ok   %s %.1fs\n", pkg, e.Elapsed)
	}
	delete(tp.outputs, key)
}

// endedEntry records that package pkg ended before its started tests
// (stuck, "pkg test" keys) had a result, as one failure naming the package
// and why: "./std [timed out after 1h0m0s; running TestX (17s); 257
// started tests without a result]". Its output is the alarm's header and
// the tests' names; ended keeps why for the steps that read the results
// Without a timeout the running tests are the stuck ones not
// paused in t.Parallel, and the output also keeps their last lines and
// the package's own, where a panic in another goroutine, log.Fatal or
// os.Exit leaves its reason.
func (tp *testParse) endedEntry(pkg, why string, stuck []string, what string) {
	tp.ended[pkg] = why
	var running, active []string
	for _, h := range tp.header[pkg] {
		if strings.HasPrefix(h, "\t\t") {
			running = append(running, strings.TrimSpace(h))
		}
	}
	if tp.timeout[pkg] == "" {
		for _, k := range stuck {
			if !tp.paused[k] {
				active = append(active, k)
				running = append(running, strings.TrimPrefix(k, pkg+" "))
			}
		}
	}
	parts := []string{why}
	if n := len(running); n > 0 {
		r := "running " + strings.Join(running[:min(n, 3)], ", ")
		if n > 3 {
			r += fmt.Sprintf(" and %d more", n-3)
		}
		parts = append(parts, r)
	}
	if len(stuck) > 0 {
		parts = append(parts, fmt.Sprintf("%d %s", len(stuck), what))
	}
	entry := pkg + " [" + strings.Join(parts, "; ") + "]"
	tp.failed = append(tp.failed, entry)
	lines := slices.Clone(tp.header[pkg])
	if len(stuck) > 0 {
		lines = append(lines, fmt.Sprintf("%d %s:", len(stuck), what))
	}
	for i, k := range stuck {
		if i == 60 {
			lines = append(lines, fmt.Sprintf("  ... %d more", len(stuck)-i))
			break
		}
		lines = append(lines, "  "+strings.TrimPrefix(k, pkg+" "))
	}
	for i, k := range active {
		if i == 5 {
			break
		}
		if out := tp.outputs[k]; len(out) > 0 {
			lines = append(lines, "--- "+strings.TrimPrefix(k, pkg+" ")+", its last lines:")
			lines = append(lines, tail(out, 20)...)
		}
	}
	if tp.timeout[pkg] == "" {
		if out := tp.outputs[pkg+" "]; len(out) > 0 {
			lines = append(lines, "--- the package's own lines:")
			lines = append(lines, tail(out, 20)...)
		}
	}
	for _, k := range stuck {
		delete(tp.outputs, k)
		delete(tp.paused, k)
	}
	if len(tp.failOut) < 80 {
		tp.failOut = append(tp.failOut, entry+":\n"+strings.Join(lines, "\n"))
	}
}

// addFailOut keeps a failure's last lines for the report (at most 80
// failures, 40 lines each).
func (tp *testParse) addFailOut(name string, lines []string) {
	if len(tp.failOut) >= 80 {
		return
	}
	tp.failOut = append(tp.failOut, name+":\n"+strings.Join(tail(lines, 40), "\n"))
}

// finish reports the tests still running when the output ended, one entry
// per package.
func (tp *testParse) finish() {
	byPkg := map[string][]string{}
	for k := range tp.running {
		pkg, _, _ := strings.Cut(k, " ")
		byPkg[pkg] = append(byPkg[pkg], k)
	}
	pkgs := make([]string, 0, len(byPkg))
	for p := range byPkg {
		pkgs = append(pkgs, p)
	}
	sort.Strings(pkgs)
	for _, p := range pkgs {
		stuck := byPkg[p]
		sort.Strings(stuck)
		why := "had no result when go test ended"
		if d := tp.timeout[p]; d != "" {
			why = "timed out after " + d + " and had no result when go test ended"
		}
		tp.endedEntry(p, why, stuck, "started tests without a result")
	}
}
