// Why: runtime.Blocking's state is consumed on success and stays with the
// caller on Canceled, as Chan.Send's x does; the checker must agree.
// What: TestRt_BlockingStateEdges (std/runtime/tests_runtime.mk runs both edges).
// How: mk check wants E0401 for a use after a successful Blocking, and a clean
// check when the error edge frees the state.

package std

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRt_BlockingStateEdges(t *testing.T) {
	root, stdDir := missStd(t)
	head := "import std.runtime\n\nGrow(xs: HeapList[int]): HeapList[int] {\n    return xs\n}\n\nexport Main(sys: &System) {\n    xs := HeapList[int].New(sys.heap.Alloc())\n    ys := runtime.Blocking(sys.io, xs, Grow) else {\n        xs.Free(sys.heap.Alloc())\n        return\n    }\n    ys.Free(sys.heap.Alloc())\n"
	for _, c := range []struct{ name, tail, want string }{
		{"error edge keeps it", "}\n", ""},
		{"use after success", "    xs.Free(sys.heap.Alloc())\n}\n", "E0401"},
	} {
		t.Run(c.name, func(t *testing.T) {
			name := strings.ReplaceAll(c.name, " ", "_") + ".mk"
			if err := os.WriteFile(filepath.Join(root, name), []byte(head+c.tail), 0o644); err != nil {
				t.Fatal(err)
			}
			out, status := mkCmd(t, root, stdDir, "check", name)
			if c.want == "" && status != 0 {
				t.Fatalf("mk check: status %d\n%s", status, out)
			}
			if c.want != "" && (status == 0 || !strings.Contains(out, name+":14:") || !strings.Contains(out, c.want)) {
				t.Errorf("mk check: status %d, want %s at line 14\n%s", status, c.want, out)
			}
		})
	}
}
