// Why: mk run must show the manifest step's notes, as mk check and mk build
// do, or a user misses why a workspace was ignored.
// What: TestRun_PrintsWalkNotes.
// How: runs a member under an ancestor workspace that lists other members and
// checks for the note. Skips without clang.

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRun_PrintsWalkNotes(t *testing.T) {
	if _, err := os.Stat(`C:\Program Files\LLVM\bin\clang.exe`); err != nil && os.Getenv("MK_LLVM") == "" {
		if _, err := exec.LookPath("clang"); err != nil {
			t.Skip("no clang")
		}
	}
	ws := t.TempDir()
	files := map[string]string{
		// An ancestor workspace that lists other members: ignored, with a note.
		"pkg.mk":      "const Package = pkg.Package(\n    name: \"outer\",\n    members: [\"other\"],\n)\n",
		"app/pkg.mk":  "const Package = pkg.Package(\n    name: \"app\",\n    bins: [pkg.Bin(name: \"app\", root: \".\", subsystem: \"console\")],\n)\n",
		"app/main.mk": "export Main(sys: &System): WriteError!void {\n    var out := sys.Stdout()\n    try Print(mut out, \"hi\\n\", ())\n}\n",
	}
	for name, text := range files {
		p := filepath.Join(ws, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	st, out, errOut := mk("run", filepath.Join(ws, "app"))
	if st != 0 || out != "hi\n" || !strings.Contains(errOut, "note: ") || !strings.Contains(errOut, "does not list this package") {
		t.Errorf("mk run: status %d stdout %q stderr %q", st, out, errOut)
	}
}
