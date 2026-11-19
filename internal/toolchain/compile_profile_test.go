// Why: the size profile must compile at -Oz, not at a speed level.
// What: TestProfileSizeCompileFlag, the Oz level's compile flag.
// How: builds a CompileJob at Oz and checks its arguments and each level's flag.

package toolchain

import (
	"path/filepath"
	"slices"
	"testing"
)

func TestProfileSizeCompileFlag(t *testing.T) {
	in, out := filepath.Join(t.TempDir(), "a.ll"), filepath.Join(t.TempDir(), "a.o")
	args, err := compileArgs(CompileJob{Triple: "x86_64-pc-windows-msvc", Input: in, Output: out, Opt: Oz})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(args, "-Oz") || slices.Contains(args, "-O3") || slices.Contains(args, "-O0") {
		t.Errorf("Oz compile args %q", args)
	}
	if O0.flag() != "-O0" || O3.flag() != "-O3" || Oz.flag() != "-Oz" {
		t.Error("the levels' flags")
	}
}
