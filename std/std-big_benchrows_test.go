// Why: a benchmark row is fair only when each baseline runs as its users
// run it and the row's notes match what std/big ships.
// What: TestSTDBIG_ZigRowsUseDefaultAllocator, TestSTDBIG_GoCellsLabelADX
// and TestSTDBIG_NotesNameShippedThresholds over tests/bench-lanes/std-big.
// How: reads the rows: each Zig row gives std.math.big.int its default
// general purpose allocator, not page_allocator (which maps and unmaps every
// temporary); each Go cell says math/big runs MULX/ADCX/ADOX assembly,
// outside x86-64-v2; each threshold the notes name is the one std/big ships.

package std

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// bigRowsDir holds the std-big rows, seen from package std's directory.
var bigRowsDir = filepath.Join("..", "tests", "bench-lanes", "std-big")

// bigRow is the part of a bench.json these checks read.
type bigRow struct {
	Name    string            `json:"name"`
	Sources map[string]string `json:"sources"`
	Notes   []string          `json:"notes"`
}

func bigRows(t *testing.T) map[string]bigRow {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(bigRowsDir, "*", "bench.json"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no std-big rows under %s: %v", bigRowsDir, err)
	}
	rows := map[string]bigRow{}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		var r bigRow
		if err := json.Unmarshal(b, &r); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		rows[filepath.Dir(f)] = r
	}
	return rows
}

// bigCode is a Zig source without its // comments.
func bigCode(src string) []string {
	var out []string
	for _, l := range strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n") {
		if i := strings.Index(l, "//"); i >= 0 {
			l = l[:i]
		}
		out = append(out, l)
	}
	return out
}

// TestSTDBIG_ZigRowsUseDefaultAllocator: no std-big Zig row hands
// std.heap.page_allocator to std.math.big.int; page_allocator may appear
// only as an ArenaAllocator's backing, and each row takes init.gpa or such
// an arena.
func TestSTDBIG_ZigRowsUseDefaultAllocator(t *testing.T) {
	n := 0
	for dir, r := range bigRows(t) {
		src, ok := r.Sources["zig"]
		if !ok {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, src))
		if err != nil {
			t.Fatal(err)
		}
		n++
		good := false
		for i, l := range bigCode(string(b)) {
			if strings.Contains(l, "page_allocator") && !strings.Contains(l, "ArenaAllocator.init(") {
				t.Errorf("%s:%d: page_allocator outside an ArenaAllocator: every temporary of std.math.big.int becomes a map and an unmap (%s)", src, i+1, strings.TrimSpace(l))
			}
			if strings.Contains(l, "init.gpa") || strings.Contains(l, "ArenaAllocator.init(") {
				good = true
			}
		}
		if !good {
			t.Errorf("%s: takes neither init.gpa nor an ArenaAllocator", src)
		}
	}
	if n == 0 {
		t.Fatal("no Zig source in any std-big row")
	}
}

// TestSTDBIG_GoCellsLabelADX: every std-big row with a Go cell says, in
// its notes, that Go's math/big takes ADX/BMI2 assembly at run time,
// outside x86-64-v2, so the published ratio is read with it.
func TestSTDBIG_GoCellsLabelADX(t *testing.T) {
	for dir, r := range bigRows(t) {
		if _, ok := r.Sources["go"]; !ok {
			continue
		}
		all := strings.Join(r.Notes, "\n")
		if !strings.Contains(all, "ADX") || !strings.Contains(all, "x86-64-v2") {
			t.Errorf("%s (%s): no note labels Go's ADX/BMI2 assembly as outside x86-64-v2", r.Name, dir)
		}
	}
}

// bigConst reads `const name: int = N` from std/big.
func bigConst(t *testing.T, name string) int {
	t.Helper()
	files, _ := filepath.Glob(filepath.Join("big", "*.mk"))
	re := regexp.MustCompile(`(?m)^const ` + name + `: int = (\d+)\s*$`)
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if g := re.FindSubmatch(b); g != nil {
			v, _ := strconv.Atoi(string(g[1]))
			return v
		}
	}
	t.Fatalf("std/big declares no const %s", name)
	return 0
}

var (
	bigNoteFrom   = regexp.MustCompile(`(Karatsuba|Toom-3|Burnikel-Ziegler)[^;,()]*? from ([\d,]+) (divisor )?words`)
	bigNotePieces = regexp.MustCompile(`([\d,]+)-word (Karatsuba|Toom-3) pieces`)
	bigNoteSquare = regexp.MustCompile(`(schoolbook|Karatsuba's|Toom-3's) square from ([\d,]+) words`)
)

// TestSTDBIG_NotesNameShippedThresholds: a threshold a std-big row's
// notes name ("Toom-3 from 300 words", "Burnikel-Ziegler from 60 divisor
// words", "512-word Toom-3 pieces", "Karatsuba's square from 80 words") is
// the one std/big ships, and the muldiv row names its product's and its
// division's algorithm that way.
func TestSTDBIG_NotesNameShippedThresholds(t *testing.T) {
	want := map[string]int{
		"Karatsuba":        bigConst(t, "KaratsubaWords"),
		"Toom-3":           bigConst(t, "ToomWords"),
		"Burnikel-Ziegler": bigConst(t, "DivRecWords"),
		"schoolbook":       bigConst(t, "SqrBasicWords"),
		"Karatsuba's":      bigConst(t, "KaraSqrWords"),
		"Toom-3's":         bigConst(t, "ToomSqrWords"),
	}
	atoi := func(s string) int {
		v, _ := strconv.Atoi(strings.ReplaceAll(s, ",", ""))
		return v
	}
	named := map[string]int{}
	for dir, r := range bigRows(t) {
		for _, note := range r.Notes {
			for _, g := range bigNoteSquare.FindAllStringSubmatch(note, -1) {
				named[r.Name]++
				if v := atoi(g[2]); v != want[g[1]] {
					t.Errorf("%s (%s): note says %q, std/big ships %s square from %d words", r.Name, dir, g[0], g[1], want[g[1]])
				}
			}
			for _, g := range bigNoteFrom.FindAllStringSubmatch(note, -1) {
				if strings.Contains(g[0], "square") {
					continue
				}
				named[r.Name]++
				if v := atoi(g[2]); v != want[g[1]] {
					t.Errorf("%s (%s): note says %q, std/big ships %s from %d words", r.Name, dir, g[0], g[1], want[g[1]])
				}
			}
			for _, g := range bigNotePieces.FindAllStringSubmatch(note, -1) {
				named[r.Name]++
				n := atoi(g[1])
				alg := "Karatsuba"
				if n >= want["Toom-3"] {
					alg = "Toom-3"
				} else if n < want["Karatsuba"] {
					alg = "schoolbook"
				}
				if g[2] != alg {
					t.Errorf("%s (%s): note says %q, but MulEq takes %d-word pieces by %s", r.Name, dir, g[0], n, alg)
				}
			}
		}
	}
	if named["big-muldiv"] < 3 {
		t.Errorf("big-muldiv's notes name %d thresholds; want its pieces, Toom-3's and Burnikel-Ziegler's", named["big-muldiv"])
	}
}
