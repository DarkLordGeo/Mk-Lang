// Why: tools/bench's contract tests skip the rows under tests/bench-lanes
// (`r.Lane != ""` in bench_test.go and findings_simd_test.go), so std.simd's
// rows there need their own check of the SIMD table's source rules.
// What: TestSTDSIMD_LaneRowsMeetTheContract and SIMD7PlantedViolations.
// How: the bans of findings_simd_test.go and simdViolations, copied (that
// package is a main package and cannot be imported), the 64-byte alignment
// refusal, a checksum in every variant, and notes that describe the Mk
// column as what its source calls; each rule is shown to catch a planted
// case.

package std

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// The bans, as tools/bench/findings_simd_test.go:40-52 states them.
var (
	sdRustUnsafeItem  = regexp.MustCompile(`\bunsafe\s+(fn|impl|trait|extern)\b`)
	sdRustTargetAttr  = regexp.MustCompile(`#\s*!?\[\s*target_feature`)
	sdCBuiltin        = regexp.MustCompile(`__builtin_\w+`)
	sdZigForcedInline = regexp.MustCompile(`\binline\s+(fn|while)\b|\.always_inline`)
	sdRustPtrArith    = regexp.MustCompile(`\.(add|sub|offset|byte_add|byte_sub|byte_offset|wrapping_offset)\(`)
	sdRustLenAssert   = regexp.MustCompile(`assert!\([^;]*<=[^;]*\.len\(\)`)
	sdRustFnStart     = regexp.MustCompile(`(?m)\bfn\s+\w+`)
	sdGoUnsafe        = regexp.MustCompile(`(?m)^\s*(import\s+)?(\w+\s+)?"unsafe"\s*$`)
	sdGoDirective     = regexp.MustCompile(`(?m)^\s*//go:`)
	sdMkScalarNote    = regexp.MustCompile(`(?i)\bmk\b[^.;]*\bscalar\b|str\.From`)
)

// sdViolations is simdViolations (findings_simd_test.go) plus tools/bench's
// sameBans for Go and the alignment refusal, for one source of language
// lang.
func sdViolations(lang, code string) []string {
	var out []string
	switch lang {
	case "rust":
		if sdRustUnsafeItem.MatchString(code) {
			out = append(out, "unsafe beyond the blocks std::arch needs")
		}
		if sdRustTargetAttr.MatchString(code) {
			out = append(out, "#[target_feature]")
		}
		for _, m := range sdRustPtrArith.FindAllStringIndex(code, -1) {
			body := code[:m[0]]
			if fns := sdRustFnStart.FindAllStringIndex(body, -1); len(fns) > 0 {
				body = body[fns[len(fns)-1][0]:]
			}
			if !sdRustLenAssert.MatchString(body) {
				out = append(out, "pointer arithmetic with no bound asserted before it in its fn")
				break
			}
		}
	case "zig":
		if sdZigForcedInline.MatchString(code) {
			out = append(out, "forced inlining or unrolling")
		}
	case "c":
		for _, b := range sdCBuiltin.FindAllString(code, -1) {
			if b != "__builtin_ctz" {
				out = append(out, "a __builtin_ hint ("+b+")")
				break
			}
		}
	case "go":
		if sdGoUnsafe.MatchString(code) {
			out = append(out, `import "unsafe"`)
		}
		if sdGoDirective.MatchString(code) {
			out = append(out, "a //go: directive")
		}
	}
	if !strings.Contains(code, "buffer not 64-byte aligned") {
		out = append(out, "no 64-byte alignment refusal")
	}
	return out
}

// sdRow is the part of a bench.json this check reads.
type sdRow struct {
	Name     string            `json:"name"`
	Table    string            `json:"table"`
	Sources  map[string]string `json:"sources"`
	Notes    []string          `json:"notes"`
	Variants []struct {
		Name          string            `json:"name"`
		Checksum      string            `json:"checksum"`
		QuickChecksum string            `json:"quick_checksum"`
		Skip          map[string]string `json:"skip"`
	} `json:"variants"`
}

// TestSTDSIMD_LaneRowsMeetTheContract runs the SIMD source rules
// over every non-Mk source of std.simd's rows, and the checksum and
// description checks over every row.
func TestSTDSIMD_LaneRowsMeetTheContract(t *testing.T) {
	dirs, err := filepath.Glob(filepath.Join("..", "tests", "bench-lanes", "std-simd", "*", "bench.json"))
	if err != nil || len(dirs) < 8 {
		t.Fatalf("%d lane rows (%v), want eight", len(dirs), err)
	}
	seen := 0
	for _, path := range dirs {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var r sdRow
		if err := json.Unmarshal(b, &r); err != nil {
			t.Errorf("%s: %v", path, err)
			continue
		}
		dir := filepath.Dir(path)
		if r.Table != "simd" {
			t.Errorf("%s: table %q, want simd", path, r.Table)
		}
		for _, v := range r.Variants {
			if v.Checksum == "" || v.QuickChecksum == "" {
				t.Errorf("%s %s: no checksum or quick_checksum", path, v.Name)
			}
		}
		var callsSimd bool
		for _, lang := range slices.Sorted(func(yield func(string) bool) {
			for k := range r.Sources {
				if !yield(k) {
					return
				}
			}
		}) {
			src := filepath.Join(dir, filepath.FromSlash(r.Sources[lang]))
			if strings.HasPrefix(lang, "mk") {
				mks, _ := filepath.Glob(filepath.Join(src, "*.mk"))
				for _, m := range mks {
					if c, err := os.ReadFile(m); err == nil && strings.Contains(string(c), "import std.simd") {
						callsSimd = true
					}
				}
				continue
			}
			code, err := os.ReadFile(src)
			if err != nil {
				t.Errorf("%s: %s: %v", path, lang, err)
				continue
			}
			seen++
			if vs := sdViolations(lang, string(code)); len(vs) > 0 {
				t.Errorf("%s (%s): %s", src, lang, strings.Join(vs, "; "))
			}
		}
		if callsSimd {
			for _, n := range r.Notes {
				if sdMkScalarNote.MatchString(n) {
					t.Errorf("%s: a note calls the mk column scalar or str.From, but mk/ calls std.simd: %.160q", path, n)
				}
			}
		}
	}
	if seen < 20 {
		t.Errorf("%d non-Mk sources checked, want the 15 C, Rust and Zig and 5 Go of the new rows and the reused ones", seen)
	}
}

// TestSTDSIMD_PlantedViolations: each rule catches its planted case in
// a copy of a row's source, so the check above cannot pass by matching
// nothing.
func TestSTDSIMD_PlantedViolations(t *testing.T) {
	base := filepath.Join("..", "tests", "bench-lanes", "std-simd", "count")
	read := func(name string) string {
		b, err := os.ReadFile(filepath.Join(base, name))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	rs, c, zig := read("count.rs"), read("count.c"), read("count.zig")
	gosrc := read(filepath.Join("testdata", "count.go"))
	for _, tc := range []struct {
		lang, code, want string
	}{
		{"rust", rs, ""},
		{"c", c, ""},
		{"zig", zig, ""},
		{"go", gosrc, ""},
		{"rust", "#[target_feature(enable = \"avx2\")]\n" + rs, "#[target_feature]"},
		{"rust", rs + "\nunsafe fn f() {}\n", "unsafe beyond"},
		{"rust", rs + "\nfn g(p: *const u8) -> *const u8 { unsafe { p.add(1) } }\n", "pointer arithmetic"},
		{"c", c + "\nint h(int x) { return __builtin_expect(x, 1); }\n", "__builtin_expect"},
		{"zig", zig + "\ninline fn k() void {}\n", "forced inlining"},
		{"go", strings.Replace(gosrc, "import (", "import (\n\t\"unsafe\"", 1), `import "unsafe"`},
		{"go", gosrc + "\n//go:noinline\nfunc z() {}\n", "//go:"},
		{"c", strings.ReplaceAll(c, "buffer not 64-byte aligned", "x"), "BS-4"},
		{"note", "mk and go are scalar std validators (str.From, utf8.Valid), labelled", "note"},
	} {
		if tc.lang == "note" {
			if !sdMkScalarNote.MatchString(tc.code) {
				t.Errorf("the scalar-note rule missed %q", tc.code)
			}
			continue
		}
		got := strings.Join(sdViolations(tc.lang, tc.code), "; ")
		if tc.want == "" && got != "" || tc.want != "" && !strings.Contains(got, tc.want) {
			t.Errorf("%s with %q planted: violations %q", tc.lang, tc.want, got)
		}
	}
}
