// Why: a generator's output depends on the bodies it ran; editing one must
// rerun the generator, and importers must miss only when an item changed.
// What: TestInvalidation_GeneratorBody, over the harness genInvPkg.
// How: edits bodies g's generator runs, checks warm, and compares the
// missed modules and the --json with a cold check into a fresh cache.

package driver

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"mk/internal/source"
	"mk/internal/types"
)

// genInvPkg is the harness for the invalidation table's row "Body that ran
// in a generator" (doc/compiler.md): g holds the generator Make, which calls
// g.Two and emits a.Helper; a has the gen line; b imports a and calls
// a.Helper.
func genInvPkg() map[string]string {
	return map[string]string{
		"pkg.mk": manifest(nil),
		"g/g.mk": "Two(): int {\n    return 2\n}\n\n" +
			"export Make(g: mut Gen) {\n    if Two() == 2 {\n        g.Emit(quote {\n            export Helper(): int {\n                return 1\n            }\n        })\n    }\n}\n",
		"a/a.mk": "import g\n\ngen g.Make()\n",
		"b/b.mk": "import a\n\nexport Use(): int {\n    return a.Helper() + 1\n}\n",
	}
}

// TestInvalidation_GeneratorBody (row 6 of invalidation_test.go): an edit of
// a body that ran in a generator reruns that generator; importers of the
// module with the gen line miss only when an item's hash changed (the item
// hash cutoff: a's interface hash). The module with gen output is stored
// with its generated file (GenRef, genref.go): unedited, a hits and no
// generator runs. Each case also asserts warm --json equal to a cold check
// of the edited source in a fresh cache.
func TestInvalidation_GeneratorBody(t *testing.T) {
	genKey := types.GenKey("a", 0).String()
	for _, c := range []struct {
		name    string
		old     string
		new     string
		missed  []string
		checked []string
	}{
		// Same output: Two's body changes, the generator reruns, nothing
		// a exports changes, so b hits. Make's own body is unchanged, so it
		// is reused (funccache.go); its run calls the new Two.
		// a reruns its generator; a generated function is never loaded on
		// its own (its pins are not in its tokens), so a.Helper is checked.
		{"SameOutput", "    return 2\n}", "    return 1 + 1\n}", []string{"a", "g"}, []string{"a.Helper", "g.Two"}},
		// A generated body changes, its signature does not: b hits.
		{"NewBody", "                return 1\n", "                return 5\n", []string{"a", "g"}, []string{"a.Helper", "g.Make"}},
		// A new generated export: a's interface changes, b misses; b.Use
		// reads only a.Helper's signature, so it is reused (funccache.go).
		{"NewItem", "            export Helper(): int {", "            export Extra(): int {\n                return 0\n            }\n\n            export Helper(): int {",
			[]string{"a", "b", "g"}, []string{"a.Extra", "a.Helper", "g.Make"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			files := genInvPkg()
			dir := writeTree(t, files)
			cfg := Config{Root: dir, CacheDir: t.TempDir(), Target: types.X86_64Windows, BuildID: source.HashString("test", "mk")}
			cold := frontCheck(t, cfg)
			clean(t, cold)
			if len(pkgOnly(cold.Trace.NotStored)) != 0 {
				t.Fatalf("cold not stored %q, want every module stored", cold.Trace.NotStored)
			}
			// No edit: every module hits, a with its generated file.
			same := frontCheck(t, cfg)
			if got := pkgOnly(same.Trace.ModulesResolved); len(got) != 0 {
				t.Errorf("unedited check resolved %q, want none", got)
			}
			if got := pkgItems(same.Trace.GeneratorRuns); len(got) != 0 {
				t.Errorf("unedited check ran generators %q, want none", got)
			}
			p := filepath.Join(dir, "g", "g.mk")
			if !strings.Contains(files["g/g.mk"], c.old) {
				t.Fatalf("%q not in g/g.mk", c.old)
			}
			if err := os.WriteFile(p, []byte(strings.Replace(files["g/g.mk"], c.old, c.new, 1)), 0o644); err != nil {
				t.Fatal(err)
			}
			warm := frontCheck(t, cfg)
			clean(t, warm)
			if got := pkgOnly(warm.Trace.ModulesResolved); !slices.Equal(got, c.missed) {
				t.Errorf("missed %q, want %q", got, c.missed)
			}
			if got := pkgItems(warm.Trace.ItemsChecked); !slices.Equal(got, c.checked) {
				t.Errorf("rechecked %q, want %q (from cache %q)", got, c.checked, pkgItems(warm.Trace.ItemsFromCache))
			}
			if got := pkgItems(warm.Trace.GeneratorRuns); !slices.Equal(got, []string{genKey}) {
				t.Errorf("generator runs %q, want [%s]", got, genKey)
			}
			fresh := cfg
			fresh.CacheDir = t.TempDir()
			if w, c := jsonOf(t, warm), jsonOf(t, frontCheck(t, fresh)); !bytes.Equal(w, c) {
				t.Errorf("warm --json %s, cold %s", w, c)
			}
		})
	}
}
