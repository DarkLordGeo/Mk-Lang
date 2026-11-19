// Why: std forwarders that state std.atomic's order clauses pass the order
// on to std.raw, so those clauses must imply std.raw's requires.
// What: TestAtomicClausesImplyRaw.
// How: checks the bridge stdForwardsRawOrder rests on (forwardreq.go): at
// each of the six orders, std.atomic's clauses (Order.CanLoad, CanStore,
// CanFence, CanModify in std/atomic/order.mk) imply std.raw's requires on
// the ordered forms (std/raw/raw.mk). The test pins both texts: a change to
// either fails here until this table and the implication are revisited.

package check

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAtomicClausesImplyRaw(t *testing.T) {
	read := func(p string) string {
		b, err := os.ReadFile(filepath.Join("..", "..", "std", filepath.FromSlash(p)))
		if err != nil {
			t.Fatal(err)
		}
		return strings.ReplaceAll(string(b), "\r\n", "\n")
	}
	order, raw := read("atomic/order.mk"), read("raw/raw.mk")
	// std.atomic's predicates, as order.mk writes their bodies.
	preds := map[string]struct {
		body string
		f    func(v int) bool
	}{
		"CanLoad":   {"return self.v != 2 && self.v != 3", func(v int) bool { return v != 2 && v != 3 }},
		"CanStore":  {"return self.v != 1 && self.v != 3", func(v int) bool { return v != 1 && v != 3 }},
		"CanFence":  {"return self.v != 0 && self.v != 5", func(v int) bool { return v != 0 && v != 5 }},
		"CanModify": {"return self.v != 5", func(v int) bool { return v != 5 }},
	}
	for name, p := range preds {
		i := strings.Index(order, "export Order."+name+"(self): bool {")
		if i < 0 || !strings.HasPrefix(strings.TrimSpace(order[i+len("export Order."+name+"(self): bool {"):]), p.body) {
			t.Errorf("order.mk's %s is not `%s`: revisit this test and stdForwardsRawOrder", name, p.body)
		}
	}
	// raw.mk's clauses, each with the std.atomic predicate its std
	// forwarders state (atomic_t.mk, order.mk's Fence).
	clauses := []struct {
		decl, clause, by string
		f                func(v int) bool
	}{
		{"LoadOrd(&self, const o: u8): T", "requires o == 0 || o == 1 || o == 4 || o == 5", "CanLoad", func(v int) bool { return v == 0 || v == 1 || v == 4 || v == 5 }},
		{"StoreOrd(&self, x: T, const o: u8)", "requires o == 0 || o == 2 || o == 4 || o == 5", "CanStore", func(v int) bool { return v == 0 || v == 2 || v == 4 || v == 5 }},
		{"SwapOrd(&self, x: T, const o: u8): T", "requires o <= 4", "CanModify", func(v int) bool { return v <= 4 }},
		{"AddOrd(&self, x: T, const o: u8): T", "requires o <= 4", "CanModify", func(v int) bool { return v <= 4 }},
		{"SubOrd(&self, x: T, const o: u8): T", "requires o <= 4", "CanModify", func(v int) bool { return v <= 4 }},
		{"AndOrd(&self, x: T, const o: u8): T", "requires o <= 4", "CanModify", func(v int) bool { return v <= 4 }},
		{"OrOrd(&self, x: T, const o: u8): T", "requires o <= 4", "CanModify", func(v int) bool { return v <= 4 }},
		{"XorOrd(&self, x: T, const o: u8): T", "requires o <= 4", "CanModify", func(v int) bool { return v <= 4 }},
		{"CasOrd(&self, old: T, new: T, const s: u8, const f: u8, const weak: bool): (ok: bool, value: T)", "requires s <= 4", "CanModify", func(v int) bool { return v <= 4 }},
		{"CasOrd(&self, old: T, new: T, const s: u8, const f: u8, const weak: bool): (ok: bool, value: T)\n    requires s <= 4", "requires f == 0 || f == 1 || f == 4", "CanLoad CanModify", func(v int) bool { return v == 0 || v == 1 || v == 4 }},
		{"FenceOrd(const o: u8)", "requires o >= 1 && o <= 4", "CanFence", func(v int) bool { return v >= 1 && v <= 4 }},
	}
	for _, c := range clauses {
		decl := "\n" + c.decl + "\n    " + c.clause + "\n"
		if !strings.Contains(raw, "export Atomic[T]."+strings.TrimPrefix(decl, "\n")) && !strings.Contains(raw, "\nexport "+strings.TrimPrefix(decl, "\n")) {
			t.Errorf("raw.mk does not declare %s with `%s`: revisit this test and stdForwardsRawOrder", strings.SplitN(c.decl, "(", 2)[0], c.clause)
			continue
		}
		for v := 0; v <= 5; v++ { // Order's six consts: no other value exists
			ok := true
			for _, by := range strings.Fields(c.by) {
				ok = ok && preds[by].f(v)
			}
			if ok && !c.f(v) {
				t.Errorf("%s at order %d: std.atomic's %s holds but raw's `%s` does not", c.decl, v, c.by, c.clause)
			}
		}
	}
	if !strings.Contains(order, "export const Unordered: Order = Order(v: 5)") {
		t.Error("order.mk has no Unordered of value 5")
	}
}
