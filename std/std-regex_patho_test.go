// Why: std.regex promises time linear in the input, even on patterns that
// make a backtracking matcher take exponential time.
// What: rxPathoCases, including patterns past the DFA's budget (the Pike
// VM), run through Find and FindAll on inputs of n and 8n bytes.
// How: results always checked; the time ratio asserted only under
// MK_TIMING=1 (timing_test.go), and logged otherwise.

package std

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// rxPatho runs args[1] over args[3] copies of the text args[2] (cut to
// args[3] bytes): with args[4] "find" it prints Find's match, or - for
// none; with "all" FindAll's count and its last match.
const rxPatho = `import std.regex

export Main(sys: &System): regex.Error|ParseError|OutOfMemory|WriteError!void {
    var out := sys.Stdout()
    n := Cast[int](try sys.args[3].ParseInt())
    fill := sys.args[2].AsBytes()
    buf := try sys.arena.Slice[u8](n)
    for i in 0..n {
        buf[i] = fill[i % fill.Len()]
    }
    s := str.From(buf[..]).Must()
    re := try regex.Compile(sys.arena, sys.args[1])
    if sys.args[4] == "all" {
        all := try re.FindAll(sys.arena, s)
        c := all.Len()
        if c == 0 {
            try Print(mut out, "0 -\n")
        } else {
            m := all[c - 1]
            lo := m.lo
            hi := m.hi
            try Print(mut out, "{c} {lo} {hi}\n", (c, lo, hi))
        }
        return
    }
    if m := re.Find(s) {
        lo := m.lo
        hi := m.hi
        try Print(mut out, "{lo} {hi}\n", (lo, hi))
    } else {
        try Print(mut out, "-\n")
    }
}
`

// rxPathoCases: pattern, fill text, mode, base size n, the expected output
// as a function of the size, and linear: the work grows with the input
// (every case but the one that matches at once), so the 8n run may take
// at most 32 times the n run under MK_TIMING. The Pike-only cases
// (`(a|b)*a(a|b){12}` needs 8,192 DFA states, past the 4,096 budget) run
// on smaller inputs, since the Pike VM does m steps a byte.
var rxPathoCases = []struct {
	pat, fill, mode string
	n               int
	want            func(n int) string
	linear          bool
}{
	{`(a*)*b`, "a", "find", 8_000_000, func(int) string { return "-" }, true},
	{`(x+x+)+y`, "x", "find", 8_000_000, func(int) string { return "-" }, true},
	{`(a|aa)*c`, "a", "find", 8_000_000, func(int) string { return "-" }, true},
	{`(?:a?){30}a{30}`, "a", "find", 8_000_000, func(n int) string { return "0 60" }, false},
	{`(\w+\s?)*$`, "a", "find", 8_000_000, func(n int) string { return fmt.Sprintf("0 %d", n) }, true},
	// FindAll, and the Pike VM past the DFA's budget.
	{`(a*)*b`, "a", "all", 8_000_000, func(int) string { return "0 -" }, true},
	{`\pL+`, "ab ", "all", 4_800_000, func(n int) string { return fmt.Sprintf("%d %d %d", n/3, n-3, n-1) }, true},
	{`\pL+`, "a", "find", 8_000_000, func(n int) string { return fmt.Sprintf("0 %d", n) }, true},
	{`(a|b)*a(a|b){12}`, "a", "find", 1_000_000, func(n int) string { return fmt.Sprintf("0 %d", n) }, true},
	{`(a|b)*a(a|b){12}`, "ab ", "find", 1_000_000, func(int) string { return "-" }, true},
	{`(a|b)*a(a|b){12}`, "ab ", "all", 1_000_000, func(int) string { return "0 -" }, true},
	{`(a|b)*a(a|b){12}`, "aaaaaaaaaaaaaab ", "all", 1_000_000, func(n int) string { return fmt.Sprintf("%d %d %d", n/16, n-16, n-1) }, true},
}

// TestSTDREGEX_PathologicalLinear: each case on n and 8n bytes, in a
// release build. A backtracker would not finish the first three.
func TestSTDREGEX_PathologicalLinear(t *testing.T) {
	exe := regexBuild(t, rxPatho, "-release")
	for _, c := range rxPathoCases {
		var took [2]time.Duration
		for k, size := range []int{c.n, 8 * c.n} {
			start := time.Now()
			got := strings.TrimSpace(regexExec(t, exe, c.pat, c.fill, fmt.Sprint(size), c.mode))
			took[k] = time.Since(start)
			if want := c.want(size); got != want {
				t.Errorf("%s %s on %d bytes of %q: got %q, want %q", c.mode, c.pat, size, c.fill, got, want)
			}
		}
		ratio := float64(took[1]) / float64(took[0])
		t.Logf("%-4s %-18s %-6q n=%d: %v, 8n: %v, ratio %.2f", c.mode, c.pat, c.fill, c.n, took[0].Round(time.Millisecond), took[1].Round(time.Millisecond), ratio)
		if timingRun() && c.linear && ratio > 32 {
			t.Errorf("%s %s: time ratio %.2f at 8x the input, want at most 32 (linear is 8)", c.mode, c.pat, ratio)
		}
	}
}
