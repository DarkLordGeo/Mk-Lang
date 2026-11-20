// Why: a method's `export` decides whether another module may call it
// (E0204), so taking it away must recheck the callers from a warm cache.
// What: TestInvalidation_MethodExportRemoved.
// How: checks a package whose module a calls b's exported method cold,
// removes the `export`, checks warm and asserts E0204 at the call, the same
// --json as a cold check of the edited tree.

package driver

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mk/internal/source"
	"mk/internal/types"
)

func TestInvalidation_MethodExportRemoved(t *testing.T) {
	b := "export struct T {\n    n: int\n}\n\nexport New(): T {\n    return T(n: 1)\n}\n\n" +
		"export T.Get(&self): int {\n    return self.n\n}\n"
	files := map[string]string{
		"pkg.mk": manifest(nil),
		"b/b.mk": b,
		"a/a.mk": "import b\n\nexport A(): int {\n    t := b.New()\n    return t.Get()\n}\n",
	}
	dir := writeTree(t, files)
	cfg := Config{Root: dir, CacheDir: t.TempDir(), Target: types.X86_64Windows, BuildID: source.HashString("test", "mk")}
	if cold := frontCheck(t, cfg); len(cold.Report.Diags) > 0 {
		t.Fatalf("cold check:\n%s", cold.Report.Text(cold.Sources))
	}
	edited := strings.Replace(b, "export T.Get", "T.Get", 1)
	if err := os.WriteFile(filepath.Join(dir, "b", "b.mk"), []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	warm := frontCheck(t, cfg)
	if len(warm.Report.Diags) != 1 || warm.Report.Diags[0].Diag.Code.String() != "E0204" {
		t.Fatalf("warm check, want one E0204:\n%s", warm.Report.Text(warm.Sources))
	}
	fresh := cfg
	fresh.CacheDir = t.TempDir()
	if cold2 := frontCheck(t, fresh); !bytes.Equal(jsonOf(t, warm), jsonOf(t, cold2)) {
		t.Errorf("warm --json %s, cold %s", jsonOf(t, warm), jsonOf(t, cold2))
	}
}
