// Why: check's recur.go is a copy of internal/borrow's (check may not
// import borrow), and the two must not drift.
// What: TestRecurKeysMatchBorrow.
// How: parses both files with go/parser and compares the shared
// declarations' text.

package check

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRecurKeysMatchBorrow: enter, size, grewFrom and occurs, the recur and
// recurAt types, the constructor and the bound's constants are borrow's
// text exactly, and key is borrow's without its anonymous-record case
// (check counts no record). A change to one copy that the other lacks fails
// here.
func TestRecurKeysMatchBorrow(t *testing.T) {
	t.Parallel()
	funcs := func(path string) map[string]string {
		t.Helper()
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		fs := token.NewFileSet()
		f, err := parser.ParseFile(fs, path, src, 0)
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]string{}
		text := func(n any) string {
			var b bytes.Buffer
			if err := printer.Fprint(&b, fs, n); err != nil {
				t.Fatal(err)
			}
			return b.String()
		}
		for _, d := range f.Decls {
			switch d := d.(type) {
			case *ast.FuncDecl:
				switch {
				case d.Recv != nil:
					out[d.Name.Name] = text(d.Body)
				case d.Name.Name == "newRecur" || d.Name.Name == "newRecurOn":
					// The constructor: check's newRecur(tab) is borrow's
					// newRecurOn(tab).
					out["ctor"] = text(d.Body)
				}
			case *ast.GenDecl:
				for _, sp := range d.Specs {
					switch sp := sp.(type) {
					case *ast.TypeSpec:
						out["type "+sp.Name.Name] = text(sp.Type)
					case *ast.ValueSpec:
						for i, n := range sp.Names {
							if strings.HasPrefix(n.Name, "maxRecur") && i < len(sp.Values) {
								out["const "+n.Name] = text(sp.Values[i])
							}
						}
					}
				}
			}
		}
		return out
	}
	mine := funcs("recur.go")
	theirs := funcs(filepath.Join("..", "borrow", "recur.go"))
	for _, name := range []string{"enter", "size", "grewFrom", "occurs", "ctor", "type recur", "type recurAt",
		"const maxRecurGrowth", "const maxRecurInstances", "const maxRecurSize"} {
		if mine[name] == "" || mine[name] != theirs[name] {
			t.Errorf("recur.%s differs from internal/borrow's", name)
		}
	}
	const recordCase = "\tcase ty.Kind == types.KRecord:\n\t\treturn recordKey, true\n"
	if !strings.Contains(theirs["key"], recordCase) {
		t.Fatalf("borrow's recur.key has no record case any more:\n%s", theirs["key"])
	}
	if want := strings.Replace(theirs["key"], recordCase, "", 1); mine["key"] != want {
		t.Errorf("recur.key is not borrow's without the record case:\ncheck:\n%s\nborrow:\n%s", mine["key"], theirs["key"])
	}
}
