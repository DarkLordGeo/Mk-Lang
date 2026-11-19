// Why: a futex and WaitOnAddress compare 4 bytes, so Atomic[T].Wait and Wake
// must refuse any other width at compile time.
// What: TestRt_AtomicWaitWidth (std/atomic/tests_wait.mk runs the waits).
// How: mk check on each type: 4-byte types check clean, others are E0706 at the
// call (`requires SizeOf[T]() == 4`).

package std

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRt_AtomicWaitWidth(t *testing.T) {
	root, stdDir := missStd(t)
	for _, c := range []struct {
		name, typ, init string
		ok              bool
	}{
		{"u32", "u32", "0", true},
		{"i32", "i32", "-1", true},
		{"u64", "u64", "0", false},
		{"u8", "u8", "0", false},
		{"i16", "i16", "0", false},
		{"int", "int", "0", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			src := "import std.atomic\n\nexport Main(sys: &System) {\n    a := atomic.Atomic[" + c.typ + "].New(" + c.init + ")\n    a.Wake(false)\n    a.Wait(sys.io, 1) else {}\n}\n"
			name := "w_" + c.name + ".mk"
			if err := os.WriteFile(filepath.Join(root, name), []byte(src), 0o644); err != nil {
				t.Fatal(err)
			}
			out, status := mkCmd(t, root, stdDir, "check", name)
			if c.ok {
				if status != 0 {
					t.Fatalf("mk check %s: status %d\n%s", name, status, out)
				}
				return
			}
			if status == 0 || !strings.Contains(out, name+":5:") || !strings.Contains(out, name+":6:") ||
				!strings.Contains(out, "E0706") || !strings.Contains(out, "SizeOf[T]() == 4") {
				t.Errorf("mk check %s: status %d, want E0706 at lines 5 and 6 naming SizeOf[T]() == 4\n%s", name, status, out)
			}
		})
	}
}
