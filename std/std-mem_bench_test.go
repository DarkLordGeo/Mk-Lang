// Why: tools/bench publishes two tables that must never mix, so each
// std.mem benchmark row has to say which table it belongs to.
// What: a check of the rows under tests/bench-lanes/std-mem2.
// How: each row must declare a table tests/bench/README.md defines, and a
// row comparing standard libraries must be in "idiomatic".

package std

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestSTDMEM_BenchTables: every std-mem2 row's table is one that
// tests/bench/README.md's "Tables, never mixed" section names (the
// backticked first cell of each line),
// and both std-mem2 rows, which compare Fixed with Zig's standard
// FixedBufferAllocator, are "idiomatic". A row named "std" fails it.
func TestSTDMEM_BenchTables(t *testing.T) {
	readme, err := os.ReadFile(filepath.Join("..", "tests", "bench", "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	sec := string(readme)
	i := strings.Index(sec, "## Tables, never mixed")
	if i < 0 {
		t.Fatal("tests/bench/README.md has no \"Tables, never mixed\" section")
	}
	sec = sec[i:]
	if j := strings.Index(sec[3:], "\n## "); j >= 0 {
		sec = sec[:j+3]
	}
	tables := map[string]bool{}
	for _, m := range regexp.MustCompile("(?m)^\\| `([a-z]+)` \\|").FindAllStringSubmatch(sec, -1) {
		tables[m[1]] = true
	}
	if !tables["same"] || !tables["idiomatic"] {
		t.Fatalf("tables read from tests/bench/README.md: %v", tables)
	}
	rows, err := filepath.Glob(filepath.Join("..", "tests", "bench-lanes", "std-mem2", "*", "bench.json"))
	if err != nil || len(rows) < 2 {
		t.Fatalf("std-mem2 bench rows: %v %v", rows, err)
	}
	for _, r := range rows {
		b, err := os.ReadFile(r)
		if err != nil {
			t.Fatal(err)
		}
		var row struct {
			Name  string `json:"name"`
			Table string `json:"table"`
		}
		if err := json.Unmarshal(b, &row); err != nil {
			t.Fatalf("%s: %v", r, err)
		}
		if !tables[row.Table] {
			t.Errorf("%s: table %q is none of tests/bench/README.md's tables", r, row.Table)
		}
		if row.Table != "idiomatic" {
			t.Errorf("%s: row %s compares standard libraries, so its table is idiomatic, not %q", r, row.Name, row.Table)
		}
	}
}
