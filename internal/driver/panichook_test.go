// Why: a hosted program may define MkPanicHook to see a panic before the
// runtime reports it, and the hook's call order and arguments are part of
// the runtime's contract.
// What: hookProgram and TestPanicHook_Hosted.
// How: builds a Windows program whose hook writes the PanicInfo through
// Win32, runs it, and matches stderr and the exit status 101.

package driver

import (
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"mk/internal/types"
)

// hookProgram defines the hosted MkPanicHook: it writes the
// PanicInfo it receives to stderr, through Win32 (the package's own
// unsafe module), then returns.
const hookProgram = `import std.runtime
import std.raw

extern GetStdHandle(n: u32): *u8
extern WriteFile(h: *u8, buf: *u8, n: u32, done: *u32, ov: *u8): i32

Say(s: str) {
    var done: u32 = 0
    unsafe "a console write of s's bytes" {
        _ = WriteFile(GetStdHandle(0xFFFFFFF4), raw.StrData(s), Trunc[u32](s.Len()), raw.Of(&done), raw.Null[u8]())
    }
}

export extern MkPanicHook(info: &runtime.PanicInfo) {
    var buf: [32]u8 = [0; 32]
    Say("hook ")
    Say(info.message)
    Say(" ")
    Say(info.file)
    if info.code == 2 && info.line == 30 && info.column == 9 {
        Say(" code 2 at 30:9")
    }
    Say("\n")
    _ = buf
}

export Main(sys: &System): void {
    xs := [1, 2, 3]
    i := sys.args.Len() + 5
    _ = xs[i]
}
`

// TestPanicHook_Hosted: a hosted program's MkPanicHook is called once,
// before the report, with the panic's code, message and site; then the
// report is written and the process exits 101. A program without one gets
// the plain report (the support pack's weak default returns at once): every
// other panic test.
func TestPanicHook_Hosted(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("SKIP: the hook writes through Win32; linux.sh runs the Linux leg")
	}
	tools := needTools(t)
	root := writeTree(t, map[string]string{
		"pkg.mk": manifest(map[string]string{"name": `"hook"`,
			"bins":           `[pkg.Bin(name: "hook", root: ".", link_script: "", cpu: "", subsystem: "", kind: "", abi: "")]`,
			"unsafe_modules": `["."]`}),
		"main.mk": hookProgram,
	})
	for _, prof := range []types.Profile{types.Dev, types.Release} {
		s, err := NewSession(Config{Root: root, Target: types.X86_64Windows, Profile: prof, Tools: tools, CacheDir: t.TempDir()})
		if err != nil {
			t.Fatal(err)
		}
		r, paths, err := s.Build(filepath.Join(t.TempDir(), "hook.exe"))
		if err != nil {
			t.Fatal(err)
		}
		if len(r.Report.Diags) > 0 || len(paths) != 1 {
			t.Fatalf("build: %d paths\n%s", len(paths), r.Report.Text(r.Sources))
		}
		_, errOut, code := runExe(t, types.X86_64Windows, paths[0], "")
		want := regexp.MustCompile(`^hook bounds main\.mk code 2 at 30:9\npanic: bounds at main\.mk:30:9\n$`)
		if code != 101 || !want.MatchString(dropBacktrace(errOut)) || strings.Count(errOut, "hook ") != 1 {
			t.Errorf("%v: exit %d, stderr %q", prof, code, errOut)
		}
	}
}
