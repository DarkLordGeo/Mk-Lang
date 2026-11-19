// Why: lower decides which runtime checks a body carries and which it
// elides, and entries reachable from C need their checks at the boundary.
// What: TestArchCounterIndexElided, TestEnumTagFromC,
// TestExportExternEntry.
// How: lowers small programs and looks for the check, or its absence, in the
// MIR text.

package lower

import (
	"strings"
	"testing"
)

func TestArchCounterIndexElided(t *testing.T) {
	l := lowerSrc(t, `Sum(xs: []int): int {
	var s := 0
	for i in 0..xs.Len() {
		s += xs[i]
	}
	return s
}
Arr(a: [4]u8): u8 {
	var s: u8 = 0
	for i in 0..a.Len() {
		s +%= a[i]
	}
	return s
}
Reassigned(ys: []int, zs: []int): int {
	var xs := ys
	var s := 0
	for i in 0..xs.Len() {
		s += xs[i]
		xs = zs
	}
	return s
}
Other(xs: []int, ys: []int): int {
	var s := 0
	for i in 0..xs.Len() {
		s += ys[i]
	}
	return s
}
Nested(xs: []int, ys: []int): int {
	var s := 0
	for i in 0..xs.Len() {
		for j in 0..ys.Len() {
			s += xs[j] + i
		}
	}
	return s
}
Offset(xs: []int): int {
	var s := 0
	for i in 1..xs.Len() {
		s += xs[i]
	}
	return s
}
`)
	for _, k := range []string{"Sum", "Arr", "Offset"} {
		if f := l.print(k); strings.Contains(f, "panic bounds") {
			t.Errorf("%s keeps a bounds check:\n%s", k, f)
		}
	}
	for _, k := range []string{"Reassigned", "Other", "Nested"} {
		if f := l.print(k); !strings.Contains(f, "panic bounds") {
			t.Errorf("%s lost its bounds check:\n%s", k, f)
		}
	}
}

func TestEnumTagFromC(t *testing.T) {
	l := lowerSrc(t, `enum Mode: u8 {
	Read
	Write
	Both = 7
}
extern GetMode(fd: i32): Mode
F(fd: i32): Mode {
	unsafe "GetMode is a C call" {
		return GetMode(fd)
	}
}
`)
	f := l.print("F")
	wantIn(t, f, "call m.GetMode(", "Discriminant(_2)", "[0: bb2, 1: bb2, 7: bb2, _: bb3]", "panic enum tag")
}

func TestExportExternEntry(t *testing.T) {
	l := lowerSrc(t, `export enum Mode: u8 {
	Read
	Write
}
G(m: Mode): i32 {
	return 1
}
export extern Take(n: i32, m: Mode): i32 {
	return G(m) + n
}
`)
	f := l.print("Take")
	wantIn(t, f, "Discriminant(_2)", "_: bb", "call m.G(")
	wantIn(t, f, "panic enum tag")
	if count(f, "Discriminant") != 1 {
		t.Fatalf("one tag check, for the enum parameter only:\n%s", f)
	}
}
