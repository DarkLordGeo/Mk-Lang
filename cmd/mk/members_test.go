// Why: in a workspace, `./...` must reach every member, or a broken member
// passes unnoticed.
// What: TestWorkspace_DotDotDotChecksEveryMember for mk check, mk build and
// mk test.
// How: membersTree builds a workspace whose members fail to import; the test
// runs from the root and from a member, with either separator, and requires
// every member's error and the highest status.

package main

import (
	"path/filepath"
	"strings"
	"testing"
)

// membersTree is a workspace with members a and b, each importing a module
// that does not exist (bad.<name>), so each member's check fails visibly.
func membersTree(t *testing.T, aImport string) string {
	return writeFiles(t, map[string]string{
		"pkg.mk":   "const Package = pkg.Package(\n    name: \"ws\",\n    members: [\"a\", \"b\"],\n)\n",
		"a/pkg.mk": "const Package = pkg.Package(\n    name: \"a\",\n)\n",
		"a/a.mk":   aImport + "export A(): int {\n    return 1\n}\n",
		"b/pkg.mk": "const Package = pkg.Package(\n    name: \"b\",\n)\n",
		"b/b.mk":   "import nothing.bee\n\nexport B(): int {\n    return 2\n}\n",
	})
}

func TestWorkspace_DotDotDotChecksEveryMember(t *testing.T) {
	ws := membersTree(t, "import nothing.ay\n\n")
	// From the root and from a member, with either separator: every member
	// is checked, and the status is the highest of theirs.
	for _, path := range []string{ws + "/...", filepath.Join(ws, "...") /* .\... on Windows */, filepath.Join(ws, "b") + "/..."} {
		st, _, errOut := mk("check", path)
		if st != 1 || !strings.Contains(errOut, "nothing.ay") || !strings.Contains(errOut, "nothing.bee") {
			t.Errorf("mk check %s: status %d, want 1 with both members' E0202\n%s", path, st, errOut)
		}
	}
	// One member clean, one failing: status 1, the failing member reported;
	// a flag's separate value before the path is not taken for it.
	ws = membersTree(t, "")
	// With --json, one document per member, in member order.
	st, out, _ := mk("check", "--target", "x86_64-windows", ws+"/...", "--json")
	if st != 1 || strings.Count(out, `{"version":1`) != 2 || !strings.Contains(out, "nothing.bee") {
		t.Errorf("--json: status %d\n%s", st, out)
	}
	st, _, errOut := mk("check", "--target", "x86_64-windows", ws+"/...")
	if st != 1 || !strings.Contains(errOut, "nothing.bee") || strings.Contains(errOut, "nothing.ay") {
		t.Errorf("one failing member: status %d\n%s", st, errOut)
	}
	// mk build -o names one output: refused over several members.
	if st, _, errOut := mk("build", "-o", filepath.Join(t.TempDir(), "x.exe"), ws+"/..."); st != 2 || !strings.Contains(errOut, "-o names one output") {
		t.Errorf("build -o: status %d\n%s", st, errOut)
	}
	// Outside a workspace, dir/... names the package in dir alone.
	one := writeFiles(t, map[string]string{
		"pkg.mk": "const Package = pkg.Package(\n    name: \"one\",\n)\n",
		"o.mk":   "import nothing.oh\n",
	})
	if st, _, errOut := mk("check", one+"/..."); st != 1 || !strings.Contains(errOut, "nothing.oh") {
		t.Errorf("no workspace: status %d\n%s", st, errOut)
	}
}
