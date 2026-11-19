// Why: whether std's Linux layer uses the system libc is a compile-time fact,
// so only the branch the mode takes may be collected.
// What: TestLibcBuildFolds (libcfold.go).
// How: every call of std.sys.LibcBuild() folds to Options.LibcMode; the test
// collects with each mode and checks which callees exist.

package mono

import (
	"testing"

	"mk/internal/mir"
	"mk/internal/types"
)

const libcSrc = `
LibcBuild(): bool

PthreadStart(): int { return 1 }
CloneStart(): int { return 2 }

ThreadStart(): int {
    if LibcBuild() {
        return PthreadStart()
    }
    return CloneStart()
}

Main(): int {
    return ThreadStart()
}
`

// TestLibcBuildFolds: in each mode ThreadStart calls only its own
// branch's start and no longer calls LibcBuild; the other start is not
// collected.
func TestLibcBuildFolds(t *testing.T) {
	k := keyLibcBuild
	keyLibcBuild = types.ItemKey("m", "LibcBuild")
	t.Cleanup(func() { keyLibcBuild = k })
	for _, libc := range []bool{false, true} {
		sw := newSrcWorldAt(t, "m", true, libcSrc)
		in, ds := Collect(sw, []Root{{Kind: RootMain, Item: sw.item(t, "Main")}}, Options{Target: types.X86_64Linux, Validate: true, LibcMode: libc})
		if len(ds) > 0 {
			sw.fail(t, "collect", ds)
		}
		want, gone := "CloneStart", "PthreadStart"
		if libc {
			want, gone = gone, want
		}
		if n := len(instancesOf(in, sw.item(t, want))); n != 1 {
			t.Errorf("libc %v: %s has %d instances, want 1", libc, want, n)
		}
		if n := len(instancesOf(in, sw.item(t, gone))); n != 0 {
			t.Errorf("libc %v: %s has %d instances, want 0 (the folded branch)", libc, gone, n)
		}
		st := instancesOf(in, sw.item(t, "ThreadStart"))
		if len(st) != 1 {
			t.Fatalf("libc %v: ThreadStart has %d instances", libc, len(st))
		}
		calls := 0
		for bi := range st[0].Body.Blocks {
			if tm := &st[0].Body.Blocks[bi].Term; tm.Kind == mir.TermCall {
				calls++
				if tm.Callee.Fn.Item == sw.item(t, "LibcBuild") {
					t.Errorf("libc %v: ThreadStart still calls LibcBuild", libc)
				}
			}
		}
		if calls != 1 {
			t.Errorf("libc %v: ThreadStart makes %d calls, want 1", libc, calls)
		}
	}
}
