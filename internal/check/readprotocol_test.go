// Why: incremental builds are right only if a stage-6 body records every
// read its result depends on.
// What: TestIncrReadProtocol.
// How: one case per event of the read-set protocol check owns, each naming
// the reads it must find in m.F's read set ("*" matches any key or name).
// Name lookups are resolve's; layouts, bodies and files are eval's and gen's.

package check

import (
	"strings"
	"testing"

	"mk/internal/types"
)

// bodyReads checks module m (over lib) and returns the reads of m.F.
func bodyReads(t *testing.T, lib, src string) []types.Read {
	t.Helper()
	w := newWorld(t, true)
	if lib != "" {
		lb := w.add("lib", opt{}, lib)
		// The driver evaluates lib's consts before m's stage 5 (EvalConst):
		// every const of lib is 3 here.
		for _, ii := range lb.info.Items {
			if d, ok := w.items.Get(ii.Item).Decl.(*types.ConstDecl); ok {
				d.Value, d.HasValue = w.c.intConst(3), true
			}
		}
	}
	b := w.add("m", opt{}, src)
	for _, id := range b.m.Units() {
		if w.items.Get(id).Key.String() == "m.F" {
			return w.c.Check(b.m, id).Reads.Sorted()
		}
	}
	t.Fatalf("no unit m.F")
	return nil
}

// TestIncrReadProtocol: one case per read-set event check owns; each
// names the reads it must find in m.F's read set ("*" matches any key or
// name).
func TestIncrReadProtocol(t *testing.T) {
	const lib = "export struct Q {\n\texport n: int\n}\n\nexport struct R {\n\texport n: int\n}\n\n" +
		"export const K: int = 3\n\nexport Make(): Q {\n\treturn Q(n: 1)\n}\n\n" +
		"export interface Shape {\n\tArea(&self): int\n}\n\nimpl Shape for Q {\n\tArea(&self): int {\n\t\treturn 1\n\t}\n}\n\n" +
		"export Area[T: Shape](x: &T): int {\n\treturn x.Area()\n}\n\n" +
		"export Twice[T: Plain](x: T): int {\n\ta := x\n\tb := x\n\treturn 2\n}\n"
	type want struct {
		k         types.ReadKind
		key, name string
	}
	for _, tc := range []struct {
		name  string
		src   string
		wants []want
	}{
		{"own signature", "F(): int {\n\treturn 1\n}\n", []want{{types.ReadSignature, "m.F", "*"}}},
		{"name resolves to an item", "import lib\n\nF(): int {\n\tq := lib.Make()\n\treturn q.n\n}\n",
			[]want{{types.ReadSignature, "lib.Make", "*"}}},
		{"foreign fields", "import lib\n\nF(q: lib.Q): int {\n\treturn q.n\n}\n", []want{{types.ReadFields, "lib.Q", ""}}},
		{"foreign construction", "import lib\n\nF(): int {\n\tq := lib.Q(n: 1)\n\treturn q.n\n}\n", []want{{types.ReadFields, "lib.Q", ""}}},
		{"own fields", "struct S {\n\tn: int\n}\n\nF(s: S): int {\n\treturn s.n\n}\n", []want{{types.ReadFields, "m.S", "all"}}},
		{"member not found", "import lib\n\nF(q: lib.Q): int {\n\treturn q.Missing\n}\n",
			[]want{{types.ReadNoItem, "lib.Q:", "Missing"}, {types.ReadExports, "lib.Q:", ""}}}, // iface.TypeNamespaceKey
		// A copy of q is borrow's use of its kind (borrow.Result.Reads); check
		// reads the shape when it judges a bound.
		{"shape used", "import lib\n\nF(q: lib.Q): int {\n\treturn lib.Twice(q)\n}\n", []want{{types.ReadShape, "lib.Q", "*"}}},
		// A task argument's Send judgement (sendable) reads the shape of its
		// type, also through a reference (an unsafe impl Send edit must reach
		// the spawning unit).
		{"shape of a task argument", "import lib\n\nPoke(q: lib.Q) {\n}\n\nF(s: &Scope, q: lib.Q) {\n\ts.go Poke(q)\n}\n",
			[]want{{types.ReadShape, "lib.Q", "*"}}},
		{"shape of a task argument by reference", "import lib\n\nPeek(q: &lib.R) {\n}\n\nF(s: &Scope, r: &lib.R) {\n\ts.go Peek(r)\n}\n",
			[]want{{types.ReadShape, "lib.R", "*"}}},
		{"const value in a type", "import lib\n\nF(): int {\n\tvar a: [lib.K]int\n\treturn a.Len()\n}\n", []want{{types.ReadValue, "lib.K", "*"}}},
		{"const value in an expression", "import lib\n\nF(): int {\n\treturn lib.K\n}\n", []want{{types.ReadValue, "lib.K", "*"}}},
		{"const value as a repeat count", "import lib\n\nF(): int {\n\ta := [0; lib.K]\n\treturn a.Len()\n}\n", []want{{types.ReadValue, "lib.K", "*"}}},
		{"impl used", "import lib\n\nF(q: lib.Q): int {\n\treturn lib.Area(&q)\n}\n", []want{{types.ReadImpl, "lib.Shape", "*"}}},
		{"impl absent", "import lib\n\nF(r: lib.R): int {\n\treturn lib.Area(&r)\n}\n", []want{{types.ReadNoImpl, "lib.Shape", "*"}}},
		{"target field", "F(): int {\n\tif Target.os == OS.Linux {\n\t\treturn 1\n\t}\n\treturn 0\n}\n", []want{{types.ReadTarget, "*", "os"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reads := bodyReads(t, lib, tc.src)
			var all []string
			for _, r := range reads {
				all = append(all, r.Kind.String()+" "+r.Key.String()+" "+r.Name)
			}
			for _, w := range tc.wants {
				found := false
				for _, r := range reads {
					if r.Kind == w.k && (w.key == "*" || r.Key.String() == w.key) && (w.name == "*" || r.Name == w.name) {
						found = true
					}
				}
				if !found {
					t.Errorf("no %v %s %q in\n%s", w.k, w.key, w.name, strings.Join(all, "\n"))
				}
			}
		})
	}
}
