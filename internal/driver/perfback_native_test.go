// Why: codegen loads a str's data pointer with !nonnull, so a str const
// with a null data word makes a path poison and lets LLVM delete a loop
// exit: `.Or("")` on a none would break System.Env's search and the same
// loop in user code on Linux release builds.
// What: envAbsentSrc and TestPerfBack_EnvAbsentLoopExits.
// How: builds and runs a program with two such searches on both targets, dev
// and release, and checks its exit status. check points an empty const at a
// zero-byte const, and codegen repairs or refuses a null nonnull word in any
// const (internal/codegen/nonnull.go, TestNonnullConstRepairsEmptyViews).

package driver

import (
	"os"
	"path/filepath"
	"testing"

	"mk/internal/types"
)

const envAbsentSrc = `// A search that returns none when nothing matches, whose result feeds a
// parameter the callee ignores outside Windows (rt-sched's FSource shape).
EnvDrop(io: Io, open: bool): int {
    if Target.os == Os.Windows && open {
        return 8
    }
    return 7
}

Find(xs: []str, k: str): ?str from xs {
    for v in xs {
        if v == k {
            return some(v)
        }
    }
    return none
}

export Main(sys: &System): int {
    a := EnvDrop(sys.io, (sys.Env("MK_PERFBACK_NO_SUCH_VARIABLE") else { "" }) == "open")
    b := EnvDrop(sys.io, (Find(sys.args, "--no-such-argument") else { "" }) == "open")
    var n := 0
    for _ in 0..1000 {
        if sys.Env("MK_PERFBACK_NO_SUCH_VARIABLE").IsSome() {
            n += 1
        }
    }
    return a + b + n
}
`

// TestPerfBack_EnvAbsentLoopExits: the program above exits 14 (7 + 7 + 0)
// on x86_64-windows and x86_64-linux, dev and release. With the null
// data word the Linux release build walked past the end of the environment and of the
// arguments until an unmapped page (exit 101, "invalid memory access").
func TestPerfBack_EnvAbsentLoopExits(t *testing.T) {
	for _, target := range []types.Target{types.X86_64Windows, types.X86_64Linux} {
		for _, prof := range []types.Profile{types.Dev, types.Release} {
			name := target.Name + "-dev"
			if prof == types.Release {
				name = target.Name + "-release"
			}
			t.Run(name, func(t *testing.T) {
				dir := t.TempDir()
				src := filepath.Join(dir, "envabsent.mk")
				if err := os.WriteFile(src, []byte(envAbsentSrc), 0o644); err != nil {
					t.Fatal(err)
				}
				exe := buildProgram(t, src, target, prof, dir)
				out, errOut, code := runExe(t, target, exe, "", "a", "b", "c")
				if code != 14 || out != "" {
					t.Errorf("exit %d, stdout %q, stderr %q; want exit 14", code, out, errOut)
				}
			})
		}
	}
}
