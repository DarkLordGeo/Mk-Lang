// Why: a store that reaches a parameter through a local's held loan is hard
// to understand unless E0604 and E0505 name the holder and the way out.
// What: tests of E0604's and E0505's text for such a store (internal/borrow
// store.go viaText), and of the alternative E0604 gives when no clause can
// declare the store (storefix.go noClauseAlt), each named after the
// dogfood program that found it.
// How: checks small programs, compares the diagnostic text, then applies
// the offered clause and checks the fixed program and its callers.

package driver

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

// TestE0604NamesHolder: a str read from a shared parameter whose
// contents hold a view (`prog: &Program`, a `List` field) and pushed twice
// into a local list is E0604 at the second push (`p#2` takes only a
// declared flow); the message names the local that makes `*prog` a target,
// and the loan fact names `*prog`, not the shared parameter itself. The
// same store also reaches `a`, the arena `rows` lives in: one E0604 (one is
// kept per place) names the holder of `a`. A `str` is shallow, so the
// writable walk does not go on through `rows` to `prog#2`: the E0604 names
// `*prog into a` alone, the store the body makes, and that clause clears it.
func TestE0604NamesHolder(t *testing.T) {
	t.Parallel()
	const decls = "struct Fun {\n    name: str\n}\n\nstruct Program {\n    funs: List[Fun]\n}\n\n"
	body := "    var rows := List[str].New(a)\n    for f in prog.funs {\n        try rows.Push(f.name)\n    }\n    return rows.Len()\n}\n"
	r := checkText(t, decls+"Names(a: &Arena, prog: &Program): OutOfMemory!int {\n"+body)
	if len(r.Report.Diags) != 1 {
		t.Fatalf("want one E0604:\n%s", diagLines(r))
	}
	d := r.Report.Diags[0].Diag
	if d.Code.String() != "E0604" || !strings.HasSuffix(d.Message, "(`rows` holds a loan of `a`, so the store reaches it); write `*prog into a`") {
		t.Errorf("got %v %q", d.Code, d.Message)
	}
	if len(d.Fixes) != 1 || len(d.Fixes[0].Edits) != 1 {
		t.Errorf("want one fix with one edit, got %+v", d.Fixes)
	}
	if one := checkText(t, decls+"Names(a: &Arena, prog: &Program): OutOfMemory!int *prog into a {\n"+body); len(one.Report.Diags) != 0 {
		t.Errorf("`*prog into a` alone, the clause E0604 names, does not clear it:\n%s", diagLines(one))
	}
	if one := checkText(t, decls+"Names(a: &Arena, prog: &Program): OutOfMemory!int *prog into prog {\n"+body); len(one.Report.Diags) != 1 ||
		!strings.Contains(one.Report.Diags[0].Diag.Message, "(`rows` holds a loan of `a`, so the store reaches it); write `*prog into a`") {
		t.Errorf("with `*prog into prog` alone, want the E0604 for `a`:\n%s", diagLines(one))
	}
	loan := ""
	for _, f := range d.Facts {
		if f.Key == "loan" {
			loan = f.Value.Str
		}
	}
	if loan != "*prog" {
		t.Errorf("loan fact names %q, want `*prog`", loan)
	}
	if ok := checkText(t, decls+"Names(a: &Arena, prog: &Program): OutOfMemory!int *prog into a, *prog into prog {\n"+body); len(ok.Report.Diags) != 0 {
		t.Errorf("the clause E0604 names does not clear it:\n%s", diagLines(ok))
	}
}

// TestE0604NoClauseForReceiver: a key of `self.map.Iter()`
// pushed into a local list is E0604 on a method (no clause stores the
// receiver's own loan), and the diagnostic now says so and names the way
// out; on a free function the message names the local list. That store
// reaches `arena` (the list's arena). The walk stops at the shallow `str`
// and does not reach `m` (a parameter takes one `into`), so the function's
// E0604 offers `m: &TextMap[int] into arena` and that clause clears its
// body; a caller whose arena dies before the map then checks, and one
// passing `sys.arena` with a local map is E0505 (the arena outlives the
// map). The method keeps its E0604 with no clause.
func TestE0604NoClauseForReceiver(t *testing.T) {
	t.Parallel()
	src := "struct Names {\n    map: TextMap[int]\n}\n\n" +
		"Names.Keys(&self, arena: &Arena): OutOfMemory!List[str] from arena, self {\n    var out := List[str].New(arena)\n    for (key, value) in self.map.Iter() {\n        _ = value\n        try out.Push(key)\n    }\n    return out\n}\n\n" +
		"Keys(m: &TextMap[int], arena: &Arena): OutOfMemory!List[str] from arena, m {\n    var out := List[str].New(arena)\n    for (key, value) in m.Iter() {\n        _ = value\n        try out.Push(key)\n    }\n    return out\n}\n"
	r := checkText(t, src)
	if len(r.Report.Diags) != 2 {
		t.Fatalf("want two E0604:\n%s", diagLines(r))
	}
	m, f := r.Report.Diags[0].Diag, r.Report.Diags[1].Diag
	if len(m.Fixes) != 0 || len(m.Alternatives) == 0 || !strings.Contains(m.Alternatives[0], "no clause stores the receiver's own loan") {
		t.Errorf("method: fixes %v alternatives %q", m.Fixes, m.Alternatives)
	}
	if !strings.HasSuffix(f.Message, "(`out` holds a loan of `arena`, so the store reaches it); write `m: &TextMap[int] into arena`") {
		t.Errorf("function: %q", f.Message)
	}
	if len(f.Fixes) != 1 || len(f.Fixes[0].Edits) != 1 {
		t.Errorf("function: want one fix with one edit, got %+v", f.Fixes)
	}
	dup := strings.Replace(src, "    for (key, value) in m.Iter() {\n        _ = value\n        try out.Push(key)", "    for (key, value) in m.Iter() {\n        _ = value\n        try out.Push(try arena.Dup(key))", 1)
	if ok := checkText(t, dup); len(ok.Report.Diags) != 1 {
		t.Errorf("the function's `Dup` does not clear it (the method's E0604 stays):\n%s", diagLines(ok))
	}
	const fn = "Keys(m: &TextMap[int] into arena, arena: &Arena): OutOfMemory!List[str] from arena, m {\n    var out := List[str].New(arena)\n    for (key, value) in m.Iter() {\n        _ = value\n        try out.Push(key)\n    }\n    return out\n}\n\n"
	const head = "export Main(sys: &System): OutOfMemory|WriteError!void {\n    a := sys.heap.Alloc()\n    var m := TextMap[int].New(a)\n    defer m.Free(a)\n    try m.Set(a, \"x\", 1)\n"
	const tail = "    try sys.Print(\"{n}\\n\", (n: keys.Len()))\n}\n"
	if ok := checkText(t, fn+head+"    var la := try Arena.New(sys.heap)\n    defer la.Free()\n    keys := try Keys(&m, &la)\n"+tail); len(ok.Report.Diags) != 0 {
		t.Errorf("the clause E0604 names, with an arena that dies before the map: want clean:\n%s", diagLines(ok))
	}
	long := checkText(t, fn+head+"    keys := try Keys(&m, sys.arena)\n"+tail)
	if got := codes(long); !slices.Equal(got, []string{"E0505"}) {
		t.Errorf("the clause with `sys.arena` and a local map: codes %q, want [E0505]:\n%s", got, diagLines(long))
	}
}

// TestE0604NamesArena: strings read from the items of a
// `match` on a `&json.Value` and pushed into a local list in `a` are E0604
// naming the list's arena. Because a `str` is shallow, E0604 offers
// `v: &Value into a`, a store into the list's arena, and it clears the
// body. A caller whose arenas are local and die in order checks; one
// passing `sys.arena` with a tree parsed into `sys.arena` is E0505 through
// `*sys`.
func TestE0604NamesArena(t *testing.T) {
	t.Parallel()
	const strs = "import std.json\n\nStrs(a: &Arena, v: &json.Value%s): OutOfMemory!List[str] from a, v {\n    var out := List[str].New(a)\n    match v {\n        .Array(items) {\n            for item in &items {\n                s := item.Str() else { continue }\n                try out.Push(s)\n            }\n        }\n        .Null | .Bool | .Number | .Text | .Object { }\n    }\n    return out\n}\n\n"
	r := checkText(t, fmt.Sprintf(strs, ""))
	if len(r.Report.Diags) != 1 {
		t.Fatalf("want one E0604:\n%s", diagLines(r))
	}
	if d := r.Report.Diags[0].Diag; d.Code.String() != "E0604" || !strings.HasSuffix(d.Message, "(`out` holds a loan of `a`, so the store reaches it); write `v: &Value into a`") || len(d.Fixes) != 1 {
		t.Errorf("got %v %q fixes %+v", d.Code, d.Message, d.Fixes)
	}
	const local = "export Main(sys: &System): json.Error|WriteError!void {\n    var pa := try Arena.New(sys.heap)\n    defer pa.Free()\n    v := try json.Parse(&pa, \"[\\\"a\\\", \\\"b\\\"]\")\n    var la := try Arena.New(sys.heap)\n    defer la.Free()\n    xs := try Strs(&la, &v)\n    try sys.Print(\"{n}\\n\", (n: xs.Len()))\n}\n"
	if ok := checkText(t, fmt.Sprintf(strs, " into a")+local); len(ok.Report.Diags) != 0 {
		t.Errorf("the clause E0604 names, local arenas: want clean:\n%s", diagLines(ok))
	}
	const sysArena = "export Main(sys: &System): json.Error|WriteError!void {\n    v := try json.Parse(sys.arena, \"[\\\"a\\\", \\\"b\\\"]\")\n    xs := try Strs(sys.arena, &v)\n    try sys.Print(\"{n}\\n\", (n: xs.Len()))\n}\n"
	if e := checkText(t, fmt.Sprintf(strs, " into a")+sysArena); !slices.Equal(codes(e), []string{"E0505"}) {
		t.Errorf("the clause with `sys.arena`: codes %q, want [E0505]:\n%s", codes(e), diagLines(e))
	}
}

// TestE0505NamesHolder: a local arena's loan that a call
// stores beside `sys.args` strings is E0505, since the list holding the
// strings reaches `sys`'s allocators; the message names the target and its
// holder: the arena whose memory holds `names` (views of `*sys`). Without
// `a into out` it checks clean. The same store from `sys.arena` on a helper
// goes into `sys`'s own allocator, which holds `*sys` by being part of it,
// so it checks clean with the clause or without it.
func TestE0505NamesHolder(t *testing.T) {
	t.Parallel()
	const keep = "Keep(a: &Arena into out, names: []str, out: mut List[str]): OutOfMemory!void *names into out {\n    for n in names {\n        try out.Push(try a.Dup(n))\n    }\n}\n\n"
	r := checkText(t, keep+"Grep(sys: &System): OutOfMemory!int {\n    var arena := try Arena.New(sys.heap)\n    defer arena.Free()\n    a := &arena\n    var names := List[str].New(a)\n    try names.Push(sys.args[0])\n    var files := List[str].New(a)\n    try Keep(a, names[..], mut files)\n    return files.Len()\n}\n")
	if len(r.Report.Diags) != 1 {
		t.Fatalf("want one E0505:\n%s", diagLines(r))
	}
	if d := r.Report.Diags[0].Diag; d.Code.String() != "E0505" || !strings.Contains(d.Message, "into `*sys` (`arena` holds a loan of `*sys`, so the store reaches it)") {
		t.Errorf("got %v %q", d.Code, d.Message)
	}
	e := checkText(t, keep+"Count(sys: &System): int {\n    var out := List[str].New(sys.arena)\n    Keep(sys.arena, sys.args[1..], mut out) else {\n        return 2\n    }\n    return out.Len()\n}\n")
	if len(e.Report.Diags) != 0 {
		t.Fatalf("want no diagnostic without the clause (C3's self filter):\n%s", diagLines(e))
	}
	ok := checkText(t, keep+"Count(sys: &System): int *sys into sys {\n    var out := List[str].New(sys.arena)\n    Keep(sys.arena, sys.args[1..], mut out) else {\n        return 2\n    }\n    return out.Len()\n}\n")
	if len(ok.Report.Diags) != 0 {
		t.Errorf("the clause E0604 names does not clear it:\n%s", diagLines(ok))
	}
}

// TestE0604SecondInto: a parameter that already flows into one
// target cannot be declared into a second (one `into` per parameter), and
// E0604 says so instead of offering nothing.
//
// A chain may close through a `*q into p` step: with `a into
// self` and `*self into a`, a's loan stored into a is declared, so Twice's
// own store needs no second `into`. Without `*self into a` the chain does
// not close and the one-`into` E0604 stands.
func TestE0604SecondInto(t *testing.T) {
	t.Parallel()
	const head = "struct Inner {\n    names: List[str]\n}\n\nstruct Outer {\n    all: List[Inner]\n    first: List[str]\n}\n\n" +
		"Outer.Grow(mut self, a: &Arena into self): OutOfMemory!void *self into self {\n    var inner := Inner(names: List[str].New(a))\n    if self.first.Len() > 0 {\n        try inner.names.Push(self.first[0])\n    }\n    try self.all.Push(inner)\n}\n\n"
	oneInto := func(r Result) bool {
		for _, e := range r.Report.Diags {
			for _, alt := range e.Diag.Alternatives {
				if strings.Contains(alt, "`a` already flows into `self`, and a parameter takes one `into`") {
					return true
				}
			}
		}
		return false
	}
	r := checkText(t, head+"Outer.Twice(mut self, a: &Arena into self): OutOfMemory!void *self into self, *a into a {\n    try self.Grow(a)\n}\n")
	if !oneInto(r) {
		t.Errorf("no E0604 names the one-`into` rule:\n%s", diagLines(r))
	}
	closed := checkText(t, head+"Outer.Twice(mut self, a: &Arena into self): OutOfMemory!void *self into self, *a into a, *self into a {\n    try self.Grow(a)\n}\n")
	if oneInto(closed) {
		t.Errorf("`a into self` and `*self into a` declare a's loan into a, yet E0604 names the one-`into` rule:\n%s", diagLines(closed))
	}
}
