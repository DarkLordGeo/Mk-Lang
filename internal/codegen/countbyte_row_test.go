// Why: the CountByte row in tests/bench-lanes/lang-sys/countbyte must follow
// the SIMD table's rules of tests/bench/README.md.
// What: tests of aligned buffers, only the vector layer, the wrapping
// column, and three sizes with a fill baseline each.
// How: read the row's bench.json and sources as text; tools/bench's
// TestLaneRowsMeetTheContract checks such rows generally.

package codegen

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

var countbyteDir = filepath.Join("..", "..", "tests", "bench-lanes", "lang-sys", "countbyte")

func countbyteFile(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(countbyteDir, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

type countbyteRow struct {
	Overflow string            `json:"overflow"`
	Sources  map[string]string `json:"sources"`
	Skip     map[string]string `json:"skip"`
	Variants []struct {
		Name     string   `json:"name"`
		Cpu      string   `json:"cpu"`
		Args     []string `json:"args"`
		Baseline []string `json:"baseline"`
		Bytes    int64    `json:"bytes"`
		Checksum string   `json:"checksum"`
	} `json:"variants"`
	Notes []string `json:"notes"`
}

func loadCountbyte(t *testing.T) countbyteRow {
	t.Helper()
	var r countbyteRow
	if err := json.Unmarshal([]byte(countbyteFile(t, "bench.json")), &r); err != nil {
		t.Fatal(err)
	}
	return r
}

// TestCountByteAligned: the C column starts its buffer on a
// 64-byte boundary and exits 3 when it is not; the Mk columns, which cannot
// align an arena slice without std.raw, carry the labelled exception.
func TestCountByteAligned(t *testing.T) {
	c := countbyteFile(t, "countbyte.c")
	for _, want := range []string{"malloc((size_t)n + 64)", "((64 - ((uintptr_t)raw & 63)) & 63)", "(uintptr_t)buf % 64 != 0", "return 3;"} {
		if !strings.Contains(c, want) {
			t.Errorf("countbyte.c lacks %q (BS-4)", want)
		}
	}
	labelled := false
	for _, n := range loadCountbyte(t).Notes {
		if strings.Contains(n, "BS-4") && strings.Contains(n, "std.raw") && strings.Contains(n, "exception") {
			labelled = true
		}
	}
	if !labelled {
		t.Error("bench.json's notes do not label the Mk columns' BS-4 exception")
	}
}

// TestCountByteNoBuiltin: the C column uses only the vector
// layer; of the __builtin_ hints the table allows only __builtin_ctz.
func TestCountByteNoBuiltin(t *testing.T) {
	code := regexp.MustCompile(`(?s)/\*.*?\*/|//[^\n]*`).ReplaceAllString(countbyteFile(t, "countbyte.c"), "")
	for _, b := range regexp.MustCompile(`__builtin_\w+`).FindAllString(code, -1) {
		if b != "__builtin_ctz" {
			t.Errorf("countbyte.c uses %s, which the SIMD table bans", b)
		}
	}
	if !strings.Contains(code, "_mm_popcnt_u32") || !strings.Contains(code, "<immintrin.h>") {
		t.Error("countbyte.c does not count with <immintrin.h>'s _mm_popcnt_u32")
	}
}

// TestCountByteWrapColumn: Mk checks the count's += and C does
// not, so the row has the mk-wrap column, whose source differs from mk's
// only in comments and in writing the count with +%.
func TestCountByteWrapColumn(t *testing.T) {
	r := loadCountbyte(t)
	if r.Sources["mk-wrap"] != "mkwrap" {
		t.Fatalf("sources.mk-wrap = %q, want mkwrap", r.Sources["mk-wrap"])
	}
	if _, skipped := r.Skip["mk-wrap"]; skipped {
		t.Error("mk-wrap is both a source and skipped")
	}
	code := func(s string) []string {
		var out []string
		for _, l := range strings.Split(s, "\n") {
			if t := strings.TrimSpace(l); t != "" && !strings.HasPrefix(t, "//") {
				out = append(out, l)
			}
		}
		return out
	}
	mk, wrap := code(countbyteFile(t, "mk/countbyte.mk")), code(countbyteFile(t, "mkwrap/countbyte.mk"))
	if len(mk) != len(wrap) {
		t.Fatalf("mk has %d code lines and mkwrap %d", len(mk), len(wrap))
	}
	changed := 0
	for i := range mk {
		if mk[i] == wrap[i] {
			continue
		}
		changed++
		if !strings.Contains(wrap[i], "n = n +% ") || !strings.Contains(mk[i], "n += ") {
			t.Errorf("line %d differs beyond the count's wrapping +%%:\n  mk:     %s\n  mkwrap: %s", i, mk[i], wrap[i])
		}
	}
	if changed != 2 {
		t.Errorf("%d lines differ, want the vector loop's and the tail's count", changed)
	}
}

// TestCountByteSizes: a bandwidth row of the SIMD table
// runs at the L1, L3 and DRAM sizes, each variant with a fixed cpu, a
// baseline of its arguments with 0 repetitions, bytes = n * reps, and a
// checksum.
func TestCountByteSizes(t *testing.T) {
	r := loadCountbyte(t)
	sizes := map[string]bool{}
	for _, v := range r.Variants {
		if len(v.Args) != 3 || len(v.Baseline) != 3 {
			t.Fatalf("variant %s: args %q, baseline %q", v.Name, v.Args, v.Baseline)
		}
		sizes[v.Args[0]] = true
		if v.Baseline[0] != v.Args[0] || v.Baseline[1] != "0" || v.Baseline[2] != v.Args[2] {
			t.Errorf("variant %s: baseline %q is not its arguments with 0 repetitions", v.Name, v.Baseline)
		}
		n, _ := strconv.ParseInt(v.Args[0], 10, 64)
		reps, _ := strconv.ParseInt(v.Args[1], 10, 64)
		if v.Bytes != n*reps {
			t.Errorf("variant %s: bytes %d, want n * reps = %d", v.Name, v.Bytes, n*reps)
		}
		if v.Cpu == "" || v.Checksum == "" {
			t.Errorf("variant %s has no cpu or no checksum", v.Name)
		}
	}
	for _, s := range []string{"16384", "33554432", "1073741824"} {
		if !sizes[s] {
			t.Errorf("no variant at %s bytes (B16: 16 KiB, 32 MiB, 1 GiB)", s)
		}
	}
}
