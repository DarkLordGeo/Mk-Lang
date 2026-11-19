// Why: the conformance units that import std.sync (model/H33.mk,
// X.SharedClone.mk and others) need sync.Shared declared as the reference says.
// What: TestSharedDeclaredAsReference and TestSharedUnitsPass.
// How: matches sync/mem_shared.mk against doc/reference.md's std.sync block, then
// runs mk corpus on each unit (sharedUnits), with and without a planted bug.

package std

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// sharedFile is the one std.sync file that may hold unsafe.
const sharedFile = "sync/mem_shared.mk"

// TestSharedDeclaredAsReference: every doc/reference.md line under
// "// std.sync." that names Shared is declared, word for word with the
// export mark, in sync/mem_shared.mk; Shared is a linear view struct; and
// pkg.mk lists sync among the unsafe modules.
func TestSharedDeclaredAsReference(t *testing.T) {
	ref := readText(t, "../doc/reference.md")
	i := strings.Index(ref, "// std.sync. Shared below is sync.Shared\n")
	if i < 0 {
		t.Fatal("the reference has no std.sync block")
	}
	src := readText(t, sharedFile)
	var n int
	for _, l := range strings.Split(ref[i:], "\n")[1:] {
		if l == "" {
			break
		}
		if !strings.HasPrefix(l, "Shared[T].") {
			continue
		}
		n++
		if !strings.Contains(src, "\nexport "+l+" {\n") {
			t.Errorf("%s does not declare %q", sharedFile, "export "+l)
		}
	}
	if n != 4 {
		t.Errorf("the reference lists %d Shared lines, want 4 (New, Clone, Ref, Free)", n)
	}
	if !strings.Contains(src, "\nexport linear view struct Shared[T] {\n") {
		t.Errorf("%s: Shared is not an exported linear view struct (3.2)", sharedFile)
	}
	if f, ok := manifestField(readText(t, "pkg.mk"), "unsafe_modules"); !ok || !strings.Contains(f, `"sync"`) {
		t.Error("pkg.mk: unsafe_modules does not list sync")
	}
}

// sharedUnits are the conformance units that import std.sync, with the
// result mk corpus must report for each.
var sharedUnits = []struct{ unit, want string }{
	{"tests/conformance/model/H33.mk", "tests/conformance/model/H33.mk ok PASS"},
	{"tests/conformance/model/X.SharedClone.mk", "tests/conformance/model/X.SharedClone.mk ok PASS"},
	{"tests/conformance/memory/H74.mk", "tests/conformance/memory/H74.mk reject E0301 PASS"},
	{"tests/conformance/rules/EV532_accept_heap_shared.mk", "tests/conformance/rules/EV532_accept_heap_shared.mk ok PASS"},
	{"tests/conformance/rules/EV532_reject_alloc_shared.mk", "tests/conformance/rules/EV532_reject_alloc_shared.mk reject E0301 PASS"},
	{"tests/conformance/codes/E0201_not_prelude.mk", "tests/conformance/codes/E0201_not_prelude.mk reject E0201 PASS"},
}

// mkCorpusUnit runs mk corpus --all -v over the one conformance unit,
// with MK_STD at stdDir, and returns its output.
func mkCorpusUnit(t *testing.T, stdDir, unit string) string {
	t.Helper()
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(mkExe(t), "corpus", "--all", "-v", "--root", root, "--only", unit+"*")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "MK_STD="+stdDir)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		if _, ok := err.(*exec.ExitError); !ok {
			t.Fatal(err)
		}
	}
	return strings.ReplaceAll(out.String(), "\r\n", "\n")
}

// TestSharedUnitsPass: every unit that imports std.sync passes mk
// corpus over std as it is, with no std diagnostic.
// Then the marker is planted back as an earlier `[0]T`, and H33.mk
// must fail with E0907: a zero-length array of T needs T's layout, so H33.mk's
// `Ring { next: Mutex[?sync.Shared[Ring]] }` is a layout cycle. So the
// pass is a judgement of Shared's layout, not a skipped unit.
func TestSharedUnitsPass(t *testing.T) {
	stdDir := copyStd(t, t.TempDir(), nil)
	for _, u := range sharedUnits {
		out := mkCorpusUnit(t, stdDir, u.unit)
		if !strings.Contains(out, "\n"+u.want+"\n") && !strings.HasPrefix(out, u.want+"\n") {
			t.Errorf("%s: want %q in mk corpus output:\n%s", u.unit, u.want, out)
		}
		if !stdClean.MatchString(out) {
			t.Errorf("%s: std is not judged with no std diagnostic:\n%s", u.unit, out)
		}
	}

	src := readText(t, sharedFile)
	const fixed, old = "    marker: [0]&T\n", "    marker: [0]T\n"
	if strings.Count(src, fixed) != 1 {
		t.Fatalf("%s: the marker field %q is not found once", sharedFile, fixed)
	}
	planted := copyStd(t, t.TempDir(), map[string]string{sharedFile: strings.Replace(src, fixed, old, 1)})
	out := mkCorpusUnit(t, planted, sharedUnits[0].unit)
	if strings.Contains(out, sharedUnits[0].want) || !strings.Contains(out, "E0907") {
		t.Errorf("with marker [0]T planted, want H33 to fail on E0907, got:\n%s", out)
	}
}

// stdClean is mk corpus's std line when std draws no diagnostic, through
// whichever phase the pipeline reaches.
var stdClean = regexp.MustCompile(`(?m)^std: units judged through \w+; 0 diagnostics;`)
