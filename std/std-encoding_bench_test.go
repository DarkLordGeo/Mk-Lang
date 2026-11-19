// Why: the std-encoding bench rows must compare like with like, or their numbers mislead.
// What: a check of tests/bench-lanes/std-encoding: every row is in the idiomatic table.
// How: other columns use their standard library only, a language without the facility
// has a skip reason (encoding-single is Mk alone), and decode rows allocate their output each round.

package std

import (
	"encoding/json"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// encBenchRow is the part of a bench.json this check reads.
type encBenchRow struct {
	Name    string            `json:"name"`
	Table   string            `json:"table"`
	Sources map[string]string `json:"sources"`
	Skip    map[string]string `json:"skip"`
}

func TestStdEncoding_BenchRows(t *testing.T) {
	dir := filepath.Join("..", "tests", "bench-lanes", "std-encoding")
	files, err := filepath.Glob(filepath.Join(dir, "*", "bench.json"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no bench rows under %s: %v", dir, err)
	}
	rustUse := regexp.MustCompile(`(?m)^\s*(?:pub\s+)?use\s+([a-z_]+)::`)
	zigImport := regexp.MustCompile(`@import\("([^"]+)"\)`)
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		var row encBenchRow
		if err := json.Unmarshal(raw, &row); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		rowDir := filepath.Dir(f)
		if row.Table != "idiomatic" {
			t.Errorf("%s: table %q, want idiomatic", row.Name, row.Table)
		}
		for _, lang := range []string{"c", "rust", "zig", "go"} {
			_, has := row.Sources[lang]
			_, skipped := row.Skip[lang]
			if has == skipped {
				t.Errorf("%s: %s must have a source or a skip reason, not both or neither", row.Name, lang)
			}
		}
		for lang, src := range row.Sources {
			if lang == "mk" {
				continue
			}
			text, err := os.ReadFile(filepath.Join(rowDir, src))
			if err != nil {
				t.Fatalf("%s %s: %v", row.Name, lang, err)
			}
			switch lang {
			case "go":
				file, err := parser.ParseFile(token.NewFileSet(), src, text, parser.ImportsOnly)
				if err != nil {
					t.Fatalf("%s: %v", src, err)
				}
				for _, im := range file.Imports {
					p, _ := strconv.Unquote(im.Path.Value)
					if first, _, _ := strings.Cut(p, "/"); strings.Contains(first, ".") {
						t.Errorf("%s go: imports %s, which is not Go's standard library", row.Name, p)
					}
				}
			case "rust":
				if strings.Contains(string(text), "extern crate") {
					t.Errorf("%s rust: extern crate", row.Name)
				}
				for _, m := range rustUse.FindAllStringSubmatch(string(text), -1) {
					if !slices.Contains([]string{"std", "core", "alloc"}, m[1]) {
						t.Errorf("%s rust: uses %s, which is not Rust's standard library", row.Name, m[1])
					}
				}
			case "zig":
				for _, m := range zigImport.FindAllStringSubmatch(string(text), -1) {
					if m[1] != "std" {
						t.Errorf("%s zig: imports %s, which is not Zig's standard library", row.Name, m[1])
					}
				}
			default:
				t.Errorf("%s: a %s column; these rows have none (C has no standard decoder for any of them)", row.Name, lang)
			}
		}
		switch row.Name {
		case "encoding-single":
			// No standard library of C, Rust, Zig or Go decodes a
			// legacy single-byte encoding, so the row is Mk alone.
			if len(row.Sources) != 1 || row.Sources["mk"] == "" {
				t.Errorf("encoding-single: sources %v, want Mk's only", row.Sources)
			}
		case "encoding-utf8", "encoding-utf8valid":
			// The output is a fresh allocation each round in every
			// column: Mk opens an arena inside the rounds loop and never
			// resets one, Zig allocates inside it; Rust's from_utf8_lossy
			// and Go's ToValidUTF8 allocate by themselves.
			mk := encReadAll(t, filepath.Join(rowDir, "mk"))
			at := strings.Index(mk, "for r in 0..rounds {")
			if at < 0 {
				t.Fatalf("%s mk: no rounds loop", row.Name)
			}
			loop := mk[at:]
			if !strings.Contains(loop, "Arena.New(sys.heap)") || strings.Contains(mk, ".Reset()") {
				t.Errorf("%s mk: the arena is not made afresh each round", row.Name)
			}
			zig, err := os.ReadFile(filepath.Join(rowDir, row.Sources["zig"]))
			if err != nil {
				t.Fatal(err)
			}
			zat := strings.Index(string(zig), "for (0..rounds)")
			if zat < 0 {
				t.Fatalf("%s zig: no rounds loop", row.Name)
			}
			zloop := string(zig)[zat:]
			if !strings.Contains(zloop, "pa.alloc(u8, b.len * 3)") {
				t.Errorf("%s zig: the output buffer is not allocated each round", row.Name)
			}
		}
	}
}

// encReadAll is the text of every .mk file in dir.
func encReadAll(t *testing.T, dir string) string {
	t.Helper()
	files, _ := filepath.Glob(filepath.Join(dir, "*.mk"))
	var b strings.Builder
	for _, f := range files {
		text, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		b.Write(text)
	}
	return b.String()
}
