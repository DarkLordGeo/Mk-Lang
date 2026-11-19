// Why: a package's C source that uses __try/__except names __C_specific_handler,
// which a C runtime would supply; Mk links no C runtime.
// What: TestCSourceTryLinksAgainstStdLibc, on x86_64-windows.
// How: builds a package with such a C source and no handler of its own, links it
// against std.libc's handler, and runs it. Before std.libc exported the
// handler, the link failed with "undefined symbol: __C_specific_handler".

package std

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

var sehFiles = map[string]string{
	"pkg.mk": `const Package = pkg.Package(
    name: "seh",
    bins: [pkg.Bin(name: "seh", root: "app", link_script: "", cpu: "", subsystem: "console")],
    deps: [],
    c_sources: [pkg.CSource(path: "lib/seh.c", flags: [])],
    c_imports: [],
    asm_sources: [],
    targets: [],
    unsafe_modules: ["ext"],
    links: [],
)
`,
	"lib/seh.c": `/* Faults inside its own __try and returns 7 from the __except, and runs
   a __finally; it defines no language handler. */
__declspec(noinline) int seh_read(volatile int *p) { return *p; }

int seh_try(void) {
    int r = 0;
    __try {
        r = seh_read((volatile int *)16);
    } __except (1) {
        r = 7;
    }
    return r;
}

int seh_finally(void) {
    volatile int r = 1;
    __try {
        __try {
            r = seh_read((volatile int *)16);
        } __finally {
            r = r + 40;
        }
    } __except (1) {
        r = r + 1;
    }
    return r;
}
`,
	"ext/ext.mk": `extern seh_try(): i32
extern seh_finally(): i32

export Try(): i32 {
    unsafe "seh_try is the package's C; it handles its own fault" {
        return seh_try()
    }
}

export Finally(): i32 {
    unsafe "seh_finally is the package's C; it handles its own fault" {
        return seh_finally()
    }
}
`,
	"app/main.mk": `import ext

export Main(sys: &System): WriteError!void {
    try sys.Print("{t} {f}\n", (t: ext.Try(), f: ext.Finally()))
}
`,
}

func TestCSourceTryLinksAgainstStdLibc(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("__try is Windows'")
	}
	stdDir, err := filepath.Abs(".")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	for name, text := range sehFiles {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, profile := range [][]string{nil, {"--release"}} {
		exe := filepath.Join(dir, "seh.exe")
		args := append(append([]string{"build"}, profile...), "-o", exe, ".")
		out, status := mkCmd(t, dir, stdDir, args...)
		if status != 0 {
			if strings.Contains(out, "no usable LLVM") {
				t.Skipf("needs the LLVM toolchain: %s", out)
			}
			t.Fatalf("mk %v: exit %d\n%s", args, status, out)
		}
		// 7 from the __except; 1 + 40 from the __finally, then + 1 from the
		// outer __except, so the handler ran both kinds of scope.
		if got, code := cfaultRun(t, exe); code != 0 || got != "7 42\n" {
			t.Errorf("mk %v: exit %#x, output %q; want exit 0 and %q", args, uint32(code), got, "7 42\n")
		}
	}
}
