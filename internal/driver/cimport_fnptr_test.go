// Why: C headers declare function pointers and extern variables, and both
// must bind to Mk types a program can use safely.
// What: TestCImport_FnPointersAndExternVars.
// How: builds and runs a package whose C import declares a function pointer
// field and result and an extern int, and checks what Mk reads and writes.

package driver

import "testing"

// TestCImport_FnPointersAndExternVars: a C function pointer in a struct field and a result
// binds as an optional Mk fn type (NULL is none), and `extern int k_count;`
// binds as an extern var, read in unsafe, where it sees a write C makes; its
// address, taken in unsafe, is a raw pointer (*i32) as in its own module.
func TestCImport_FnPointersAndExternVars(t *testing.T) {
	needTools(t)
	h := "typedef struct CB { int (*f)(int); } CB;\nextern int k_count;\nCB k_cb(void);\nint (*k_none(void))(int);\nvoid k_set(int v);\n"
	c := "#include \"k.h\"\nint k_count = 41;\nstatic int add1(int x) { return x + 1; }\n" +
		"CB k_cb(void) { CB c; c.f = add1; return c; }\nint (*k_none(void))(int) { return 0; }\nvoid k_set(int v) { k_count = v; }\n"
	dir := writeTree(t, cBin("fp", h, c,
		cMain("", "        cb := k.k_cb()\n        f := cb.f else { return }\n        a := f(1)\n"+
			"        b := if k.k_none().IsNone() { 7 } else { 0 }\n"+
			"        n := KCountRead()\n        k.k_set(100)\n        r := KCountRead()\n        _ = KCountAddr()\n",
			"{a} {b} {n} {r}", "a: a, b: b, n: n, r: r")+
			"\nKCountRead(): i32 {\n    unsafe \"an extern var\" {\n        return k.k_count\n    }\n}\n"+
			"\nKCountAddr(): *i32 {\n    unsafe \"an extern var's address\" {\n        return &k.k_count\n    }\n}\n"))
	if out := buildRun(t, dir, t.TempDir()); out != "2 7 41 100\n" {
		t.Errorf("printed %q, want 2 7 41 100", out)
	}
}
