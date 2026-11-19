// Why: a cached or shared object must not depend on where or when it was
// compiled.
// What: TestCOFFObjectsReproducible over reproLL, a module with debug lines.
// How: a dev COFF object is a function of its .ll and the compile options,
// not of the directory it was compiled in or the time; the test compiles
// in two directories and compares bytes.

package toolchain

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// reproLL is a module with a CodeView line table, as codegen writes one in
// every profile.
const reproLL = `define i32 @f(i32 %x) !dbg !5 {
  %y = add i32 %x, 1, !dbg !8
  ret i32 %y, !dbg !8
}
!llvm.dbg.cu = !{!0}
!llvm.module.flags = !{!3, !4}
!0 = distinct !DICompileUnit(language: DW_LANG_C, file: !1, producer: "mk", isOptimized: false, runtimeVersion: 0, emissionKind: LineTablesOnly)
!1 = !DIFile(filename: "x.mk", directory: ".")
!3 = !{i32 2, !"CodeView", i32 1}
!4 = !{i32 2, !"Debug Info Version", i32 3}
!5 = distinct !DISubprogram(name: "f", scope: !1, file: !1, line: 1, type: !6, unit: !0, spFlags: DISPFlagDefinition)
!6 = !DISubroutineType(types: !7)
!7 = !{}
!8 = !DILocation(line: 2, column: 3, scope: !5)
`

// TestCOFFObjectsReproducible compiles one .ll at -O0 in two directories of
// different name lengths and requires byte-identical objects (the flags
// -mno-incremental-linker-compatible and -gno-codeview-command-line).
func TestCOFFObjectsReproducible(t *testing.T) {
	tl := realTools(t)
	const triple = "x86_64-pc-windows-msvc"
	root := t.TempDir()
	var objs []string
	for _, d := range []string{"a", "a-much-longer-directory"} {
		dir := filepath.Join(root, d)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		in := filepath.Join(dir, "x.ll")
		if err := os.WriteFile(in, []byte(reproLL), 0o644); err != nil {
			t.Fatal(err)
		}
		out := filepath.Join(dir, "x.o")
		if err := tl.Compile(context.Background(), CompileJob{Triple: triple, Input: in, Output: out, Opt: O0, CPU: "x86-64-v2"}); err != nil {
			t.Fatal(err)
		}
		objs = append(objs, out)
	}
	if !sameBytes(t, objs[0], objs[1]) {
		t.Error("the same .ll compiled in two directories gave two objects")
	}
}
