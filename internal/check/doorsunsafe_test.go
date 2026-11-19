// Why: the runtime doors (std.vm, std.linux, fault filters, foreign I/O,
// custom allocators, raw byte allocation) bypass Mk's safety, so they are
// callable only in `unsafe`, as fs.Map is.
// What: TestDoorsNeedUnsafe and TestAllocBytesNeedUnsafe.
// How: every exported function and method of std.vm and std.linux,
// runtime.AddFaultFilter and runtime.ForeignIo, mem.Custom, and std.core's
// Alloc.Bytes and Alloc.FreeBytes, called or named as a value outside
// unsafe, is E1101 with the operation named; inside unsafe it checks clean;
// other items of those modules, and a user module of the same name, are not
// caught.

package check

import (
	"testing"

	"mk/internal/diag"
)

// doorsStubs are the surfaces the cases need, each a stand-in with the
// reference's shape where it matters (names, owners, exports).
var doorsStubs = []struct{ path, src string }{
	{"std.vm", "export struct Region {\n\tbase: uint\n}\n\n" +
		"export Reserve(size: int, align: int): Region {\n\treturn Region(base: 0)\n}\n\n" +
		"export Region.Commit(&self, offset: int, len: int): int {\n\treturn len\n}\n\n" +
		"export PageSize(): int {\n\treturn 4096\n}\n\n" +
		"Helper(): int {\n\treturn PageSize()\n}\n"},
	{"std.linux", "export Syscall(nr: uint, a0: uint, a1: uint, a2: uint, a3: uint, a4: uint, a5: uint): int {\n\treturn 0\n}\n"},
	{"std.runtime", "export AddFaultFilter(f: fn(int): int): int {\n\treturn 0\n}\n\n" +
		"export ForeignIo(ctx: *u8): int {\n\treturn 0\n}\n\n" +
		"export ForeignContext(x: int): int {\n\treturn x\n}\n\n" +
		"export Processors(): int {\n\treturn 1\n}\n"},
	{"std.mem", "export Custom(ctx: *u8): int {\n\treturn 0\n}\n\n" +
		"export Counting(): int {\n\treturn 0\n}\n"},
}

func doorsWorld(t *testing.T, std bool) *world {
	t.Helper()
	w := newWorld(t, true)
	for _, s := range doorsStubs {
		w.add(s.path, opt{std: std}, s.src).wantClean(t)
	}
	return w
}

const doorsImports = "import std.vm\nimport std.linux\nimport std.runtime\nimport std.mem\n\n"

func TestDoorsNeedUnsafe(t *testing.T) {
	for _, tc := range []struct{ name, src, want, op string }{
		{"vm function", "F(): int {\n\treturn vm.PageSize()\n}\n", "E1101@7:9", "vm.PageSize"},
		{"vm method", "F(r: &vm.Region): int {\n\treturn r.Commit(0, 4096)\n}\n", "E1101@7:9", "vm.Region.Commit"},
		{"vm in unsafe", "F(r: &vm.Region): int {\n\tunsafe \"the region is reserved\" {\n\t\treturn r.Commit(0, 4096) + vm.PageSize()\n\t}\n}\n", "", ""},
		{"vm named as a value", "F(): int {\n\tg := vm.PageSize\n\treturn g()\n}\n", "E1101@7:7", "vm.PageSize"},
		{"linux.Syscall", "F(): int {\n\treturn linux.Syscall(39, 0, 0, 0, 0, 0, 0)\n}\n", "E1101@7:9", "linux.Syscall"},
		{"linux.Syscall in unsafe", "F(): int {\n\tunsafe \"getpid reads nothing\" {\n\t\treturn linux.Syscall(39, 0, 0, 0, 0, 0, 0)\n\t}\n}\n", "", ""},
		{"runtime.AddFaultFilter", "G(x: int): int {\n\treturn x\n}\n\nF(): int {\n\treturn runtime.AddFaultFilter(G)\n}\n", "E1101@11:9", "runtime.AddFaultFilter"},
		{"runtime.ForeignIo", "F(p: *u8): int {\n\treturn runtime.ForeignIo(p)\n}\n", "E1101@7:9", "runtime.ForeignIo"},
		{"runtime.ForeignContext", "F(): int {\n\treturn runtime.ForeignContext(1)\n}\n", "E1101@7:9", "runtime.ForeignContext"},
		{"runtime.Processors is safe", "F(): int {\n\treturn runtime.Processors()\n}\n", "", ""},
		{"mem.Custom", "F(p: *u8): int {\n\treturn mem.Custom(p)\n}\n", "E1101@7:9", "mem.Custom"},
		{"mem.Custom named", "F(): int {\n\tg := mem.Custom\n\treturn 0\n}\n", "E1101@7:7", "mem.Custom"},
		{"mem.Counting is safe", "F(): int {\n\treturn mem.Counting()\n}\n", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := doorsWorld(t, true)
			b := checkBody(t, w, tc.want, doorsImports+tc.src)
			for _, d := range b.bodyDiags(t) {
				if d.Code == diag.E1101 && fact(d, "operation") != tc.op {
					t.Errorf("E1101 operation %q, want %q", fact(d, "operation"), tc.op)
				}
			}
		})
	}
	t.Run("user modules of the same names", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t, true)
		for _, s := range doorsStubs {
			w.add(s.path[len("std."):], opt{}, s.src).wantClean(t)
		}
		checkBody(t, w, "", "import vm\nimport linux\nimport runtime\nimport mem\n\nF(p: *u8): int {\n\treturn vm.PageSize() + linux.Syscall(39, 0, 0, 0, 0, 0, 0) + runtime.ForeignIo(p) + mem.Custom(p)\n}\n")
	})
}

// TestAllocBytesNeedUnsafe: std.core's Alloc.Bytes and FreeBytes, on
// the prelude's Alloc, are unsafe only; another Alloc method is not.
func TestAllocBytesNeedUnsafe(t *testing.T) {
	core := fakePrelude() + "\nexport Alloc.Bytes(&self, n: int, align: int): *u8 {\n\treturn raw.Null[u8]()\n}\n\n" +
		"export Alloc.FreeBytes(&self, p: *u8, n: int, align: int) {\n}\n\n" +
		"export Alloc.Owner(&self): int {\n\treturn 0\n}\n"
	for _, tc := range []struct{ name, src, want, op string }{
		{"Bytes", "F(a: &Alloc): *u8 {\n\treturn a.Bytes(16, 8)\n}\n", "E1101@2:9", "Alloc.Bytes"},
		{"FreeBytes", "F(a: &Alloc, p: *u8) {\n\ta.FreeBytes(p, 16, 8)\n}\n", "E1101@2:2", "Alloc.FreeBytes"},
		{"in unsafe", "F(a: &Alloc): *u8 {\n\tunsafe \"the caller frees it with FreeBytes\" {\n\t\tp := a.Bytes(16, 8)\n\t\ta.FreeBytes(p, 16, 8)\n\t\treturn p\n\t}\n}\n", "", ""},
		{"another method", "F(a: &Alloc): int {\n\treturn a.Owner()\n}\n", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := newWorldCore(t, true, core)
			b := checkBody(t, w, tc.want, tc.src)
			for _, d := range b.bodyDiags(t) {
				if d.Code == diag.E1101 && fact(d, "operation") != tc.op {
					t.Errorf("E1101 operation %q, want %q", fact(d, "operation"), tc.op)
				}
			}
		})
	}
}
