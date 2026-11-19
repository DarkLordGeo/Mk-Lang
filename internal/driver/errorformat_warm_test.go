// Why: the compiler synthesizes Format for errors, and a synthesized item
// must cache like any other or warm builds drift from cold ones.
// What: TestErrorFormatColdWarm and TestElseOnValueRuns.
// How: a program that prints an error through the provided Format builds
// the same bytes cold and warm, and after an edit of the importer the warm
// build equals a cold build of the edited source (the synthesized impl and
// method go through the interface, front and function caches).

package driver

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"mk/internal/types"
)

func dg3Pkg(text string) map[string]string {
	return map[string]string{
		"pkg.mk":     manifest(map[string]string{"bins": `[pkg.Bin(name: "app", root: "app", link_script: "", cpu: "", subsystem: "console")]`}),
		"lib/lib.mk": "export error NotFound(id: i64, code: u8)\nexport error Denied\n\nexport Find(id: i64): NotFound | Denied!i64 {\n    if id < 0 {\n        fail Denied\n    }\n    fail NotFound(id: id, code: 7)\n}\n",
		"app/app.mk": "import lib\n\nexport Main(sys: &System): WriteError!void {\n    n := lib.Find(3) else e {\n        try sys.Print(\"" + text + " {e}\\n\", (e))\n        return\n    }\n    _ = n\n}\n",
	}
}

func TestErrorFormatColdWarm(t *testing.T) {
	t.Parallel()
	tools := needTools(t)
	dir := writeTree(t, dg3Pkg("lookup:"))
	out := t.TempDir()
	cfg := Config{Root: dir, Target: types.X86_64Windows, Tools: tools, CacheDir: filepath.Join(t.TempDir(), "cache")}
	_, cold, _ := incBuild(t, cfg, filepath.Join(out, "cold.exe"))
	_, warm, _ := incBuild(t, cfg, filepath.Join(out, "warm.exe"))
	if !bytes.Equal(cold, warm) {
		t.Error("warm executable differs from the cold one")
	}
	edited := dg3Pkg("find:")
	if err := os.WriteFile(filepath.Join(dir, "app", "app.mk"), []byte(edited["app/app.mk"]), 0o644); err != nil {
		t.Fatal(err)
	}
	_, edit, _ := incBuild(t, cfg, filepath.Join(out, "edit.exe"))
	fresh := Config{Root: writeTree(t, edited), Target: types.X86_64Windows, Tools: tools, CacheDir: filepath.Join(t.TempDir(), "c2")}
	_, freshExe, _ := incBuild(t, fresh, filepath.Join(out, "fresh.exe"))
	if !bytes.Equal(edit, freshExe) {
		t.Error("warm build after the edit differs from a cold build of the edited source")
	}
	if runtime.GOOS != "windows" {
		return
	}
	got, err := exec.Command(filepath.Join(out, "edit.exe")).CombinedOutput()
	if err != nil || strings.TrimSpace(string(got)) != "find: NotFound(id: 3, code: 7)" {
		t.Errorf("run: %v %q", err, got)
	}
}

// TestElseOnValueRuns: `else e` and a bare `else` on an E!T value that
// is no call (a parameter) take the right arm when run: 40 + 1 + 40 + 2.
func TestElseOnValueRuns(t *testing.T) {
	t.Parallel()
	needTools(t)
	src := "error Bad\n\nCode(r: Bad!int): int {\n    v := r else e {\n        _ = e\n        return 1\n    }\n    return v\n}\n\n" +
		"Code2(r: Bad!int): int {\n    v := r else {\n        return 2\n    }\n    return v\n}\n\n" +
		"Make(ok: bool): Bad!int {\n    if ok {\n        return 40\n    }\n    fail Bad\n}\n\n" +
		"export Main(sys: &System): WriteError!void {\n    a := Code(Make(true)) + Code(Make(false)) + Code2(Make(true)) + Code2(Make(false))\n    try sys.Print(\"{a}\", (a))\n}\n"
	file := filepath.Join(t.TempDir(), "main.mk")
	if err := os.WriteFile(file, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := NewSession(Config{Root: file, CacheDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	r, code, err := s.Run(nil, RunIO{Stdout: &out, Stderr: &out})
	if err != nil || len(r.Report.Diags) != 0 || code != 0 || out.String() != "83" {
		t.Errorf("run: %v exit %d %q\n%s", err, code, out.String(), diagLines(r))
	}
}
