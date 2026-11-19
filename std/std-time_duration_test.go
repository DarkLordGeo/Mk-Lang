// Why: Duration.Format (std/core/core_duration.mk) must print what Go's
// Duration.String prints, the oracle for every duration.
// What: TestStdTime_DurationFormatAgainstGo over every edge and random durations.
// How: one Mk program formats each duration read from stdin; the tree's
// core_duration.mk is laid over the snapshot MK_TIME_BASE names, which may predate it.

package std

import (
	"fmt"
	"math"
	"math/rand/v2"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// time2DurationProg prints the Format of each duration on stdin, one a
// line.
const time2DurationProg = `import std.time

export Main(sys: &System): WriteError|ReadError|OutOfMemory!void {
    var out := sys.Stdout()
    var inp := sys.Stdin()
    for {
        var a := try Arena.New(sys.heap)
        defer a.Free()
        line := try inp.Line(&a) else {
            return
        }
        n := line.ParseInt() else { 0 }
        d := time.Nanos(n)
        try Print(mut out, "{d}\n", (d))
    }
}
`

// time2CoreFile is this tree's std/core/core_duration.mk.
func time2CoreFile(t *testing.T) string {
	b, err := os.ReadFile("core/core_duration.mk")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestStdTime_DurationFormatAgainstGo(t *testing.T) {
	exe := time2Build(t, map[string]string{"core/core_duration.mk": time2CoreFile(t)}, time2DurationProg)
	r := rand.New(rand.NewPCG(31, 32))
	ds := []int64{0, 1, -1, 999, 1000, 1001, 999_999, 1_000_000, 1_500_000, 999_999_999, 1e9, -1e9, 1_500_000_000, 60e9, 3600e9, 3601e9, 86400e9, math.MaxInt64, math.MinInt64, math.MinInt64 + 1}
	for range 20000 {
		ds = append(ds, int64(r.Uint64())>>r.IntN(64))
		ds = append(ds, -(int64(r.Uint64()) >> r.IntN(64)))
	}
	var in strings.Builder
	for _, d := range ds {
		fmt.Fprintln(&in, d)
	}
	cmd := exec.Command(exe)
	cmd.Stdin = strings.NewReader(in.String())
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(strings.ReplaceAll(string(out), "\r\n", "\n"), "\n"), "\n")
	if len(lines) != len(ds) {
		t.Fatalf("%d answers for %d durations", len(lines), len(ds))
	}
	for i, d := range ds {
		if want := time.Duration(d).String(); lines[i] != want {
			t.Errorf("Format(%d) = %q, Go %q", d, lines[i], want)
		}
	}
}
