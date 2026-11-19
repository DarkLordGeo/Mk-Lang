// Why: E0501's fix data, a `from self.f` clause, comes from check, and
// borrow attaches it to an E0501 whose loan a call made.
// What: TestE0501_FieldClauseRecorded and
// TestE0501_FieldClauseImplMethodNotProven.
// How: checks functions and reads fromfix.go's FieldClause; the borrow side
// is internal/borrow's TestE0501_FieldClauseFix.

package check

import "testing"

// TestE0501_FieldClauseRecorded: a function whose automatic result comes,
// at every return, from one reference field of a parameter gets `from p.f`
// at the end of its result type, exported or not; an inline or `mut`
// field, a written clause, a return that is no field path, or two fields,
// gets none.
func TestE0501_FieldClauseRecorded(t *testing.T) {
	w := newWorld(t, true)
	b := w.add("m", opt{}, `view struct Pager {
    text: []u8
    other: []u8
    buf: [4]u8
    at: int
}

view struct Out {
    dst: mut []u8
}

Pager.Peek(mut self): []u8 {
    self.at += 1
    return self.text
}

export Pager.Tail(mut self): []u8 {
    return self.text[1..]
}

Pager.Inline(mut self): []u8 {
    return self.buf[..]
}

Pager.Either(mut self, f: bool): []u8 {
    if f {
        return self.text
    }
    return self.other
}

Pager.Said(mut self): []u8 from self.text {
    return self.text
}

Pager.Whole(mut self): &Pager {
    return self
}

Out.Dest(mut self): mut []u8 {
    return self.dst
}
`)
	b.wantClean(t)
	for _, tc := range []struct {
		name, clause, at string
		exported         bool
	}{
		{"Pager:Peek", "from self.text", "12:27", false},
		{"Pager:Tail", "from self.text", "17:34", true},
		{"Pager:Inline", "", "", false},
		{"Pager:Either", "", "", false},
		{"Pager:Said", "", "", false},
		{"Pager:Whole", "", "", false},
		{"Out:Dest", "", "", false},
	} {
		id := b.item(tc.name)
		clause, at, exported, ok := w.c.FieldClause(id)
		if tc.clause == "" {
			if ok {
				t.Errorf("%s: want no clause, got %q", tc.name, clause)
			}
			continue
		}
		if !ok || clause != tc.clause || exported != tc.exported || at.Start != at.End {
			t.Errorf("%s: got %q at %v exported %v ok %v, want %q exported %v", tc.name, clause, at, exported, ok, tc.clause, tc.exported)
			continue
		}
		if lc := w.lc(at); lc != tc.at {
			t.Errorf("%s: clause at %s, want %s", tc.name, lc, tc.at)
		}
	}
}

// TestE0501_FieldClauseImplMethodNotProven: an impl method's `from self.f`
// makes its flows differ from the interface method's (E0608), so its
// FieldClause reads as an exported function's: borrow offers it as
// compiles, never proven.
func TestE0501_FieldClauseImplMethodNotProven(t *testing.T) {
	w := newWorld(t, true)
	b := w.add("m", opt{}, `view struct Pager {
    text: []u8
    at: int
}

interface Peeker {
    Peek(mut self): []u8
}

impl Peeker for Pager {
    Peek(mut self): []u8 {
        self.at += 1
        return self.text
    }
}
`)
	b.wantClean(t)
	clause, _, exported, ok := w.c.FieldClause(b.item("#impl(m.Peeker for m.Pager):Peek"))
	if !ok || clause != "from self.text" || !exported {
		t.Errorf("impl method: got %q exported %v ok %v, want from self.text, exported", clause, exported, ok)
	}
}
