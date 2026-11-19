// Why: an interface call in a non-generic function owned by an interface
// has no concrete callee to lower to, so check must reject it before lower
// meets it.
// What: TestIfaceSelf_NonGenericCallIsE0702.
// How: checks each shape and expects E0702 at the method name (expr_call.go
// ifaceSelfMethod, IfaceCallLowers), or a clean check for the shapes that
// build.

package check

import "testing"

// TestIfaceSelf_NonGenericCallIsE0702: the shapes that were an internal
// error in lower (`Shape.Double(&self)` calling `self.Area()`, uncalled or
// not) are E0702 at the method name; the shapes that built stay clean: a
// generic interface's function that calls its method, a function owned by
// Shape that calls no Shape method through self, one with no self, and one
// with generics of its own; another of the interface's functions called on
// self is E0702 as its methods are.
func TestIfaceSelf_NonGenericCallIsE0702(t *testing.T) {
	t.Parallel()
	const shape = "interface Shape {\n    Area(&self): int\n}\n\n"
	runBodyCases(t, []bodyCase{
		{"calls Area", shape + "Shape.Double(&self): int {\n    return self.Area() * 2\n}\n", "E0702@6:17"},
		{"generic", "interface Get[T] {\n    Value(&self): T\n}\n\nGet[T].Twice(&self): T {\n    return self.Value()\n}\n", ""},
		{"no call", shape + "Shape.Two(&self): int {\n    return 2\n}\n", ""},
		{"no self", shape + "Shape.Unit(): int {\n    return 1\n}\n", ""},
		// The interface's own functions called on self (also through a copy),
		// and a function with generics of its own, which builds.
		{"calls Two", shape + "Shape.Two(&self): int {\n    return 2\n}\n\nShape.Four(&self): int {\n    return self.Two() * 2\n}\n", "E0702@10:17"},
		{"copy calls Two", shape + "Shape.Two(&self): int {\n    return 2\n}\n\nShape.Cp3(&self): int {\n    x := self\n    return x.Two()\n}\n", "E0702@11:14"},
		{"own generics", shape + "Shape.Scale[U](&self, u: U): int {\n    return self.Area()\n}\n", ""},
	})
}
