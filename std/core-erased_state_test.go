// Why: a write through ErasedCall.State's result must be a write into the
// state, or a Mutex state's guard could store a reference to a dead local.
// What: TestCore_ErasedStateWriteIsIntoState.
// How: std.core is internal, so the probe is a module of a std copy, imported
// by a one-line program; storing &n through State's guard must be E0505.
//
// The result borrows the state's own loan (`from self.anchor`). With `from
// self` it borrowed only the loan of the caller's ErasedCall local, and a
// built probe read a dead frame.

package std

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// erasedStateProbe is the audit's zzc2c, as module std.zzc2c: Stash erases
// Run's local Mutex[?&i32] with a function, takes the state back with
// State, and stores &n, its own local, through the guard. Stash returns
// and Run reads m: sound would refuse the store.
const erasedStateProbe = `import std.core

Nop(m: &Mutex[?&i32]) {
}

Stash(io: Io, m: &Mutex[?&i32]): int {
    ec := core.ErasedCall.Of[Mutex[?&i32], fn(&Mutex[?&i32])](m, Nop)
    st := ec.State[Mutex[?&i32], fn(&Mutex[?&i32])]()
    var n: i32 = 5
    var g := st.Lock(io) else {
        return -2
    }
    p := g.Mut()
    *p = some(&n)
    g.Unlock()
    return 0
}

Read(io: Io, m: &Mutex[?&i32]): i32 {
    var g := m.Lock(io) else {
        return -1
    }
    var v: i32 = -1
    if r := *g.Ref() {
        v = *r
    }
    g.Unlock()
    return v
}

export Run(io: Io): int {
    m := Mutex[?&i32].New(none)
    if Stash(io, &m) != 0 {
        return -2
    }
    return Cast[int](Read(io, &m))
}
`

// TestCore_ErasedStateWriteIsIntoState: the store of &n through State's
// guard is E0505 at the store (std/zzc2c/p.mk:14:15), and nothing else.
// It fails with core_conn.mk's State declared `&T from self` (the check
// is then clean), which shows that it pins the `from self.anchor` rule.
func TestCore_ErasedStateWriteIsIntoState(t *testing.T) {
	root := t.TempDir()
	stdDir := copyStd(t, root, map[string]string{"zzc2c/p.mk": erasedStateProbe})
	main := "import std.zzc2c\n\nexport Main(sys: &System) {\n    _ = zzc2c.Run(sys.io)\n}\n"
	if err := os.WriteFile(filepath.Join(root, "main.mk"), []byte(main), 0o644); err != nil {
		t.Fatal(err)
	}
	out, st := mkCmd(t, root, stdDir, "check", "main.mk")
	want := filepath.ToSlash(filepath.Join("std", "zzc2c", "p.mk")) + ":14:15: E0505"
	if st == 0 || !strings.Contains(filepath.ToSlash(out), want) || strings.Count(out, ": E") != 1 {
		t.Fatalf("mk check: exit %d, want 1 with one diagnostic, %q (a write through State's guard is a write into the state)\n%s", st, want, out)
	}
}
