// Why: ChunksMut is std's parallel for (`for c in xs.ChunksMut(n) { s.go
// Work(c) }`), so its disjoint pieces must hold up when tasks run them at once.
// What: TestCoreNew_ChunksMutUnderScopeMatchesModel.
// How: each task maps x to x * k + j (j the piece's number), so a piece run
// twice, skipped or cut wrong changes the checksum a Go model computes serially.
//
// Built natively, dev and release, and run at one carrier and at sixteen
// (three runs each), so the pieces really run in parallel.

package std

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const chunksScopeProgram = `// ChunksMut pieces handed to tasks under io.Scope.

Mix(x: u64): u64 {
    var z := x
    z = (z ^ (z >> 30)) *% 0xbf58476d1ce4e5b9
    z = (z ^ (z >> 27)) *% 0x94d049bb133111eb
    return z ^ (z >> 31)
}

Scale(c: mut []u64, k: u64, j: u64) {
    for e in mut c {
        *e = *e *% k +% j
    }
}

ScaleAll(io: Io, xs: mut HeapList[u64], n: int, k: u64) {
    scope s := io.Scope() {
        var j: u64 = 0
        for piece in xs.ChunksMut(n) {
            s.go Scale(piece, k, j)
            j += 1
        }
    }
}

export Main(sys: &System): OutOfMemory|ParseError|WriteError!void {
    n := Cast[int](try sys.args[1].ParseInt())
    piece := Cast[int](try sys.args[2].ParseInt())
    rounds := Cast[int](try sys.args[3].ParseInt())
    a := sys.heap.Alloc()
    var xs := HeapList[u64].New(a)
    onerr xs.Free(a)
    var st: u64 = 1
    for _ in 0..n {
        st = st +% 0x9e3779b97f4a7c15
        try xs.Push(a, Mix(st))
    }
    for r in 0..rounds {
        ScaleAll(sys.io, mut xs, piece, Cast[u64](r) * 2 + 3)
    }
    var sum: u64 = 0
    for i in 0..xs.Len() {
        sum = sum +% xs[i] *% Cast[u64](i + 1)
    }
    xs.Free(a)
    var out := sys.Stdout()
    try Print(mut out, "{sum}\n", (sum))
}
`

func chunksMix(z uint64) uint64 {
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
	z = (z ^ (z >> 27)) * 0x94d049bb133111eb
	return z ^ (z >> 31)
}

// chunksScopeModel is the program's checksum, computed serially.
func chunksScopeModel(n, piece, rounds int) string {
	xs := make([]uint64, n)
	st := uint64(1)
	for i := range xs {
		st += 0x9e3779b97f4a7c15
		xs[i] = chunksMix(st)
	}
	for r := 0; r < rounds; r++ {
		k := uint64(r)*2 + 3
		for i := range xs {
			xs[i] = xs[i]*k + uint64(i/piece)
		}
	}
	var sum uint64
	for i, x := range xs {
		sum += x * uint64(i+1)
	}
	return fmt.Sprintf("%d\n", sum)
}

// runCarriers runs exe with MK_CARRIERS set, under runTimeout.
func runCarriers(t *testing.T, exe, carriers string, args ...string) nativeRun {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), runTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, args...)
	cmd.Dir = filepath.Dir(exe)
	cmd.Env = append(os.Environ(), "MK_CARRIERS="+carriers)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	r := nativeRun{stdout: strings.ReplaceAll(out.String(), "\r\n", "\n"), stderr: errb.String()}
	if ctx.Err() != nil {
		t.Fatalf("%s: killed after %s", filepath.Base(exe), runTimeout)
	}
	if ee, ok := err.(*exec.ExitError); ok {
		r.code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestCoreNew_ChunksMutUnderScopeMatchesModel(t *testing.T) {
	t.Parallel()
	cases := []struct{ n, piece, rounds int }{
		{100003, 1000, 20}, // 101 pieces, the last one 3 elements
		{10007, 1, 3},      // one task per element
		{5, 1000, 2},       // one piece, shorter than n asks
		{0, 7, 2},          // no piece at all
	}
	for _, prof := range [][]string{nil, {"--release"}} {
		exe := buildNative(t, nil, chunksScopeProgram, prof...)
		for _, c := range cases {
			want := chunksScopeModel(c.n, c.piece, c.rounds)
			for _, carriers := range []string{"1", "16", "16", "16"} {
				r := runCarriers(t, exe, carriers, strconv.Itoa(c.n), strconv.Itoa(c.piece), strconv.Itoa(c.rounds))
				if r.code != 0 || r.stdout != want {
					t.Fatalf("%v n %d piece %d rounds %d carriers %s: exit %d, got %q, want %q\nstderr: %s",
						prof, c.n, c.piece, c.rounds, carriers, r.code, r.stdout, want, r.stderr)
				}
			}
		}
	}
}
