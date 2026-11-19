// Why: on pinned carriers (runtime.Thread, runtime.Dedicated) a C fault that C
// handles with its own __try must reach C's handler, as on a shared carrier.
// What: TestRt_CFaultOwnHandlerPinned, Windows only; TestRwRt_CFaultOwnHandler
// is the shared-carrier twin.
// How: the C function must return 7 from its __except when called from a
// Thread's f, its tasks, and tasks under Dedicated.
//
// A pinned carrier sits at a record index at or past the shared carrier
// count, so the in_c test (task.InExtern) must take every recorded index,
// not only those below ncarriers: before it did, the handler neither lowered
// the stale StackLimit nor stopped its walk at mk_call_system, and each such
// call ended in Mk's report and exit 101.

package std

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// rt10bCFaultApp is the package's bin: CFAULT_MODE "thread" calls ext.Try
// from a Thread's f and from 8 tasks f spawns on its Io (20 calls each);
// "dedicated" from 8 tasks spawned under a Dedicated Io (20 calls each).
const rt10bCFaultApp = `import ext
import std.atomic
import std.runtime

var Sum: atomic.AtomicInt = atomic.AtomicInt.New(0)

// RunG calls the C function that faults inside its own __try 20 times.
RunG() {
    for _ in 0..20 {
        _ = Sum.Add(i64(ext.Try()))
    }
}

// Body is the Thread's f: one call on its root task, then 8 tasks on its Io.
Body(io: Io) {
    _ = Sum.Add(i64(ext.Try()))
    scope s := io.Scope() {
        for _ in 0..8 {
            s.go RunG()
        }
    }
}

// JoinAll joins j, retrying a cancelled Join with a NoCancel Io.
JoinAll(j: runtime.Joined, io: Io) {
    j.Join(io) else {
        JoinAll(j, io.NoCancel())
    }
}

export Main(sys: &System): WriteError!void {
    var out := sys.Stdout()
    mode := sys.Env("CFAULT_MODE") else { "thread" }
    if mode == "dedicated" {
        d := runtime.Dedicated(sys.io)
        scope s := d.Scope() {
            for _ in 0..8 {
                s.go RunG()
            }
        }
    } else {
        JoinAll(runtime.Thread(sys.io, Body), sys.io)
    }
    try Print(mut out, "{m} {n}\n", (m: mode, n: Sum.Load()))
}
`

func TestRt_CFaultOwnHandlerPinned(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("__try and the vectored handler are Windows'")
	}
	stdDir, err := filepath.Abs(".")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	files := map[string]string{
		"pkg.mk": `const Package = pkg.Package(
    name: "cfaultpin",
    bins: [pkg.Bin(name: "cfaultpin", root: "app", link_script: "", cpu: "", subsystem: "console")],
    deps: [],
    c_sources: [pkg.CSource(path: "lib/cfault.c", flags: [])],
    c_imports: [],
    asm_sources: [],
    targets: [],
    unsafe_modules: ["ext"],
    links: [],
)
`,
		"lib/cfault.c": cfaultFiles["lib/cfault.c"],
		"ext/ext.mk":   cfaultFiles["ext/ext.mk"],
		"app/main.mk":  rt10bCFaultApp,
	}
	for name, text := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, profile := range [][]string{nil, {"--release"}} {
		name := "dev"
		if profile != nil {
			name = "release"
		}
		exe := filepath.Join(dir, "cfaultpin-"+name+".exe")
		args := append(append([]string{"build"}, profile...), "-o", exe, ".")
		out, status := mkCmd(t, dir, stdDir, args...)
		if status != 0 {
			if strings.Contains(out, "no usable LLVM") {
				t.Skipf("needs the LLVM toolchain: %s", out)
			}
			t.Fatalf("mk %v: exit %d\n%s", args, status, out)
		}
		for _, c := range []int{1, 16} {
			for _, mode := range []struct{ name, want string }{
				{"thread", "thread 1127\n"},
				{"dedicated", "dedicated 1120\n"},
			} {
				for run := 0; run < 3; run++ {
					out, code := cfaultRun(t, exe, "CFAULT_MODE="+mode.name, fmt.Sprintf("MK_CARRIERS=%d", c), fmt.Sprintf("MK_TEST_SEED=%d", run+1))
					if code != 0 || out != mode.want {
						t.Errorf("%s, %s, %d carriers, run %d: exit %#x, output %q; want exit 0 and %q", name, mode.name, c, run, uint32(code), out, mode.want)
					}
				}
			}
		}
	}
}
