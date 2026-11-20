// Why: mk check must not label a clause fix proven when mk fix, which
// applies every proven fix, would refuse it. A clause proven for the
// function it edits can move the error to a caller as one no clause clears.
//
// What: TestProvenClauseCallerError (the E0604 fixes whose caller then fails
// with E0505 or E0503 are alternatives naming that error),
// TestProvenClauseNotWrittenBeside, TestProvenClauseChainDeadEnd,
// TestProvenClausePastBound, and TestProvenClauseMovedUpStaysProven (a
// chain of clauses mk fix writes in rounds stays proven).
//
// How: each program is checked with its fixes recompiled (verifiedCheck);
// mk fix writes only what that check labels proven, and ends better.
package driver

import (
	"fmt"
	"strings"
	"testing"

	"mk/internal/diag"
)

// keysSysArena: Keys copies a borrowed map's key views into a list in
// arena; E0604 names `m: &TextMap[int] into arena`, and with it Main's
// call with a local map and `sys.arena` is E0505.
const keysSysArena = `Keys(m: &TextMap[int], arena: &Arena): OutOfMemory!List[str] from arena, m {
    var out := List[str].New(arena)
    for (key, value) in m.Iter() {
        _ = value
        try out.Push(key)
    }
    return out
}

export Main(sys: &System): OutOfMemory|WriteError!void {
    a := sys.heap.Alloc()
    var m := TextMap[int].New(a)
    defer m.Free(a)
    try m.Set(a, "x", 1)
    keys := try Keys(&m, sys.arena)
    try sys.Print("{n}\n", (n: keys.Len()))
}
`

// provenApplied checks mk fix against the verified check of src, and
// returns the verified result. mk fix's first round writes only fixes the
// check labels proven; with none it writes nothing; with some it ends with
// a report better than the check's (fewer diagnostics, or clean).
func provenApplied(t *testing.T, src string) Result {
	t.Helper()
	v, file := verifiedCheck(t, src)
	proven := map[string]bool{}
	for _, e := range v.Report.Diags {
		for _, f := range e.Diag.Fixes {
			if f.Class == diag.Proven {
				proven[f.Why] = true
			}
		}
	}
	s, err := NewSession(Config{Root: file, CacheDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	res, err := s.Fix(FixOptions{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(proven) == 0 {
		if len(res.Rounds) != 0 {
			t.Errorf("mk check offers no proven fix, mk fix writes %+v", res.Rounds)
		}
		return v
	}
	if len(res.Rounds) == 0 {
		t.Fatalf("mk check offers %d proven fixes, mk fix writes none (stopped: %s):\n%s", len(proven), res.Stopped, diagLines(v))
	}
	for _, a := range res.Rounds[0].Applied {
		if !proven[a.Why] {
			t.Errorf("mk fix writes %q at %s, which mk check does not label proven:\n%s", a.Why, a.At, diagLines(v))
		}
	}
	if !better(res.Final.Report, v.Report) {
		t.Errorf("mk fix ends no better than it started (stopped: %s):\n%s", res.Stopped, diagLines(res.Final))
	}
	return v
}

// TestProvenClauseNotWrittenBeside: beside a good E0604 (Log.Add, no
// caller), the Keys clause whose caller then fails is not written: mk fix
// writes Log.Add's clause alone and leaves Keys' E0604.
func TestProvenClauseNotWrittenBeside(t *testing.T) {
	t.Parallel()
	v := provenApplied(t, keysSysArena+`
struct Log {
    lines: List[str]
}

Log.Add(mut self, line: str): OutOfMemory!void {
    try self.lines.Push(line)
}
`)
	if n := len(v.Report.Diags); n != 2 {
		t.Fatalf("want two E0604:\n%s", diagLines(v))
	}
}

// TestProvenClauseChainDeadEnd: Inner's clause moves its E0604 to Outer,
// whose own clause then leaves Main with an E0505. The chain ends in no
// progress, so Inner's clause is not proven and mk fix writes nothing.
func TestProvenClauseChainDeadEnd(t *testing.T) {
	t.Parallel()
	src := strings.Replace(keysSysArena, "keys := try Keys(&m, sys.arena)", "keys := try Outer(&m, sys.arena)", 1) + `
Outer(m: &TextMap[int], arena: &Arena): OutOfMemory!List[str] from arena, m {
    return try Keys(m, arena)
}
`
	v := provenApplied(t, src)
	if len(v.Report.Diags) != 1 {
		t.Fatalf("want one E0604:\n%s", diagLines(v))
	}
	alts := strings.Join(v.Report.Diags[0].Diag.Alternatives, "|")
	if !strings.Contains(alts, "m: &TextMap[int] into arena") || !strings.Contains(alts, "(not proven: recompiled, the package then reports E0505 at ") {
		t.Errorf("alternatives %q: want the clause with a note naming E0505", alts)
	}
}

// TestProvenClausePastBound: eighteen clauses whose callers then fail are
// more than the rechecks of one report allow; none is labelled proven, and
// those not recompiled say so.
func TestProvenClausePastBound(t *testing.T) {
	t.Parallel()
	var b strings.Builder
	b.WriteString("export Main(sys: &System): OutOfMemory!void {\n    a := sys.heap.Alloc()\n    var m := TextMap[int].New(a)\n    defer m.Free(a)\n")
	var fns strings.Builder
	for i := range 18 {
		n := "Keys" + itoaQ(i)
		fmt.Fprintf(&b, "    k%d := try %s(&m, sys.arena)\n    _ = k%d\n", i, n, i)
		fmt.Fprintf(&fns, "\n%s(m: &TextMap[int], arena: &Arena): OutOfMemory!List[str] from arena, m {\n    var out := List[str].New(arena)\n    for (key, value) in m.Iter() {\n        _ = value\n        try out.Push(key)\n    }\n    return out\n}\n", n)
	}
	b.WriteString("}\n")
	v, _ := verifiedCheck(t, b.String()+fns.String())
	if len(v.Report.Diags) != 18 {
		t.Fatalf("want 18 E0604:\n%s", diagLines(v))
	}
	unchecked := 0
	for _, e := range v.Report.Diags {
		for _, f := range e.Diag.Fixes {
			if f.Class == diag.Proven {
				t.Errorf("a clause is labelled proven: %+v", f)
			}
		}
		for _, a := range e.Diag.Alternatives {
			if strings.Contains(a, "(not proven: ") && strings.Contains(a, "over the bound") {
				unchecked++
			}
		}
	}
	if unchecked == 0 {
		t.Errorf("no alternative says it was not recompiled:\n%s", diagLines(v))
	}
}

// TestProvenClauseCallerError: the clause E0604 names clears the function's
// own body, but with it the caller stores a local's loan into memory that
// outlives it (E0505 through `*sys`), or moves a value that now holds its
// own loan (E0503). No clause clears that, so mk fix stops with no
// progress; mk check offers the clause as an alternative that names the
// caller's error, not as a proven fix.
func TestProvenClauseCallerError(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, src, clause, code string
	}{
		{"map keys into sys.arena", keysSysArena, "m: &TextMap[int] into arena", "E0505"},
		{"json strings into sys.arena", `import std.json

Strs(a: &Arena, v: &json.Value): OutOfMemory!List[str] from a, v {
    var out := List[str].New(a)
    match v {
        .Array(items) {
            for item in &items {
                s := item.Str() else { continue }
                try out.Push(s)
            }
        }
        .Null | .Bool | .Number | .Text | .Object { }
    }
    return out
}

export Main(sys: &System): json.Error|WriteError!void {
    v := try json.Parse(sys.arena, "[\"a\", \"b\"]")
    xs := try Strs(sys.arena, &v)
    try sys.Print("{n}\n", (n: xs.Len()))
}
`, "v: &Value into a", "E0505"},
		{"ctx carrying the allocator", `struct Shared2 {
    m: Mutex[TextMap[HeapText]]
}

struct Ctx {
    st: &Shared2
    a: &Alloc
    io: Io
}

PutB(c: &Ctx, key: str, value: str): Canceled|OutOfMemory!void {
    var g := try c.st.m.Lock(c.io)
    defer g.Unlock()
    var v := HeapText.New(c.a)
    onerr v.Free(c.a)
    try v.Push(c.a, value)
    try g.Mut().Set(c.a, key, v)
}

export Main(sys: &System) {
    a := sys.heap.Alloc()
    st := Shared2(m: Mutex.New(TextMap[HeapText].New(a)))
    c := Ctx(st: &st, a: a, io: sys.io)
    PutB(&c, "k", "v") else { }
    Shared2(m) := st
    m.Into().Free(a)
}
`, "*c into c", "E0503"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			plain := checkText(t, tc.src)
			if len(plain.Report.Diags) != 1 || plain.Report.Diags[0].Diag.Code != diag.E0604 {
				t.Fatalf("want one E0604:\n%s", diagLines(plain))
			}
			v := provenApplied(t, tc.src)
			if len(v.Report.Diags) != 1 {
				t.Fatalf("want one E0604:\n%s", diagLines(v))
			}
			d := v.Report.Diags[0].Diag
			for _, f := range d.Fixes {
				if f.Class == diag.Proven {
					t.Errorf("the clause is still offered as proven: %+v", f)
				}
			}
			alts := strings.Join(d.Alternatives, "|")
			if !strings.Contains(alts, tc.clause) || !strings.Contains(alts, "(not proven: recompiled, the package then reports "+tc.code+" at ") {
				t.Errorf("alternatives %q: want %q with a note naming %s", d.Alternatives, tc.clause, tc.code)
			}
		})
	}
}

// TestProvenClauseMovedUpStaysProven: writing `line: str into self` on
// Log.Add moves the E0604 to Log.Warn, then to Log.Twice, each with its own
// proven clause, and mk fix writes the chain. The recompile counts the move
// as progress, so the fix stays proven.
func TestProvenClauseMovedUpStaysProven(t *testing.T) {
	t.Parallel()
	src := `struct Log {
    lines: List[str]
}

Log.Add(mut self, line: str): OutOfMemory!void {
    try self.lines.Push(line)
}

Log.Warn(mut self, line: str): OutOfMemory!void {
    try self.Add(line)
}

Log.Twice(mut self, line: str): OutOfMemory!void {
    try self.Warn(line)
    try self.Warn(line)
}

export Main(sys: &System): OutOfMemory!void {
    var log := Log(lines: List[str].New(sys.arena))
    try log.Twice("x")
}
`
	v := provenApplied(t, src)
	if len(v.Report.Diags) != 1 {
		t.Fatalf("want one E0604:\n%s", diagLines(v))
	}
	fixes := v.Report.Diags[0].Diag.Fixes
	if len(fixes) != 1 || fixes[0].Class != diag.Proven {
		t.Errorf("want the one proven clause: %+v alternatives %q", fixes, v.Report.Diags[0].Diag.Alternatives)
	}
}
