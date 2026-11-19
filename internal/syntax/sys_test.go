// Why: cpu and section are ordinary names too, so the parser must tell a
// clause after a signature from a statement that starts with the word.
// What: TestCpuClauseNewline: the cpu and section clauses after a
// signature.
// How: a line that begins with cpu or section and a string continues the
// signature; `cpu := x` stays a statement.

package syntax

import (
	"testing"

	"mk/internal/ast"
)

func sigOf(t *testing.T, p parsed, i int) *ast.Signature {
	t.Helper()
	switch d := p.file.Items[i].(type) {
	case *ast.FuncDecl:
		return d.Sig
	case *ast.ExternDecl:
		return d.Sig
	}
	t.Fatalf("item %d is %T", i, p.file.Items[i])
	return nil
}

func TestCpuClauseNewline(t *testing.T) {
	for _, tc := range []struct {
		name, src, cpu, section string
	}{
		{"same line", "F(x: int): int cpu \"avx2\" {\n\treturn x\n}\n", `"avx2"`, ""},
		{"next line", "F(x: int): int\n    cpu \"avx2, fma\"\n{\n\treturn x\n}\n", `"avx2, fma"`, ""},
		{"after a contract", "F(x: int): int requires x > 0 cpu \"avx2\" {\n\treturn x\n}\n", `"avx2"`, ""},
		{"cpu and section", "F(x: int): int cpu \"avx2\" section \".text.fast\" {\n\treturn x\n}\n", `"avx2"`, `".text.fast"`},
		{"section on its own line", "F(x: int): int\n    section \".text.boot\"\n{\n\treturn x\n}\n", "", `".text.boot"`},
		{"raw string", "F(x: int): int cpu r\"sse4.2\" {\n\treturn x\n}\n", `r"sse4.2"`, ""},
		{"extern", "extern F(x: int): int cpu \"avx2\" {\n\treturn x\n}\n", `"avx2"`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := parse(t, tc.src, Options{})
			if len(p.diags) != 0 {
				t.Fatalf("diagnostics: %v", p.diags)
			}
			s := sigOf(t, p, 0)
			got := func(b *ast.BasicLit) string {
				if b == nil {
					return ""
				}
				return b.Raw
			}
			if got(s.Cpu) != tc.cpu || got(s.Section) != tc.section {
				t.Fatalf("cpu %q section %q, want %q %q", got(s.Cpu), got(s.Section), tc.cpu, tc.section)
			}
		})
	}
	t.Run("cpu := x stays a statement", func(t *testing.T) {
		p := parse(t, "F(x: int): int {\n\tcpu := x\n\treturn cpu\n}\n", Options{})
		if len(p.diags) != 0 {
			t.Fatalf("diagnostics: %v", p.diags)
		}
		if s := sigOf(t, p, 0); s.Cpu != nil {
			t.Fatal("a statement read as a cpu clause")
		}
	})
	t.Run("a function named cpu", func(t *testing.T) {
		p := parse(t, "cpu(x: int): int {\n\treturn x\n}\n", Options{})
		if len(p.diags) != 0 {
			t.Fatalf("diagnostics: %v", p.diags)
		}
	})
	t.Run("section before cpu", func(t *testing.T) {
		p := parse(t, "F(x: int): int section \".t\" cpu \"avx2\" {\n\treturn x\n}\n", Options{})
		if len(p.diags) == 0 {
			t.Fatal("no diagnostic for a cpu clause after the section clause")
		}
	})
}
