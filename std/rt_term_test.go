// Why: a program that exits, panics or dies by a signal must restore the
// terminal exactly once per record (std/rt/term.mk's restore registry).
// What: TestRt_TermRestore (native) and TestRt_TermRestoreLinux (WSL), the
// RT8-Term- blocks of std/rt/tests_term.mk.
// How: each record's restore writes a mark to standard error; rt8TermWant
// says how often each mark must appear, and rt8TermCheck checks the order.

package std

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// rt8TermWant is each RT8-Term- block's marks and how often each must
// appear in standard error.
var rt8TermWant = map[string]map[string]int{
	"RT8-Term-Exit":    {"TERMX\n": 1},
	"RT8-Term-Panic":   {"TERMP\n": 1},
	"RT8-Term-Removed": {"TERMK\n": 1, "TERMR": 0},
	"RT8-Term-Alt":     {"TERMA\n": 1, "\x1b[?1049l": 1},
	"RT8-Term-Signal":  {"TERMS\n": 1},
	// Mouse capture off, once per record that has it on, before
	// the alternate screen is left (rt8TermCheck checks the order).
	"RT8-Term-Mouse":     {"TERMM\n": 1, rt8MouseOff: 1, "\x1b[?1049l": 1},
	"RT8-Term-MouseOnly": {"TERMO\n": 1, rt8MouseOff: 1},
	// A record held by an exit path that never finished is restored by the
	// exit path once its wait ends.
	"RT8-Term-HeldRestore": {"TERMQ\n": 1},
	// A slow, live exit path and the one that took its record over
	// both restore it, with the flags of the word; a claim after the latch
	// is refused (TERMN, TERMG never restored). A claim made before it is
	// waited for by the one exit path that ends the process (TERMY), and
	// one whose registrant stalls past the bound is skipped by it and
	// restored only by a later exit path, when there is one (a fatal
	// signal during the panic report, a console handler's thread beside
	// rt.Exit); Exit is that later path in the test (TERMV).
	// RT8-Term-Stress has no marks: its asserts are its verdict.
	"RT8-Term-SlowRestore":     {"TERMW\n": 2, rt8MouseOff: 2, "\x1b[?1049l": 2, "TERMN": 0},
	"RT8-Term-StaleFlag":       {"TERMF\n": 1, "TERMG": 0, "\x1b[?1049l": 0},
	"RT8-Term-HeldWrite":       {"TERMV\n": 1},
	"RT8-Term-HeldWriteOneWay": {"TERMY\n": 1},
	// The kitty pops, one per flag left pushed on each screen: the
	// alternate screen's before its leave, the main screen's after it
	// (rt8TermKittyOrder); a main-screen count clamped to 255; a takeover
	// of a record whose taker cleared its counts pops nothing more.
	"RT8-Term-Kitty":     {"TERMJ\n": 1, rt8MouseOff: 1, "\x1b[?1049l": 1, rt8KittyPop: 3},
	"RT8-Term-KittyMain": {"TERMU\n": 1, "\x1b[?1049l": 0, rt8KittyPop: 255},
	"RT8-Term-KittyHeld": {"TERMZ\n": 2, rt8KittyPop: 1},
	// The SIGCONT hook sets raw mode again (rt8RawAgain, a test
	// record's mark) once for the one Ready record whose raw flag is set,
	// and not after the latch (a continue already past that check puts the
	// saved mode back after its raw set: one more of each); the record owner's paths wait for a continue in
	// progress and the exit path only up to TermHoldNs; on Linux a real
	// SIGCONT runs the hook.
	"RT8-Term-Continue":       {"TERMC\n": 1, "TERMB\n": 1, "TERMD\n": 2, rt8RawAgain: 2},
	"RT8-Term-ContinueHeld":   {"TERMH\n": 1, "TERME": 0, rt8RawAgain: 0},
	"RT8-Term-ContinueSignal": {"TERMI\n": 1, rt8RawAgain: 1},
	// The exit path waits for a continue in progress, within TermHoldNs:
	// the continue's mark comes before the record's restore (rt8TermCheck).
	"RT8-Term-ContinueWait": {"TERMT\n": 1, "TERMHOOK\n": 1},
}

// rt8RawAgain is std/sys's TermRawTestMark.
const rt8RawAgain = "TERMRAW\n"

// rt8MouseOff is std/rt/term.mk's MouseOff.
const rt8MouseOff = "\x1b[?1004l\x1b[?1006l\x1b[?1002l\x1b[?1000l"

// rt8KittyPop is std/rt/term.mk's KittyPop.
const rt8KittyPop = "\x1b[<u"

// rt8TermKittyOrder is RT8-Term-Kitty's order: mouse capture off, one pop,
// the alternate screen left, two pops.
func rt8TermKittyOrder(t *testing.T, stderr string) {
	t.Helper()
	m, a := strings.Index(stderr, rt8MouseOff), strings.Index(stderr, "\x1b[?1049l")
	if m < 0 || a < 0 {
		return
	}
	before, after := strings.Count(stderr[m:a], rt8KittyPop), strings.Count(stderr[a:], rt8KittyPop)
	if strings.Count(stderr[:m], rt8KittyPop) != 0 || before != 1 || after != 2 {
		t.Errorf("want mouse off, 1 pop, the leave, 2 pops; got %d pops between and %d after\n%q", before, after, stderr)
	}
}

// rt8TermCheck counts block b's marks in stderr.
func rt8TermCheck(t *testing.T, b rtsBlock, stderr string) {
	t.Helper()
	for k, w := range rt8TermWant[strings.Fields(b.name)[0]] {
		if n := strings.Count(stderr, k); n != w {
			t.Errorf("%q appears %d times in standard error, want %d\n%s", k, n, w, stderr)
		}
	}
	if strings.Contains(b.name, "Panic") && strings.Index(stderr, "TERMP") > strings.Index(stderr, "panic") {
		t.Errorf("the restore must come before the panic report\n%s", stderr)
	}
	if m, a := strings.Index(stderr, rt8MouseOff), strings.Index(stderr, "\x1b[?1049l"); m >= 0 && a >= 0 && m > a {
		t.Errorf("mouse capture must be turned off before the alternate screen is left\n%q", stderr)
	}
	if strings.HasPrefix(b.name, "RT8-Term-Kitty ") {
		rt8TermKittyOrder(t, stderr)
	}
	if strings.HasPrefix(b.name, "RT8-Term-ContinueWait ") {
		if h, r := strings.Index(stderr, "TERMHOOK\n"), strings.Index(stderr, "TERMT\n"); h < 0 || r < 0 || h > r {
			t.Errorf("the continue's mark must come before the record's restore\n%q", stderr)
		}
	}
}

// TestRt_TermRestore runs the blocks natively. Each block registers records
// whose restore writes a mark to standard error and leaves by one exit path
// (rt.Exit, a panic); a record runs exactly once on that exit, a removed one
// never, and a record with the alternate screen on writes the leave sequence
// once; the fatal-signal path (a real SIGTERM on Linux, the console handler's
// path on Windows) restores once before the process ends by the signal. A
// record a slow exit path holds past TermHoldNs is restored by the path that
// takes it over and again by the slow one, so twice.
func TestRt_TermRestore(t *testing.T) {
	rtsNative(t)
	for _, profile := range []string{"dev", "release"} {
		exe, all := rtsBuild(t, profile)
		seen := 0
		for _, b := range all {
			if !strings.HasPrefix(b.name, "RT8-Term-") {
				continue
			}
			seen++
			b := b
			t.Run(fmt.Sprintf("%s/%s", profile, b.name), func(t *testing.T) {
				r := rtsRun(t, exe, b.index, 60*time.Second, rtsEnv(b)...)
				rtsJudge(t, b, r, "")
				rt8TermCheck(t, b, r.stderr)
			})
		}
		if seen != 22 {
			t.Fatalf("%d RT8-Term- blocks, want 22", seen)
		}
	}
}

// TestRt_TermRestoreLinux runs the RT8-Term- blocks on x86_64-linux, dev
// and release, in one wsl.exe client, each under `timeout -k 5 60`, and
// counts their marks as TestRt_TermRestore does: RT8-Term-Signal raises a
// real SIGTERM there, so its mark is the fatal-signal handler's.
func TestRt_TermRestoreLinux(t *testing.T) {
	linuxGate(t)
	rtsNeedWSL(t)
	for _, profile := range []string{"linux-dev", "linux-release"} {
		exe, all := rtsBuild(t, profile)
		dir := filepath.Dir(exe)
		var blocks []rtsBlock
		var sh strings.Builder
		sh.WriteString("#!/bin/sh\ncd \"$(dirname \"$0\")\"\n")
		for _, b := range all {
			if !strings.HasPrefix(b.name, "RT8-Term-") {
				continue
			}
			blocks = append(blocks, b)
			fmt.Fprintf(&sh, "%s timeout -k 5 60 ./%s %d 2>rt8term-%d.txt </dev/null; echo \"%d $?\"\n",
				strings.Join(rtsEnv(b), " "), filepath.Base(exe), b.index, b.index, b.index)
		}
		if len(blocks) != 22 {
			t.Fatalf("%d RT8-Term- blocks, want 22", len(blocks))
		}
		script := filepath.Join(dir, "rt8term.sh")
		if err := os.WriteFile(script, []byte(sh.String()), 0o755); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		wslGuard()
		cmd := exec.CommandContext(ctx, "wsl.exe", "-e", "sh", rtsWSLPath(script))
		cmd.WaitDelay = 10 * time.Second
		out, err := cmd.Output()
		cancel()
		if err != nil {
			t.Fatalf("wsl.exe: %v\n%s", err, out)
		}
		got := map[string]string{}
		for _, line := range strings.Split(strings.ReplaceAll(string(out), "\r\n", "\n"), "\n") {
			if f := strings.Fields(line); len(f) == 2 {
				got[f[0]] = f[1]
			}
		}
		for _, b := range blocks {
			b := b
			t.Run(fmt.Sprintf("%s/%s", profile, b.name), func(t *testing.T) {
				code, ok := got[strconv.Itoa(b.index)]
				if !ok {
					t.Fatal("no result line")
				}
				r := rtsResult{timeout: code == "124" || code == "137"}
				r.code, _ = strconv.Atoi(code)
				e, _ := os.ReadFile(filepath.Join(dir, fmt.Sprintf("rt8term-%d.txt", b.index)))
				r.stderr = string(e)
				rtsJudge(t, b, r, "")
				rt8TermCheck(t, b, r.stderr)
			})
		}
	}
}
