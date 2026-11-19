// Why: a release build with a cache must keep its alwaysinline marks when a
// call passes an immediate to a const parameter.
// What: TestConstImmArgPackKeyed.
// How: builds a package with such a call in release with a fresh cache and
// with --no-cache, checks every pack but support is keyed, and compares
// the linked bytes.

package driver

import (
	"bytes"
	"path/filepath"
	"testing"

	"mk/internal/types"
)

// TestConstImmArgPackKeyed (a regression found through std's
// Utf8LookupN[N](s, true)): in release, a direct call that passes an
// immediate to a const parameter must not stop codegen's PackFacts. Its
// packGen has no LLVM module, and calleeArgs once took the immediate's
// size from the LLVM repr, so the small-function set's computation
// panicked inside its sync.Once: the pack went unkeyed and the unit's set
// stayed empty, so every pack of a build with a cache lost its
// alwaysinline marks while a --no-cache build kept them. Every pack but
// support is keyed, and a fresh-cache build links the --no-cache bytes.
func TestConstImmArgPackKeyed(t *testing.T) {
	tools := needTools(t)
	files := pkFiles()
	files["lib/lib.mk"] += "\nexport Pick(x: int, const neg: bool): int {\n    if neg {\n        return 0 - x\n    }\n    return x\n}\n"
	files["app/app.mk"] = `import lib
import shape

export Main(sys: &System): int {
    o := shape.MakeOuter(3)
    return lib.F(2) + lib.Size(&o) + lib.Wrap(1) + lib.Tag(shape.Color.Green) + lib.Counter - 26 + lib.Pick(4, true) + lib.Pick(4, false)
}
`
	dir := writeTree(t, files)
	out := t.TempDir()
	cfg := Config{Root: dir, Target: pkTarget(), Profile: types.Release, Tools: tools,
		CacheDir: filepath.Join(t.TempDir(), "cache")}
	cold := pkBuild(t, cfg, filepath.Join(out, "cold.exe"))
	for _, e := range cold.events {
		if e.Name != "support" && !e.HasPre {
			t.Errorf("pack %s (%s) has no pre-emission key", e.Name, e.First)
		}
	}
	nc := cfg
	nc.NoCache = true
	nc.CacheDir = filepath.Join(t.TempDir(), "unused")
	_, ncExe, _ := incBuild(t, nc, filepath.Join(out, "nocache.exe"))
	if !bytes.Equal(cold.exe, ncExe) {
		t.Errorf("fresh-cache bin (%d bytes) differs from the --no-cache bin (%d bytes)", len(cold.exe), len(ncExe))
	}
}
