// Why: List and HeapList must check an index before they grow, or a bad index
// could allocate before it panics.
// What: TestStdColl_InsertRemoveBoundsPanic, and Slice[T].Swap out of range.
// How: HeapList runs over a heap whose every request panics with another
// message, so a growth before the bounds check shows.

package std

import (
	"bytes"
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const collBoundsCore = `import std.raw

// A heap whose every entry panics: reaching it is a test failure.
const CollPanicTable = AllocTable(alloc: CollPanicAlloc, resize: CollPanicResize, free: CollPanicFree)

CollPanicAlloc(ctx: *u8, size: int, align: int): *u8 {
    raw.Fail(99, "test heap reached")
    return ctx
}

CollPanicResize(ctx: *u8, p: *u8, old: int, new: int, align: int): bool {
    raw.Fail(99, "test heap reached")
    return false
}

CollPanicFree(ctx: *u8, p: *u8, size: int, align: int) {
    raw.Fail(99, "test heap reached")
}

export CollFixPanicHeap(): Heap {
    return MakeHeap(raw.Null[u8](), raw.Of(&CollPanicTable))
}
`

const collBoundsModule = `import std.core

export PanicHeap(): Heap {
    return core.CollFixPanicHeap()
}
`

// TestStdColl_InsertRemoveBoundsPanic: Insert(i) needs 0 <= i <= Len() and
// Remove(i) needs 0 <= i < Len(), else a bounds panic (code 2, exit 101) with
// the operation's message, before any growth. The HeapList cases run over a
// heap whose every entry panics with another message, so a growth before the
// check would show that message instead. Slice[T].Swap out of range panics the
// same way.
func TestStdColl_InsertRemoveBoundsPanic(t *testing.T) {
	t.Parallel()
	cases := []struct{ name, body, msg string }{
		{"list-insert-past-end", "var l := List[int].New(sys.arena)\n    try l.Push(1)\n    try l.Insert(2, 5)", "List.Insert: index out of range"},
		{"list-insert-negative", "var l := List[int].New(sys.arena)\n    try l.Insert(-1, 5)", "List.Insert: index out of range"},
		{"list-remove-at-len", "var l := List[int].New(sys.arena)\n    try l.Push(1)\n    _ = l.Remove(1)", "List.Remove: index out of range"},
		{"heaplist-insert-before-growth", "h := boundsfix.PanicHeap()\n    a := h.Alloc()\n    var l := HeapList[int].New(a)\n    l.Insert(a, 1, 5) else {\n        l.Free(a)\n        return\n    }\n    l.Free(a)", "HeapList.Insert: index out of range"},
		{"heaplist-insert-negative", "h := boundsfix.PanicHeap()\n    a := h.Alloc()\n    var l := HeapList[int].New(a)\n    l.Insert(a, -1, 5) else {\n        l.Free(a)\n        return\n    }\n    l.Free(a)", "HeapList.Insert: index out of range"},
		{"heaplist-remove-empty", "h := boundsfix.PanicHeap()\n    a := h.Alloc()\n    var l := HeapList[int].New(a)\n    _ = l.Remove(0)\n    l.Free(a)", "HeapList.Remove: index out of range"},
		{"slice-swap", "var xs: [3]int = [1, 2, 3]\n    xs.Swap(0, 3)", "index out of range"},
	}
	extra := map[string]string{"core/tests_collbounds.mk": collBoundsCore, "boundsfix/boundsfix.mk": collBoundsModule}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			src := ""
			if strings.Contains(c.body, "boundsfix.") {
				src = "import std.boundsfix\n\n"
			}
			src += "export Main(sys: &System): OutOfMemory|WriteError!void {\n    var out := sys.Stdout()\n    try Print(mut out, \"before\\n\", ())\n    try out.Flush()\n    " + c.body + "\n    try Print(mut out, \"after\\n\", ())\n}\n"
			exe := collBuildWith(t, extra, src)
			ctx, cancel := context.WithTimeout(context.Background(), collTimeout)
			defer cancel()
			cmd := exec.CommandContext(ctx, exe)
			cmd.Dir = filepath.Dir(exe)
			var out, errb bytes.Buffer
			cmd.Stdout, cmd.Stderr = &out, &errb
			err := cmd.Run()
			code := 0
			if ee, ok := err.(*exec.ExitError); ok {
				code = ee.ExitCode()
			} else if err != nil {
				t.Fatal(err)
			}
			if code != 101 || out.String() != "before\n" || !strings.Contains(errb.String(), c.msg) || strings.Contains(errb.String(), "test heap reached") {
				t.Fatalf("exit %d, stdout %q, stderr %q; want 101, \"before\\n\" and %q before any heap call", code, out.String(), errb.String(), c.msg)
			}
		})
	}
}
