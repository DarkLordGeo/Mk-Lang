// Why: two requires clauses that check tells apart can lower to one body,
// so a signature hash of the body alone let a warm check keep a stale
// clean verdict after a clause edit.
// What: TestInvalidation_ClauseText and
// TestInvalidation_ClauseTextReaderReused.
// How: edit cycles over a callee's clause with fresh caches, each step warm
// against cold, then a verify check; the second test counts what a clause
// edit rechecks.

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

// The mk.sig.v1 hash of a compile-time requires: a signature's hash holds
// each contract's text beside its body's hash (iface writeSig). check folds
// a same-module clause from its AST and matches a restatement by its text,
// and two clauses check tells apart can lower to one body: `SizeOf[T]() +
// SizeOf[u8]() < 4` does not fold (layoutCall folds SizeOf only of a type
// parameter) and `SizeOf[T]() + 1 < 4` does. Were the caller's read of the
// callee's signature to hash the body only, an edit from the first to the
// second would keep the caller's clean verdict warm while a cold check is
// E0706, and `mk run --release` would build the program and read past the
// table; imported, the restatement `requires SizeOf[U]() + SizeOf[u8]() < 4`
// would meet the old text warm and not the new one cold. Each case runs its
// edit cycle with fresh caches, several times, as mk check does
// (ReleaseFront), each step warm against cold, then a verify check.

const clauseTokZ2 = "Z2[T](t: &[4]int): int\n    requires CLAUSE\n{\n    return t[0]\n}\n\n" +
	"struct Big23 {\n    b: [23]u8\n}\n\n"

const clauseTokPick = "Pick(): int {\n    t := [11, 22, 33, 44]\n    return Z2[u16](&t) + Z2[Big23](&t)\n}\n"

// clauseTokLib is the imported callee and its caller in another module,
// which restates the clause as it was first written.
const clauseTokLib = "export G[T](): int\n    requires CLAUSE\n{\n    return 0\n}\n"

const clauseTokK = "import b\n\nK[U](): int\n    requires SizeOf[U]() + SizeOf[u8]() < 4\n{\n    return b.G[U]()\n}\n\nPick(): int {\n    return K[u8]()\n}\n"

// The const-generic twin, which gave the same warm and cold verdicts even
// before the clause text was hashed.
const clauseTokLibN = "export GN[const N: int](): int\n    requires CLAUSE\n{\n    return N\n}\n"

const clauseTokKN = "import b\n\nK[const M: int](): int\n    requires M + SizeOf[u8]() < 4\n{\n    return b.GN[M]()\n}\n\nPick(): int {\n    return K[1]()\n}\n"

func TestInvalidation_ClauseText(t *testing.T) {
	const (
		u8Form  = "SizeOf[T]() + SizeOf[u8]() < 4"
		litForm = "SizeOf[T]() + 1 < 4"
	)
	for _, c := range []struct {
		name   string
		cycles int // the observed stale-hit rate leaves (3/8)^6 < 0.3% for six cycles
		files  func(clause string) map[string]string
		vals   []string // the clause per step
		bad    []bool   // E0706 at the step
	}{
		{"same module: SizeOf of a closed type to a literal (r6_i16)", 6, func(cl string) map[string]string {
			return map[string]string{"a/main.mk": strings.Replace(clauseTokZ2, "CLAUSE", cl, 1) + clauseTokPick}
		}, []string{u8Form, litForm, u8Form}, []bool{false, true, false}},
		{"same module: AlignOf of a closed type to a literal (r6_i19e)", 3, func(cl string) map[string]string {
			return map[string]string{"a/main.mk": strings.Replace(clauseTokZ2, "CLAUSE", cl, 1) + clauseTokPick}
		}, []string{"SizeOf[T]() + AlignOf[u8]() < 4", litForm, "SizeOf[T]() + AlignOf[u8]() < 4"}, []bool{false, true, false}},
		{"same module: the callee's file alone edited", 3, func(cl string) map[string]string {
			return map[string]string{"a/main.mk": clauseTokPick, "a/z2.mk": strings.Replace(clauseTokZ2, "CLAUSE", cl, 1)}
		}, []string{u8Form, litForm, u8Form}, []bool{false, true, false}},
		// Its stale hit is not deterministic (without the fix it once
		// passed where the other five failed), so six cycles.
		{"same module: a literal to SizeOf of a closed type", 6, func(cl string) map[string]string {
			return map[string]string{"a/main.mk": strings.Replace(clauseTokZ2, "CLAUSE", cl, 1) + clauseTokPick}
		}, []string{litForm, u8Form, litForm}, []bool{true, false, true}},
		{"imported: a type generic's restatement matched by text (xmodt)", 3, func(cl string) map[string]string {
			return map[string]string{"a/main.mk": clauseTokK, "b/b.mk": strings.Replace(clauseTokLib, "CLAUSE", cl, 1)}
		}, []string{u8Form, litForm, u8Form}, []bool{false, true, false}},
		{"imported: a const generic's restatement matched by text (xmod)", 3, func(cl string) map[string]string {
			return map[string]string{"a/main.mk": clauseTokKN, "b/b.mk": strings.Replace(clauseTokLibN, "CLAUSE", cl, 1)}
		}, []string{"N + SizeOf[u8]() < 4", "N + 1 < 4", "N + SizeOf[u8]() < 4"}, []bool{false, true, false}},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			for k := range c.cycles {
				init := c.files(c.vals[0])
				init["pkg.mk"] = manifest(nil)
				dir := writeTree(t, init)
				cfg := Config{Root: dir, CacheDir: t.TempDir(), Target: types.X86_64Windows, BuildID: source.HashString("test", "mk"), ReleaseFront: true}
				for i, v := range c.vals {
					for p, text := range c.files(v) {
						if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(p)), []byte(text), 0o644); err != nil {
							t.Fatal(err)
						}
					}
					warm := frontCheck(t, cfg)
					coldCfg := cfg
					coldCfg.CacheDir = t.TempDir()
					cold := frontCheck(t, coldCfg)
					wc, cc := codes(warm), codes(cold)
					if !slices.Equal(wc, cc) {
						t.Errorf("cycle %d step %d (%q): warm %q, cold %q\nwarm:\n%s", k, i, v, wc, cc, warm.Report.Text(warm.Sources))
					}
					if want := c.bad[i]; slices.Contains(cc, "E0706") != want || len(cc) > 1 {
						t.Errorf("cycle %d step %d (%q): cold %q, want E0706 %v alone", k, i, v, cc, want)
					}
					if i > 0 && !slices.Contains(c.vals[:i], v) && !slices.Contains(pkgItems(warm.Trace.ItemsChecked), "a.Pick") && !slices.Contains(pkgItems(warm.Trace.ItemsChecked), "a.K") {
						t.Errorf("cycle %d step %d: rechecked %q, want a.Pick or a.K among them", k, i, pkgItems(warm.Trace.ItemsChecked))
					}
				}
				// The text is in the hash the same way fresh and decoded:
				// a verify check recomputes every entry as stored.
				vcfg := cfg
				vcfg.CacheVerify = true
				if v := frontCheck(t, vcfg); !slices.Equal(codes(v), codes(frontCheck(t, cfg))) {
					t.Errorf("cycle %d: verify check %q\n%s", k, codes(v), v.Report.Text(v.Sources))
				}
				if t.Failed() {
					return
				}
			}
		})
	}
}

// A clause edit rechecks no more than before: a function whose clause
// changed is no longer reused, and neither is a caller that reads its
// signature, but a function that names neither is.
func TestInvalidation_ClauseTextReaderReused(t *testing.T) {
	other := "Other[T](): int\n    requires SizeOf[T]() + SizeOf[u8]() < 4\n{\n    return 0\n}\n\n"
	main := strings.Replace(clauseTokZ2, "CLAUSE", "SizeOf[T]() < 100", 1) + clauseTokPick
	dir := writeTree(t, map[string]string{"pkg.mk": manifest(nil), "a/main.mk": main, "a/other.mk": other})
	cfg := Config{Root: dir, CacheDir: t.TempDir(), Target: types.X86_64Windows, BuildID: source.HashString("test", "mk"), ReleaseFront: true}
	if cold := frontCheck(t, cfg); len(cold.Report.Diags) > 0 {
		t.Fatalf("cold check:\n%s", cold.Report.Text(cold.Sources))
	}
	edited := strings.Replace(other, "SizeOf[u8]()", "1", 1)
	if err := os.WriteFile(filepath.Join(dir, "a", "other.mk"), []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	warm := frontCheck(t, cfg)
	if len(warm.Report.Diags) > 0 {
		t.Fatalf("warm check:\n%s", warm.Report.Text(warm.Sources))
	}
	got := pkgItems(warm.Trace.ItemsChecked)
	if slices.Contains(got, "a.Pick") {
		t.Errorf("rechecked %q: a.Pick should be reused", got)
	}
	if !slices.Contains(got, "a.Other") {
		t.Errorf("rechecked %q, want a.Other among them", got)
	}
}
