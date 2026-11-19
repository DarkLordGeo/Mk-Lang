// Why: a const generic typed by a type parameter must take a named const
// of the type the parameter becomes, and refuse one of another type.
// What: TestParamTypedConstArg.
// How: checks small programs passing named consts of several
// types to such generics, in bodies and in declarations' types.

package driver

import "testing"

// TestParamTypedConstArg: a const generic typed by a type
// parameter (`GT[T, const V: T]`, `struct P[T, const V: T]`) takes a named
// const of any type, a str one included, in a body, and an integer one in a
// declaration's type. An earlier E0301 compared the const's type with the
// unsubstituted `$0` and rejected every const that was not an integer or a
// bool. (A same-module str const named in a declaration's type is E0301
// "expected `str`, found `str`" at its initializer: a separate bug, not
// pinned here. A same-module enum const there is E0908.) The same test
// pins the other side: an int const for an enum with a payload is E0301
// (its instance would read a tag and a payload out of the int).
func TestParamTypedConstArg(t *testing.T) {
	t.Parallel()
	r := checkText(t, `enum Mode {
    A
    B
}

const Here: str = "here"
const MB: Mode = Mode.B
const K: u8 = 3

GT[T, const V: T](): T {
    return V
}

struct P[T, const V: T] {
    x: int
}

Take(p: P[u8, K]): int {
    return p.x
}

Use(): int {
    a := GT[str, Here]()
    c := GT[u8, K]()
    d := GT[Mode, MB]()
    e := GT[Mode, Mode.A]()
    p := P[u8, K](x: 4)
    q := P[str, Here](x: 5)
    if d == e {
        return 0
    }
    return a.Len() + int(c) + Take(p) + q.x
}
`)
	if len(r.Report.Diags) != 0 {
		t.Fatalf("want no diagnostics, got:\n%s", diagLines(r))
	}
	r = checkText(t, `enum Pick {
    Name(s: str)
    Num(n: int)
}

const K: int = 0

GD[const D: Pick](): int {
    return 1
}

Use(): int {
    return GD[K]()
}
`)
	got := ""
	for _, e := range r.Report.Diags {
		got += e.Diag.Code.String() + " "
	}
	if got != "E0711 E0301 " && got != "E0301 E0711 " {
		t.Fatalf("want E0301 and E0711, got %q:\n%s", got, diagLines(r))
	}
	// A char const for an int generic is E0301 (it once printed 97), and a
	// str const for a generic typed by the caller's open T is too (no
	// instance is checked again at mono).
	r = checkText(t, `const Ch: char = 'a'
const Here: str = "here"

GI[const N: int](): int {
    return N
}

GT[T, const V: T](): T {
    return V
}

Use(): int {
    return GI[Ch]()
}

H[U](): U {
    return GT[U, Here]()
}
`)
	got = ""
	for _, e := range r.Report.Diags {
		if e.Diag.Code.String() == "E0301" {
			got += e.Diag.Code.String() + " "
		}
	}
	if got != "E0301 E0301 " {
		t.Fatalf("want two E0301, got %q:\n%s", got, diagLines(r))
	}
}
