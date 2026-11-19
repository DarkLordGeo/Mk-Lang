// Why: an imported compile-time requires must be judged by the real
// evaluator, which refuses some reads, and a diagnostic inside a fn literal
// in a requires must not turn into an internal error.
// What: TestRequiresRefusalRealEvaluator and
// TestLiteralInContractWalk.
// How: checks small packages with such clauses at calls and fn values and
// compares the diagnostics.

package driver

import "testing"

// TestRequiresRefusalRealEvaluator: an imported compile-time requires
// at a concrete call or fn value runs on the real evaluator. A
// clause the evaluator refuses for a var read is left to run time, with no
// E0901: `Ready > 0` named directly and through a function, at a call and
// at a value. A clause false before the var is read stays E0706, and a
// refused raw access (a null read) stays E0901 at the call. Run with -run,
// as every driver test.
func TestRequiresRefusalRealEvaluator(t *testing.T) {
	t.Parallel()
	const lib = `import std.raw

export var Ready: int = 1

export G(): int
    requires Ready > 0
{
    return 7
}

Now(): int {
    return Ready
}

export H(): int
    requires Now() > 0
{
    return 8
}

export Both[const N: int](): int
    requires N > 0 && Ready > 0
{
    return N
}

Peek(): bool {
    unsafe "a null read, refused by the evaluator" {
        p := raw.Null[u8]()
        return raw.Read(p) == 0
    }
}

export Raw[const N: int](): int
    requires N > 0 && Peek()
{
    return N
}
`
	bins := `[pkg.Bin(name: "app", root: ".", link_script: "", cpu: "", subsystem: "console")]`
	for _, tc := range []struct{ name, main, want string }{
		{"refused at calls and values", "f := lib.G\n    g := lib.H\n    return f() + g() + lib.G() + lib.H() + lib.Both[1]()", ""},
		{"false before the var", "return lib.Both[0]()", "E0706@4"},
		{"false before the var, value", "f := lib.Both[0]\n    return f()", "E0706@4"},
		{"refused raw access", "return lib.Raw[1]()", "E0901@4"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := writeTree(t, map[string]string{
				"pkg.mk":     manifest(map[string]string{"name": `"app"`, "bins": bins, "unsafe_modules": `["lib"]`}),
				"lib/lib.mk": lib,
				"main.mk":    "import lib\n\nexport Main(sys: &System): int {\n    " + tc.main + "\n}\n",
			})
			r := frontCheck(t, Config{Root: dir, CacheDir: t.TempDir()})
			if got := diagAt(r); got != tc.want {
				t.Errorf("diagnostics %q, want %q:\n%s", got, tc.want, diagLines(r))
			}
		})
	}
}

// TestLiteralInContractWalk: a diagnostic inside a fn literal in a
// requires (here the fn value `G[0]`, E0706) left the literal with no body,
// and the instance-limit walk hashed F's instance, which names its
// predicate's body and so the literal's: E0000 "iface: no body for
// main.F#requires0#fn0" beside the E0706. F's callers are now leaves
// (lostPredicateOwners), so the E0706 is the one diagnostic; the literal
// with `G[N]` under `requires N > 0` checks clean (ownerSig climbs from the
// predicate to F).
func TestLiteralInContractWalk(t *testing.T) {
	t.Parallel()
	const pre = "G[const N: int](): int\n    requires N > 0\n{\n    return N\n}\n\n" +
		"Run(const f: fn(): bool): bool {\n    return f()\n}\n\n"
	for _, tc := range []struct{ name, src, want string }{
		{"E0706 in the literal", pre + "F(): int\n    requires Run(fn(): bool {\n        h := G[0]\n        return h() > 0\n    })\n{\n    return 1\n}\n\n" +
			"export Main(sys: &System): int {\n    return F()\n}\n", "E0706@13"},
		{"restated by F", pre + "F[const N: int](): int\n    requires N > 0\n    requires Run(fn(): bool {\n        h := G[N]\n        return h() > 0\n    })\n{\n    return N\n}\n\n" +
			"export Main(sys: &System): int {\n    return F[2]()\n}\n", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := checkText(t, tc.src)
			if got := diagAt(r); got != tc.want {
				t.Errorf("diagnostics %q, want %q:\n%s", got, tc.want, diagLines(r))
			}
		})
	}
}
