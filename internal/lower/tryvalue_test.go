// Why: try on a stored E!T value must lower to a switch on its discriminant,
// as try on a call does, instead of failing in lower.
// What: TestTryUnion_StoredValue.
// How: lowers a local and a parameter of type E!T under try and reads the MIR
// for the switch, the payload and the try exit chain.

package lower

import (
	"strings"
	"testing"
)

// TestTryUnion_StoredValue: try on an E!T value that is no call (a local
// kept from `var r: E!T = F()`, a parameter) switches on the value's
// discriminant: ok continues with the payload, an error leaves through the
// try exit chain (return try), as a call's try edge does.
func TestTryUnion_StoredValue(t *testing.T) {
	l := lowerSrc(t, `error Bad

Get(n: int): Bad!int {
	if n < 0 {
		fail Bad
	}
	return n
}

Local(n: int): Bad!int {
	var r: Bad!int = Get(n)
	v := try r
	return v + 1
}

Param(r: Bad!int): Bad!int {
	return try r
}

Void(r: Bad!void): Bad!void {
	try r
}
`)
	for _, fn := range []string{"Local", "Param", "Void"} {
		got := l.print(fn)
		if strings.Count(got, "return try") != 1 || !strings.Contains(got, "Discriminant(") {
			t.Errorf("%s: want one try exit reached by a switch on the value's discriminant:\n%s", fn, got)
		}
	}
}
