// Why: a C import may call Win32 functions that std's .def lists do not
// hold; the driver finds their DLL so the link succeeds.
// What: TestAddSystemLibs and TestCImport_SystemDLL.
// How: addSystemLibs reads the host's System32 export tables; the build
// test links a C import against import libraries made from them.

package driver

import (
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"mk/internal/diag"
	"mk/internal/types"
)

// TestAddSystemLibs: a name no std .def list holds goes to the system DLL
// that exports it, a std DLL's list gaining it; an unknown name stays out.
func TestAddSystemLibs(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("the system DLLs are read from the host's System32")
	}
	got := addSystemLibs([]importLib{{dll: "kernel32", names: []string{"ExitProcess"}}},
		[]string{"ExitProcess", "IsCharAlphaA", "MulDiv", "no_such_name"})
	want := []importLib{{dll: "kernel32", names: []string{"ExitProcess", "MulDiv"}}, {dll: "user32", names: []string{"IsCharAlphaA"}}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

// TestCImport_SystemDLL: on Windows, a C import's Win32 functions link
// through import libraries made from the system DLLs' export tables:
// MulDiv (kernel32, not in std's list) and IsCharAlphaA (user32). A C
// source links against std.libc only, so its call of lstrlenA fails
// at link, E0001.
func TestCImport_SystemDLL(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("the system DLLs are read from the host's System32")
	}
	tools := needTools(t)
	pkg := func(withC bool) map[string]string {
		over := map[string]string{"name": `"sysdll"`, "bins": `[pkg.Bin(name: "sysdll", root: "app", link_script: "", cpu: "", subsystem: "console")]`,
			"c_imports":      `[pkg.CImport(name: "w", headers: ["lib/w.h"], defines: [], macros: [], vendor: false)]`,
			"unsafe_modules": `["app"]`}
		n := ""
		if withC {
			over["c_sources"] = `[pkg.CSource(path: "lib/w.c", flags: [])]`
			n = "\n        n := w.hello_len()\n        try Print(mut out, \"{n}\\n\", (n: n))"
		}
		return map[string]string{
			"pkg.mk": manifest(over),
			"lib/w.h": "__declspec(dllimport) int __stdcall MulDiv(int a, int b, int c);\n" +
				"__declspec(dllimport) int __stdcall IsCharAlphaA(char ch);\nint hello_len(void);\n",
			"lib/w.c": "#include \"w.h\"\n__declspec(dllimport) int __stdcall lstrlenA(const char *s);\n" +
				"int hello_len(void) { return lstrlenA(\"hello\"); }\n",
			"app/main.mk": `import c.w

export Main(sys: &System): WriteError!void {
    var out := sys.Stdout()
    unsafe "Win32 calls are plain C" {
        m := w.MulDiv(6, 7, 1)
        var al := 0
        if w.IsCharAlphaA(0x61) != 0 {
            al = 1
        }
        try Print(mut out, "{m} {al}\n", (m: m, al: al))` + n + `
    }
}
`,
		}
	}
	build := func(withC bool) (Result, []string) {
		s, err := NewSession(Config{Root: writeTree(t, pkg(withC)), Target: types.X86_64Windows, Tools: tools, CacheDir: t.TempDir()})
		if err != nil {
			t.Fatal(err)
		}
		r, paths, err := s.Build(filepath.Join(t.TempDir(), "sysdll.exe"))
		if err != nil {
			t.Fatal(err)
		}
		return r, paths
	}
	r, paths := build(false)
	if len(r.Report.Diags) > 0 || len(paths) != 1 {
		t.Fatalf("build: %d paths\n%s", len(paths), r.Report.Text(r.Sources))
	}
	out, errOut, code := runExe(t, types.X86_64Windows, paths[0], "")
	if code != 0 || out != "42 1\n" {
		t.Errorf("exit %d, stdout %q, stderr %q; want 42 1", code, out, errOut)
	}
	r, _ = build(true)
	if len(r.Report.Diags) != 1 || r.Report.Diags[0].Diag.Code != diag.E0001 || !strings.Contains(r.Report.Text(r.Sources), "lstrlenA") {
		t.Errorf("a C source calling lstrlenA: want one E0001 naming it, got\n%s", r.Report.Text(r.Sources))
	}
}
