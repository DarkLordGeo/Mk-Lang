// Why: Unordered (order 5, LLVM's unordered) is a load or store order only, so
// a wrapper that forwards its const order to a read-modify-write must say so.
// What: TestRt_ForwardedModifyOrder over Swap, FetchAdd, FetchXor and
// CompareExchange wrappers (std/atomic/tests_order5.mk covers Unordered itself).
// How: without `requires o.CanModify()` the wrapper is E0706 at the inner call;
// with it, every C++20 order checks clean.

package std

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRt_ForwardedModifyOrder(t *testing.T) {
	root, stdDir := missStd(t)
	type wrapper struct{ name, decl, call string }
	ws := []wrapper{
		{"swap", "Sw(a: &atomic.Atomic[i64], const o: atomic.Order): i64%s {\n    return a.Swap(7, o)\n}\n", "_ = Sw(&a, atomic.%s)"},
		{"fetch add", "Fa(a: &atomic.Atomic[i64], const o: atomic.Order): i64%s {\n    return a.FetchAdd(1, o)\n}\n", "_ = Fa(&a, atomic.%s)"},
		{"fetch xor", "Fx(a: &atomic.Atomic[i64], const o: atomic.Order): i64%s {\n    return a.FetchXor(0, o)\n}\n", "_ = Fx(&a, atomic.%s)"},
		{"cas success", "Cs(a: &atomic.Atomic[i64], const o: atomic.Order): bool%s {\n    r := a.CompareExchange(9, 9, o, atomic.Relaxed)\n    return r.ok\n}\n", "_ = Cs(&a, atomic.%s)"},
	}
	prog := func(w wrapper, clause string, calls []string) string {
		var b strings.Builder
		b.WriteString("import std.atomic\n\n")
		b.WriteString(fmt.Sprintf(w.decl, clause))
		b.WriteString("\nexport Main(sys: &System) {\n    a := atomic.Atomic[i64].New(5)\n")
		for _, c := range calls {
			b.WriteString("    " + c + "\n")
		}
		b.WriteString("}\n")
		return b.String()
	}
	check := func(t *testing.T, name, src string) (string, int) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
		out, status := mkCmd(t, root, stdDir, "check", name)
		if strings.Contains(out, "E0000") || strings.Contains(out, "internal compiler error") {
			t.Fatalf("mk check %s: an internal compiler error\n%s\n%s", name, src, out)
		}
		return out, status
	}
	all := []string{"Relaxed", "Acquire", "Release", "AcqRel", "SeqCst"}
	for _, w := range ws {
		tag := strings.ReplaceAll(w.name, " ", "_")
		t.Run(w.name+"/not restated", func(t *testing.T) {
			name := "fwd_" + tag + ".mk"
			out, status := check(t, name, prog(w, "", []string{fmt.Sprintf(w.call, "Relaxed")}))
			if status == 0 || !strings.Contains(out, name+":4:") || !strings.Contains(out, "E0706") || !strings.Contains(out, "o.CanModify()") {
				t.Errorf("mk check %s: status %d, want E0706 at the inner call naming o.CanModify()\n%s", name, status, out)
			}
		})
		t.Run(w.name+"/restated", func(t *testing.T) {
			var calls []string
			for _, o := range all {
				calls = append(calls, fmt.Sprintf(w.call, o))
			}
			name := "good_" + tag + ".mk"
			if out, status := check(t, name, prog(w, "\n    requires o.CanModify()", calls)); status != 0 {
				t.Fatalf("mk check %s: status %d\n%s", name, status, out)
			}
		})
	}
}
