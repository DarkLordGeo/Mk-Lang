// Why: a struct pattern in a match arm was a fuzz crasher
// (lower_struct_pattern): check accepts it and sets no Use, and lower read
// every PathPattern as a variant ("types.Items.Get: unknown item 0").
// What: TestLowerStructPattern.
// How: lowers the crasher's seed, then evaluates matches whose struct
// patterns bind, test fields and nest.

package lower

import "testing"

// TestLowerStructPattern: the crasher's seed lowers and validates, and
// struct patterns bind, test their fields and nest as the reference says.
func TestLowerStructPattern(t *testing.T) {
	// The seed, byte for byte.
	seed := "struct P {\n\tx: int\n}\n\nA(p: P) {\n\tmatch p {\n\t\tP(x) {\n\t\t\t_ = x\n\t\t}\n\t}\n}\n"
	l := lowerSrc(t, seed)
	if b := l.body("A"); len(b.Blocks) == 0 {
		t.Fatal("A has no blocks")
	}
	for _, c := range []struct {
		name, src string
		want      int64
	}{
		{"bind", `struct P {
	x: int
	y: int
}
H(): int {
	p := P(x: 3, y: 4)
	match p {
		P(x, y) {
			return x * 10 + y
		}
	}
}
`, 34},
		{"field sub-pattern", `struct P {
	x: int
	y: int
}
Pick(p: P): int {
	match p {
		P(x: 1, y) {
			return y
		}
		P(x, y: _) {
			return x * 100
		}
	}
}
H(): int {
	return Pick(P(x: 1, y: 7)) + Pick(P(x: 2, y: 9))
}
`, 207},
		{"by reference", `struct P {
	x: int
}
H(): int {
	p := P(x: 5)
	match &p {
		P(x) {
			return *x
		}
	}
}
`, 5},
		{"nested in a variant", `struct P {
	x: int
}
enum E {
	A(p: P)
	B
}
Get(e: E): int {
	match e {
		.A(p: P(x)) {
			return x
		}
		.B {
			return 0
		}
	}
}
H(): int {
	return Get(E.A(p: P(x: 8))) + Get(E.B)
}
`, 8},
		{"generic struct", `struct Box[T] {
	v: T
}
H(): int {
	b := Box[int](v: 6)
	match b {
		Box(v) {
			return v
		}
	}
}
`, 6},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, err := evalH(t, c.src)
			if err != nil {
				t.Fatalf("eval: %v", err)
			}
			if got != c.want {
				t.Fatalf("got %d, want %d", got, c.want)
			}
		})
	}
}
