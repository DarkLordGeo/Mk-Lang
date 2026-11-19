// Why: a release call given an allocator other than New's must panic before it
// frees anything.
// What: TestRwColl_OwnerCheckOfReleaseCalls: HeapDeque's and HeapBTree's
// FreeEmpty and FreeEach, and HeapBTree.Replace when it needs a node.
// How: each mode expects exit 101 with "wrong allocator" (code 12) and no
// output; a FreeEach release run before the check panics otherwise.

package std

import (
	"strings"
	"testing"
)

const rwCollOwnerProgram = `import std.coll

Owner(sys: &System, which: str) {
    h := sys.heap.Alloc()
    w := sys.arena.Alloc()
    if which == "HeapDeque.FreeEmpty" {
        q := coll.HeapDeque[int].New(h)
        q.FreeEmpty(w)
    } else if which == "HeapDeque.FreeEach" {
        var q := coll.HeapDeque[int].New(h)
        for i in 0..3 {
            q.Push(h, i) else {
            }
        }
        q.FreeEach(w, fn(x: int) { Assert(x < 0) })
    } else if which == "HeapBTree.FreeEmpty" {
        u := coll.HeapBTree[int, int].New(h)
        u.FreeEmpty(w)
    } else if which == "HeapBTree.FreeEach" {
        var u := coll.HeapBTree[int, int].New(h)
        for i in 0..3 {
            u.Set(h, i, i) else {
            }
        }
        u.FreeEach(w, fn(k: int, v: int) { Assert(k < 0 && v < 0) })
    } else if which == "HeapBTree.Replace" {
        var u := coll.HeapBTree[int, int].New(h)
        first := u.Replace(w, 1, 1) else {
            none
        }
        Assert(first.IsNone())
        u.Free(h)
    } else if which == "HeapBTree.Replace.present" {
        // A present key needs no node, so the allocator is not asked.
        var u := coll.HeapBTree[int, int].New(h)
        first := u.Replace(h, 1, 1) else {
            none
        }
        Assert(first.IsNone())
        old := u.Replace(w, 1, 2) else {
            none
        }
        Assert(old.Must() == 1)
        u.Free(h)
    }
}

export Main(sys: &System) {
    var out := sys.Stdout()
    which := sys.args[1]
    Print(mut out, "before\n") else {
        return
    }
    out.Flush() else {
        return
    }
    Owner(sys, which)
    Print(mut out, "after\n") else {
        return
    }
}
`

// TestRwColl_OwnerCheckOfReleaseCalls runs each mode at dev and release.
func TestRwColl_OwnerCheckOfReleaseCalls(t *testing.T) {
	t.Parallel()
	cases := []struct {
		mode string
		msg  string // "" means no panic: exit 0 and "after"
	}{
		{"HeapDeque.FreeEmpty", "wrong allocator: HeapDeque.FreeEmpty"},
		{"HeapDeque.FreeEach", "wrong allocator: HeapDeque.FreeEach"},
		{"HeapBTree.FreeEmpty", "wrong allocator: HeapBTree.FreeEmpty"},
		{"HeapBTree.FreeEach", "wrong allocator: HeapBTree.FreeEach"},
		{"HeapBTree.Replace", "wrong allocator: HeapBTree.Replace"},
		{"HeapBTree.Replace.present", ""},
	}
	for _, flags := range c2Flags {
		t.Run(c2FlagName(flags), func(t *testing.T) {
			t.Parallel()
			exe := collBuild(t, rwCollOwnerProgram, flags...)
			for _, c := range cases {
				code, stdout, stderr := collOwnerRun(t, exe, c.mode)
				if !strings.HasPrefix(stdout, "before\n") {
					t.Fatalf("%s: stdout %q, want it to start with before", c.mode, stdout)
				}
				if c.msg == "" {
					if code != 0 || !strings.Contains(stdout, "after") {
						t.Errorf("%s: exit %d, stdout %q, stderr %q; want exit 0 and after", c.mode, code, stdout, stderr)
					}
					continue
				}
				if code != 101 || !strings.Contains(stderr, "panic: ") || !strings.Contains(stderr, c.msg) ||
					strings.Contains(stderr, "assert") || strings.Contains(stdout, "after") {
					t.Errorf("%s: exit %d, stdout %q, stderr %q; want 101, no after, no assertion, and %q", c.mode, code, stdout, stderr, c.msg)
				}
			}
		})
	}
}
