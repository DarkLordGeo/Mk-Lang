// Why: a caller that restates a callee's requires clause is matched by what
// the clause's names mean, so a change that keeps the text but moves a
// meaning must recheck the caller, warm as cold.
// What: TestInvalidation_RestateByMeaning and
// TestInvalidation_RestateImportedAlias.
// How: steps a small package through edits, checks each step warm and cold
// with fresh caches per cycle, then runs a verify check.

package driver

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"mk/internal/source"
	"mk/internal/types"
)

// TestInvalidation_RestateByMeaning: check matches a restated clause by
// what each name the callee's clause keeps means, read from the callee's
// Contract.Text and the item table (check's restate.go). The callee's
// module gaining a private item of a name its clause reads bare (here
// `Arch`, until then the prelude's) changes that meaning with the text
// unchanged: the caller's restatement no longer meets the demand, which no
// clause of the caller can now meet. Each step is checked warm and cold,
// with fresh caches per cycle, then a verify check.
func TestInvalidation_RestateByMeaning(t *testing.T) {
	const own = "enum Arch {\n    X64\n}\n\n"
	lib := func(hidden bool) string {
		s := "export F[const N: int](): int\n    requires N < 4 && Arch.X64 == Arch.X64\n{\n    return N\n}\n"
		if hidden {
			s = own + s
		}
		return s
	}
	const app = "import b\n\nK[const M: int](): int\n    requires M < 4 && Arch.X64 == Arch.X64\n{\n    return b.F[M]()\n}\n\nPick(): int {\n    return K[1]()\n}\n"
	steps := []bool{false, true, false} // b's own Arch per step; E0706 alone where it has one
	for k := range 3 {
		dir := writeTree(t, map[string]string{"pkg.mk": manifest(nil), "a/main.mk": app, "b/b.mk": lib(steps[0])})
		cfg := Config{Root: dir, CacheDir: t.TempDir(), Target: types.X86_64Windows, BuildID: source.HashString("test", "mk"), ReleaseFront: true}
		for i, hidden := range steps {
			if err := os.WriteFile(filepath.Join(dir, "b", "b.mk"), []byte(lib(hidden)), 0o644); err != nil {
				t.Fatal(err)
			}
			warm := frontCheck(t, cfg)
			coldCfg := cfg
			coldCfg.CacheDir = t.TempDir()
			cold := frontCheck(t, coldCfg)
			wc, cc := codes(warm), codes(cold)
			if !slices.Equal(wc, cc) {
				t.Errorf("cycle %d step %d: warm %q, cold %q\nwarm:\n%s", k, i, wc, cc, warm.Report.Text(warm.Sources))
			}
			if slices.Contains(cc, "E0706") != hidden || len(cc) > 1 {
				t.Errorf("cycle %d step %d: cold %q, want E0706 %v alone\n%s", k, i, cc, hidden, cold.Report.Text(cold.Sources))
			}
			if hidden && !strings.Contains(cold.Report.Text(cold.Sources), "`Arch` is private to b") {
				t.Errorf("cycle %d step %d: the demand should say b's Arch is private:\n%s", k, i, cold.Report.Text(cold.Sources))
			}
		}
		vcfg := cfg
		vcfg.CacheVerify = true
		if v := frontCheck(t, vcfg); !slices.Equal(codes(v), codes(frontCheck(t, cfg))) {
			t.Errorf("cycle %d: verify check %q\n%s", k, codes(v), v.Report.Text(v.Sources))
		}
		if t.Failed() {
			return
		}
	}
}

// TestInvalidation_RestateImportedAlias: b's at.mk binds lim to module small
// (Lim 4) and requires `N < lim.Lim`; the caller binds module lim (Lim 100)
// as q and restates `J < q.Lim`. The middle step adds b/big.mk, which
// imports module lim plainly, and the last removes it. A module-qualified
// name of an imported callee's clause is matched by its text (its file's
// binding is not in the contract), so the demand is `J < lim.Lim` at every
// step and E0706 cold. A guess by the last path segment would meet it at the
// middle step, and a warm check would then reuse that clean body at the
// last. Each step warm and cold, fresh caches per cycle, then a verify
// check.
func TestInvalidation_RestateImportedAlias(t *testing.T) {
	const at = "import lim = small\n\nexport F[const N: int](): int\n    requires N >= 0 && N < lim.Lim\n{\n    return N\n}\n"
	const big = "import lim\n\nexport Big(): int {\n    return lim.Lim\n}\n"
	const app = "import b\nimport q = lim\n\nK[const J: int](): int\n    requires J >= 0 && J < q.Lim\n{\n    return b.F[J]()\n}\n\nPick(): int {\n    return K[24]()\n}\n"
	steps := []bool{false, true, false} // b/big.mk present per step
	for k := range 4 {
		dir := writeTree(t, map[string]string{"pkg.mk": manifest(nil), "a/main.mk": app, "b/at.mk": at,
			"small/small.mk": "export const Lim: int = 4\n", "lim/lim.mk": "export const Lim: int = 100\n"})
		cfg := Config{Root: dir, CacheDir: t.TempDir(), Target: types.X86_64Windows, BuildID: source.HashString("test", "mk"), ReleaseFront: true}
		bigPath := filepath.Join(dir, "b", "big.mk")
		for i, withBig := range steps {
			if withBig {
				if err := os.WriteFile(bigPath, []byte(big), 0o644); err != nil {
					t.Fatal(err)
				}
			} else if err := os.Remove(bigPath); err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
			warm := frontCheck(t, cfg)
			coldCfg := cfg
			coldCfg.CacheDir = t.TempDir()
			cold := frontCheck(t, coldCfg)
			wc, cc := codes(warm), codes(cold)
			if !slices.Equal(wc, cc) {
				t.Errorf("cycle %d step %d: warm %q, cold %q\nwarm:\n%s", k, i, wc, cc, warm.Report.Text(warm.Sources))
			}
			if !slices.Equal(cc, []string{"E0706"}) {
				t.Errorf("cycle %d step %d: cold %q, want E0706 alone\n%s", k, i, cc, cold.Report.Text(cold.Sources))
			}
			if !strings.Contains(cold.Report.Text(cold.Sources), "J < lim.Lim") {
				t.Errorf("cycle %d step %d: the demand should keep the callee's `lim.Lim`:\n%s", k, i, cold.Report.Text(cold.Sources))
			}
		}
		vcfg := cfg
		vcfg.CacheVerify = true
		if v := frontCheck(t, vcfg); !slices.Equal(codes(v), codes(frontCheck(t, cfg))) {
			t.Errorf("cycle %d: verify check %q\n%s", k, codes(v), v.Report.Text(v.Sources))
		}
		if t.Failed() {
			return
		}
	}
}
