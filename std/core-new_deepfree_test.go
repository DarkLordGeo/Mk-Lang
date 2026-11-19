// Why: a HeapMap whose keys and values own memory must release each of them
// exactly once, on Set, Replace, Ensure and the map's deep Free.
// What: TestCoreNew_DeepFreeMatchesModel, against a Go model.
// How: seeded random operations allocate through a std.mem.Counting; every
// reply matches the model and no live byte is left after Free; dev and release.

package std

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
)

const coreDeepFreeProgram = `import std.mem

linear struct Key {
    id: int
    name: HeapText
}

impl Eq for Key {
    Eq(&self, other: &Key): bool {
        return self.id == other.id
    }
}

impl Hash for Key {
    Hash(&self, h: mut Hasher) {
        h.U64(Cast[u64](self.id))
    }
}

impl Free for Key {
    Free(self, a: &Alloc) {
        Key(id: _, name) := self
        name.Free(a)
    }
}

Next(s: mut u64): u64 {
    *s = *s *% 6364136223846793005 +% 1442695040888963407
    return *s >> 33
}

// Text is s followed by pad dashes, so lengths vary and the texts grow.
Text(a: &Alloc, s: str, pad: int): OutOfMemory!HeapText from a {
    var t := HeapText.New(a)
    onerr t.Free(a)
    try t.Set(a, s)
    for _ in 0..pad {
        try t.Push(a, "-")
    }
    return t
}

NewKey(a: &Alloc, id: int): OutOfMemory!Key from a {
    return Key(id: id, name: try Text(a, "key", id % 7))
}

// Put is Set (op 0), Replace (op 1) or Ensure (op 2) of id's key and the
// text v; Replace gives the old value, Ensure a copy of the stored one.
Put(a: &Alloc into m, m: mut HeapMap[Key, HeapText], id: int, v: str, op: u64): OutOfMemory!?HeapText from a, m {
    var k := try NewKey(a, id)
    onerr k.Free(a)
    var t := try Text(a, v, id % 13)
    onerr t.Free(a)
    if op == 0 {
        try m.Set(a, k, t)
        return none
    }
    if op == 1 {
        return try m.Replace(a, k, t)
    }
    got := try m.Ensure(a, k, t)
    return some(try Text(a, got.Str(), 0))
}

export Main(sys: &System): WriteError|OutOfMemory|ParseError!void {
    var out := sys.Stdout()
    n := int(try sys.args[1].ParseInt())
    var s := Trunc[u64](try sys.args[2].ParseInt())
    c := mem.Counting.New(sys.heap.Alloc())
    a := c.Alloc()
    var m := HeapMap[Key, HeapText].New(a)
    onerr m.Free(a)
    for i in 0..n {
        op := Next(mut s) % 3
        id := Trunc[int](Next(mut s) % 24)
        v := try sys.arena.Fmt("v{i}", (i))
        r := try Put(a, mut m, id, v, op)
        if t := r {
            onerr t.Free(a)
            try Print(mut out, "{op} {id} {v}\n", (op, id, v: t.Str()))
            t.Free(a)
        } else {
            try Print(mut out, "{op} {id} none\n", (op, id))
        }
    }
    try Print(mut out, "len {n}\n", (n: m.Len()))
    m.Free(a)
    try Print(mut out, "live {n}\n", (n: c.Live()))
}
`

// deepFreeModel replays the program's choices and returns its expected
// output: a Go map from id to the value text.
func deepFreeModel(n int, seed uint64) string {
	s := seed
	next := func() uint64 {
		s = s*6364136223846793005 + 1442695040888963407
		return s >> 33
	}
	m := map[int]string{}
	var b strings.Builder
	for i := 0; i < n; i++ {
		op := next() % 3
		id := int(next() % 24)
		v := fmt.Sprintf("v%d", i) + strings.Repeat("-", id%13)
		old, ok := m[id]
		switch op {
		case 0:
			m[id] = v
			fmt.Fprintf(&b, "0 %d none\n", id)
		case 1:
			m[id] = v
			if ok {
				fmt.Fprintf(&b, "1 %d %s\n", id, old)
			} else {
				fmt.Fprintf(&b, "1 %d none\n", id)
			}
		default:
			if !ok {
				m[id] = v
				old = v
			}
			fmt.Fprintf(&b, "2 %d %s\n", id, old)
		}
	}
	fmt.Fprintf(&b, "len %d\nlive 0\n", len(m))
	return b.String()
}

func TestCoreNew_DeepFreeMatchesModel(t *testing.T) {
	t.Parallel()
	for _, prof := range [][]string{nil, {"--release"}} {
		exe := buildNative(t, nil, coreDeepFreeProgram, prof...)
		for seed := uint64(1); seed <= 20; seed++ {
			n := int(50 + seed*37)
			r := runNative(t, exe, "", strconv.Itoa(n), strconv.FormatUint(seed, 10))
			want := deepFreeModel(n, seed)
			if r.code != 0 || r.stdout != want {
				t.Fatalf("%v seed %d n %d: exit %d\nstderr: %s\n%s", prof, seed, n, r.code, r.stderr, coreNewDiff(r.stdout, want))
			}
		}
	}
}

// coreNewDiff names the first line where got and want differ.
func coreNewDiff(got, want string) string {
	g, w := strings.Split(got, "\n"), strings.Split(want, "\n")
	for i := 0; i < len(g) || i < len(w); i++ {
		var gl, wl string
		if i < len(g) {
			gl = g[i]
		}
		if i < len(w) {
			wl = w[i]
		}
		if gl != wl {
			return fmt.Sprintf("line %d: got %q, want %q", i+1, gl, wl)
		}
	}
	return "no difference"
}
