// Why: try on a stored E!T value (a local or a parameter, not a call) must
// behave as try on a call in every build and at compile time.
// What: TestTryUnion_SameInEveryMode and its program tryValueMain.
// How: compares dev, release and compile-time results (internal/lower/errors.go
// tryUnion), with onerr running on the try's error exit.

package driver

import (
	"path/filepath"
	"strings"
	"testing"

	"mk/internal/types"
)

const tryValueMain = `
error Bad(code: int)

Get(n: int): Bad!int {
    if n < 0 {
        fail Bad(code: n)
    }
    return n * 2
}

Use(n: int): Bad!int {
    var r: Bad!int = Get(n)
    v := try r
    return v + 1
}

Count(n: int, hits: mut int): Bad!void {
    onerr { *hits += 100 }
    var r: Bad!int = Get(n)
    *hits += 1
    _ = try r
}

Probe(): int {
    a := Use(5) else {
        return -1
    }
    b := Use(-3) else e {
        return a * 1000 + e.code
    }
    return b
}

const Folded: int = Probe()

export Main(sys: &System): WriteError!void {
    rp := Probe()
    var hits := 0
    Count(4, mut hits) else {
    }
    Count(-1, mut hits) else {
    }
    try sys.Print("{rp} {Folded} {hits}\n", (rp, Folded, hits))
}
`

func TestTryUnion_SameInEveryMode(t *testing.T) {
	dir := writeTree(t, map[string]string{"main.mk": tryValueMain})
	// Use(5) = 11; Use(-3) fails with code -3, so Probe = 11*1000 - 3; the
	// second Count's try fails, so its onerr adds 100 to its one hit.
	const want = "10997 10997 102\n"
	for _, prof := range []types.Profile{types.Dev, types.Release} {
		exe := buildProgram(t, filepath.Join(dir, "main.mk"), types.X86_64Windows, prof, t.TempDir())
		out, errOut, code := runExe(t, types.X86_64Windows, exe, "")
		if got := strings.ReplaceAll(out, "\r\n", "\n"); code != 0 || got != want {
			t.Errorf("%v: exit %d, stdout %q, want %q\n%s", prof, code, got, want, errOut)
		}
	}
}
