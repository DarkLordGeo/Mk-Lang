// Why: a map made with NewKeyed under a random HashKey must iterate in an
// order no one can predict from another run.
// What: TestStdColl_HashKeyNewDiffersPerProcess.
// How: runs one program twice: the NewKeyed map under HashKey.New(sys.io)
// finds every key and changes order; New's order stays.

package std

import (
	"strings"
	"testing"
)

const collNewProgram = `// Order is the keys of m in iteration order, folded (FNV-1a).
Order(m: &Map[int, int]): u64 {
    var h: u64 = 14695981039346656037
    var it := m.Iter()
    for e := it.Next() {
        h = (h ^ Trunc[u64](*e.key)) *% 1099511628211
    }
    return h
}

export Main(sys: &System): WriteError|OutOfMemory|Canceled!void {
    var out := sys.Stdout()
    key: HashKey = try HashKey.New(sys.io)
    var m := Map[int, int].NewKeyed(sys.arena, key)
    var f := Map[int, int].New(sys.arena)
    for i in 0..2_000 {
        try m.Set(i * 7_919, i)
        try f.Set(i * 7_919, i)
    }
    var found := 0
    for i in 0..2_000 {
        k := i * 7_919
        if v := m.Get(&k) {
            if *v == i {
                found += 1
            }
        }
    }
    absent := 1
    missing := m.Get(&absent).IsNone()
    n := m.Len()
    a := Order(&m)
    b := Order(&f)
    try Print(mut out, "found {found} len {n} missing {missing}\nkeyed {a}\nfixed {b}\n", (found, n, missing, a, b))
}
`

func TestStdColl_HashKeyNewDiffersPerProcess(t *testing.T) {
	exe := collBuild(t, collNewProgram)
	type run struct{ keyed, fixed string }
	var runs []run
	for i := 0; i < 2; i++ {
		out := collExec(t, exe)
		lines := strings.Split(strings.TrimRight(strings.ReplaceAll(out, "\r\n", "\n"), "\n"), "\n")
		if len(lines) != 3 || lines[0] != "found 2000 len 2000 missing true" ||
			!strings.HasPrefix(lines[1], "keyed ") || !strings.HasPrefix(lines[2], "fixed ") {
			t.Fatalf("run %d: unexpected output:\n%s", i+1, out)
		}
		runs = append(runs, run{lines[1][len("keyed "):], lines[2][len("fixed "):]})
	}
	t.Logf("keyed orders %s %s; fixed order %s %s", runs[0].keyed, runs[1].keyed, runs[0].fixed, runs[1].fixed)
	if runs[0].fixed != runs[1].fixed {
		t.Fatalf("New's order differs between runs: %s, %s", runs[0].fixed, runs[1].fixed)
	}
	// Two 128-bit random keys giving one order of 2,000 keys, or a random
	// key giving the fixed key's order, has a negligible probability.
	if runs[0].keyed == runs[1].keyed {
		t.Fatalf("NewKeyed under HashKey.New gives one order in two runs: %s", runs[0].keyed)
	}
	if runs[0].keyed == runs[0].fixed || runs[1].keyed == runs[1].fixed {
		t.Fatalf("NewKeyed under HashKey.New orders as New")
	}
}
