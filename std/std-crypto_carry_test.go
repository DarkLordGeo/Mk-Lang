// Why: the carry chains of fiat.mk and poly1305.mk and field.mk's 128-bit
// columns must give the same words in the evaluator as natively.
// What: TestSTDCRYPTO_CarriesCompileTimeMatchesRunTime with its fixture and
// program (cryptoCarryFixture, cryptoCarryMain).
// How: computes tests_carry.mk's and tests_field.mk's checks in consts and at
// run time, in dev and release.

package std

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// cryptoCarryFixture exports the two checks to the test's program.
const cryptoCarryFixture = `// Fixture of the std-crypto carry test: tests_carry.mk's and
// tests_field.mk's checks, exported.

export FxCarries(): bool {
    return TxCaFiat()
}

export FxColumns(from: int, to: int, n: int): bool {
    return TxFeColumns(from, to, n)
}
`

// cryptoCarryMain evaluates each check in consts (the columns' nine rows of
// boundary pairs three to a const, which the evaluator's fuel holds with
// room, and four seeded pairs) and runs both at run time, the columns in
// full.
const cryptoCarryMain = `import std.crypto

const Carries: bool = crypto.FxCarries()
const Columns: bool = crypto.FxColumns(0, 3, 4)
const Columns2: bool = crypto.FxColumns(3, 6, 0)
const Columns3: bool = crypto.FxColumns(6, 9, 0)

export Main(sys: &System): WriteError!void {
    var out := sys.Stdout()
    try Print(mut out, "{a} {b} {c} {d}\n", (a: Carries, b: Columns && Columns2 && Columns3, c: crypto.FxCarries(), d: crypto.FxColumns(0, 9, 2000)))
}
`

// TestSTDCRYPTO_CarriesCompileTimeMatchesRunTime: both checks hold in the
// evaluator and natively, in dev and release.
func TestSTDCRYPTO_CarriesCompileTimeMatchesRunTime(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	stdDir := cryptoStdDir(t, root, map[string]string{"crypto/tests_carryfx.mk": cryptoCarryFixture})
	if err := os.WriteFile(filepath.Join(root, "main.mk"), []byte(cryptoCarryMain), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, mode := range cryptoModes {
		exe := filepath.Join(root, "carry"+strings.Join(mode, "")+".exe")
		args := append(append([]string{"build"}, mode...), "-o", exe, "main.mk")
		out, st := mkCmd(t, root, stdDir, args...)
		if st != 0 {
			if strings.Contains(out, "no usable LLVM") {
				t.Skipf("needs the LLVM toolchain: %s", out)
			}
			t.Fatalf("mk %s: exit %d\n%s", strings.Join(args, " "), st, out)
		}
		r := runNative(t, exe, "")
		if r.code != 0 {
			t.Fatalf("%v: exit %d\n%s", mode, r.code, r.stderr)
		}
		if got := strings.TrimSpace(strings.ReplaceAll(r.stdout, "\r\n", "\n")); got != "true true true true" {
			t.Errorf("%v: %q, want \"true true true true\" (compile-time carries, compile-time columns, run-time carries, run-time columns)", mode, got)
		}
	}
}
