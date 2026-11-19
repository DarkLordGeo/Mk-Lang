// Why: variant sources, the mk-ctx column, the vendor field and the rows
// under tests/bench-lanes each widen what a row may build, and each widening
// must still meet the benchmark contract.
// What: TestVariantSources, TestCtxColumn, TestVendor,
// TestLaneRowsMeetTheContract and TestDispatchRow.
// How: loads small rows in a temporary tree, and the repository's own rows,
// and checks the built commands and the contract's bans.

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// rowSourceText is the path and text of a row's source of lang, with an Mk
// package's pkg.mk appended; the text is "" when it cannot be read.
func rowSourceText(r *row, lang string) (string, string) {
	p := filepath.Join(r.Dir, r.Sources[lang])
	if isMkLang(lang) {
		dir := p
		p = filepath.Join(dir, r.base()+".mk")
		if _, err := os.Stat(p); err != nil {
			// A tests/bench-lanes package may name its root file otherwise
			// (main.mk).
			ms, _ := filepath.Glob(filepath.Join(dir, "*.mk"))
			ms = slices.DeleteFunc(ms, func(s string) bool { return filepath.Base(s) == "pkg.mk" })
			if len(ms) == 0 {
				return p, ""
			}
			p = ms[0]
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return p, ""
		}
		pkg, _ := os.ReadFile(filepath.Join(dir, "pkg.mk"))
		return p, string(b) + "\n" + string(pkg)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return p, ""
	}
	return p, string(b)
}

// treeRowsAll is every row of the repository, lane rows included.
func treeRowsAll(t *testing.T) []*row {
	t.Helper()
	wd, _ := os.Getwd()
	root, err := repoRoot(wd)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := discoverTree(t, root)
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

// TestVariantSources: a variant may replace a row's source for one
// language (mandelvec's Mk width), and only one the row has.
func TestVariantSources(t *testing.T) {
	var v2, v3 *row
	for _, r := range treeRowsAll(t) {
		switch r.id() {
		case "mandelvec-v2":
			v2 = r
		case "mandelvec-v3":
			v3 = r
		}
	}
	if v2 == nil || v3 == nil {
		t.Fatal("no mandelvec variants")
	}
	if v2.Sources["mk"] != "mkv2" || v3.Sources["mk"] != "mk" || v2.Sources["c"] != v3.Sources["c"] {
		t.Fatalf("variant sources: v2 %v, v3 %v", v2.Sources, v3.Sources)
	}
	// Every tree SIMD variant's Mk cell:
	// every cell of a row prints the same checksum, so a variant that lost
	// its override and fell back to the row's package would pass every
	// run; this table is what catches it.
	wantMk := map[string]string{
		"indexbyte-l1v2": "mkfixv2", "indexbyte-l3v2": "mkfixv2", "indexbyte-dramv2": "mkfixv2",
		"indexbyte-l1v3": "mkfix", "indexbyte-l3v3": "mkfix", "indexbyte-dramv3": "mkfix",
		"dot-l1v2": "mkfix", "dot-l3v2": "mkfix", "dot-dramv2": "mkfix",
		"dot-l1v3": "mkfix", "dot-l3v3": "mkfix", "dot-dramv3": "mkfix",
		"validutf8-l1v2": "mk", "validutf8-l3v2": "mkfixv2", "validutf8-dramv2": "mkfixv2",
		"validutf8-l1v3": "mkfix", "validutf8-l3v3": "mkfix", "validutf8-dramv3": "mkfix",
		"mandelvec-v2": "mkv2", "mandelvec-v3": "mk",
	}
	seen := 0
	for _, r := range treeRowsAll(t) {
		if r.Lane != "" || r.Table != "simd" {
			continue
		}
		want, ok := wantMk[r.id()]
		if !ok {
			t.Errorf("%s: a SIMD variant the table of Mk cells does not name", r.id())
			continue
		}
		seen++
		if got := r.Sources["mk"]; got != want {
			t.Errorf("%s: its Mk cell is %q, want %q", r.id(), got, want)
		}
	}
	if seen != len(wantMk) {
		t.Errorf("%d of the %d SIMD variants of the table loaded", seen, len(wantMk))
	}
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.c"), []byte("x"), 0o644)
	bad := `{"name": "x", "table": "simd", "sources": {"c": "a.c"}, "variants": [{"name": "v", "args": ["1"], "quick": ["1"], "sources": {"mk": "a.c"}}]}`
	os.WriteFile(filepath.Join(dir, "bench.json"), []byte(bad), 0o644)
	if _, err := loadRow(filepath.Join(dir, "bench.json"), ""); err == nil || !strings.Contains(err.Error(), "replaces no source") {
		t.Fatalf("a variant source for a language the row lacks loaded: %v", err)
	}
}

// TestCtxColumn: a row with an "mk-ctx" source gets the mk-ctx column,
// built by mk like the mk column from its own package, and rendered with
// mk's toolchain.
func TestCtxColumn(t *testing.T) {
	r := &row{Name: "closuresort", Dir: "D", Sources: map[string]string{"mk": "mk", "mk-ctx": "mkctx"}}
	var ids []string
	for _, c := range rowColumns(r) {
		ids = append(ids, c.ID)
		tc := &toolchain{Mk: "mk.exe", Work: "W"}
		cmd, err := tc.command(r, c, "out.exe")
		if err != nil || cmd.Args[0] != "mk.exe" || cmd.Args[len(cmd.Args)-1] != filepath.Join("D", r.Sources[c.Lang]) {
			t.Fatalf("%s command %v %v", c.ID, cmd.Args, err)
		}
	}
	if !slices.Equal(ids, []string{"mk", "mk-ctx"}) {
		t.Fatalf("columns %v", ids)
	}
	if langKey(langOf("mk-ctx")) != "mk" {
		t.Fatal("mk-ctx is not rendered with mk's toolchain")
	}
}

// TestVendor (Requests 11 and 17): absent vendor files keep a row from
// loading, with the reason; present ones join the C column's clang line and
// the Mk package's build copy; the two sides' flags must agree on -march,
// builtins and -D but for the named exceptions.
func TestVendor(t *testing.T) {
	ev := t.TempDir()
	t.Setenv("MK_EVIDENCE", ev)
	wd, _ := os.Getwd()
	root, err := repoRoot(wd)
	if err != nil {
		t.Fatal(err)
	}
	rowDir := filepath.Join(root, "tests", "bench") // any directory of the repository
	v := &vendorSpec{Dir: "vendor/x", Files: []string{"x.c", "x.h"}}
	if err := resolveVendor(v, rowDir); err == nil || !strings.Contains(err.Error(), "absent") {
		t.Fatalf("missing vendor files: %v", err)
	}
	if err := resolveVendor(&vendorSpec{Dir: "../x", Files: []string{"x.c"}}, rowDir); err == nil {
		t.Fatal("a vendor dir outside the evidence directory loaded")
	}
	os.MkdirAll(filepath.Join(ev, "vendor", "x"), 0o755)
	os.WriteFile(filepath.Join(ev, "vendor", "x", "x.c"), []byte("int x;"), 0o644)
	os.WriteFile(filepath.Join(ev, "vendor", "x", "x.h"), []byte(""), 0o644)
	v.CFlags = []string{"-DA=1", "-fno-builtin"}
	if err := resolveVendor(v, rowDir); err != nil {
		t.Fatal(err)
	}
	pkgDir := filepath.Join(t.TempDir(), "mk")
	os.MkdirAll(pkgDir, 0o755)
	os.WriteFile(filepath.Join(pkgDir, "pkg.mk"), []byte(`c_sources: [pkg.CSource(path: "x.c", flags: ["-DA=1", "-march=x86-64-v2"])],`), 0o644)
	r := &row{Name: "v", Dir: filepath.Dir(pkgDir), Sources: map[string]string{"mk": "mk", "c": "main.c"}, Vendor: v}
	tc := &toolchain{Mk: "mk.exe", Clang: "clang.exe", Work: t.TempDir()}
	cmd, err := tc.command(r, column{ID: "c", Lang: "c"}, "o.exe")
	if err != nil || !slices.Contains(cmd.Args, "-DA=1") || !slices.Contains(cmd.Args, filepath.Join(v.Path, "x.c")) || !slices.Contains(cmd.Args, "-I"+v.Path) {
		t.Fatalf("c command %v %v", cmd.Args, err)
	}
	cmd, err = tc.command(r, column{ID: "mk", Lang: "mk"}, "o.exe")
	if err != nil {
		t.Fatal(err)
	}
	copyDir := cmd.Args[len(cmd.Args)-1]
	for _, f := range []string{"pkg.mk", "x.c", "x.h"} {
		if _, err := os.Stat(filepath.Join(copyDir, f)); err != nil || !strings.HasPrefix(copyDir, tc.Work) {
			t.Fatalf("build copy %s lacks %s: %v", copyDir, f, err)
		}
	}
	if err := checkVendorFlags(r); err != nil {
		t.Fatalf("agreeing flags refused: %v", err)
	}
	if d := vendorFlagDiff(`pkg.CSource(path: "x.c", flags: ["-DA=1", "-DB", "-march=x86-64-v3"])`, []string{"-O3", "-march=x86-64-v2", "-DA=1", "-fno-builtin"}, nil); len(d) != 3 {
		t.Fatalf("diff %q, want -DB and both -march", d)
	}
	if d := vendorFlagDiff(`pkg.CSource(path: "x.c", flags: ["-DOS=1", "-march=x86-64-v2"])`, []string{"-O3", "-march=x86-64-v2", "-DOS=2", "-fno-builtin"}, []string{"OS"}); len(d) != 0 {
		t.Fatalf("named exception refused: %q", d)
	}
}

// mkCheckedOp is an Mk operator that traps on overflow (+ - * without %,
// += -= *=) or a checked conversion (Cast[). Float arithmetic matches too,
// so the rule that uses it applies only to rows whose overflow class is
// "never" (checked integer types).
var mkCheckedOp = regexp.MustCompile(`[^%+\-*/<>=!&|^]\s[+\-*]\s|[+\-*]=\s|Cast\[`)

// TestLaneRowsMeetTheContract: the checks that TestTreeRowsMeetTheContract
// and the row-ban tests apply to tests/bench rows, over the rows under
// tests/bench-lanes: every source opens, the tuning bans of its table, a
// GB/s row's buffers aligned or labelled with a reps-0 baseline, and no
// mk-wrap skip where Mk checks arithmetic that the row's C column does not.
// A lane row that fails here is not folded into RESULTS.md; the test names
// it.
func TestLaneRowsMeetTheContract(t *testing.T) {
	wd, _ := os.Getwd()
	root, _ := repoRoot(wd)
	lr, err := loadLaneReview(root)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, r := range treeRowsAll(t) {
		if r.Lane == "" || r.Broken != "" {
			continue
		}
		n++
		v := lr.verdictFor(r)
		if v == nil {
			t.Errorf("%s: no entry in tests/bench/lane-review.json", r.id())
			continue
		}
		fail := func(format string, args ...any) {
			msg := fmt.Sprintf(format, args...)
			for _, e := range v.Exempt {
				if strings.Contains(msg, e) {
					return
				}
			}
			t.Error(msg)
		}
		if r.Checksum == "" || r.QuickSum == "" {
			fail("%s: checksums not recorded", r.id())
		}
		for lang := range r.Sources {
			p, text := rowSourceText(r, lang)
			if text == "" {
				fail("%s %s: cannot read %s", r.id(), lang, p)
				continue
			}
			if r.Table == "same" {
				for _, v := range contractViolations(lang, text) {
					fail("%s %s: %s uses %s, which the same-algorithm contract forbids", r.id(), lang, p, v)
				}
			} else {
				for _, v := range rowViolations(r, lang, text) {
					fail("%s %s: %s uses %s, which the contract forbids outside the SIMD layer", r.id(), lang, p, v)
				}
			}
			if r.Table == "simd" && r.Bytes > 0 {
				code := strings.ReplaceAll(text, "\n// ", " ")
				aligned := strings.Contains(text, "buffer not 64-byte aligned")
				labelled := strings.Contains(code, "labelled alignment exception") && strings.Contains(strings.Join(r.Notes, " "), "labelled alignment exception")
				if !aligned && !labelled && (!isMkLang(lang) || strings.Contains(stripComments(text), "simd")) {
					fail("%s %s: %s neither aligns its buffers with the refusal nor is labelled alignment exception", r.id(), lang, p)
				}
			}
		}
		if r.Table == "simd" && r.Bytes > 0 && len(r.Baseline) == 0 {
			fail("%s: a GB/s row with no reps-0 baseline", r.id())
		}
		if why, ok := r.Skip["mk-wrap"]; ok && r.Table == "same" && r.Overflow == "never" {
			if _, hasC := r.Sources["c"]; hasC {
				_, text := rowSourceText(r, "mk")
				if mkCheckedOp.MatchString(stripComments(text)) {
					fail("%s: skips mk-wrap (%q) though its Mk source has checked arithmetic and the row has a C column (PCG2-R4)", r.id(), why)
				}
			}
		}
	}
	if n < 50 {
		t.Fatalf("checked %d lane rows", n)
	}
}

// TestDispatchRow: a SIMD row with a
// dispatch field may use C's __attribute__((target("..."))) and Rust's
// #[target_feature] on a safe fn, and nothing else the SIMD table bans; a
// SIMD row without one may use neither. Every tree row with a dispatch field
// is a SIMD row built at x86-64-v2, says for every language with a source
// how it dispatches, names no language without one, and says in its notes
// that it is the dispatch row.
func TestDispatchRow(t *testing.T) {
	disp := &row{Name: "d", Table: "simd", Dispatch: map[string]string{"c": "x", "rust": "y"}}
	fixed := &row{Name: "f", Table: "simd"}
	cases := []struct {
		r    *row
		lang string
		src  string
		want string // a part of the violation, "" when the source passes
	}{
		{disp, "c", "__attribute__((target(\"avx512bw\"))) static int f(void) { return 0; }\n", ""},
		{disp, "c", "__attribute__((target(\"xsave\"))) static int f(void) { return 0; }\n", ""},
		{disp, "c", "static int f(unsigned long long m) { return __builtin_ctzll(m); }\n", ""},
		{disp, "c", "__attribute__((hot)) static int f(void) { return 0; }\n", "attribute"},
		{disp, "c", "static int f(void) { return __builtin_expect(1, 1); }\n", "__builtin_"},
		{fixed, "c", "__attribute__((target(\"avx2\"))) static int f(void) { return 0; }\n", "attribute"},
		{disp, "rust", "#[target_feature(enable = \"avx2\")]\nfn f() {}\nfn g() { if is_x86_feature_detected!(\"avx2\") { unsafe { f() } } }\n", ""},
		{disp, "rust", "#[target_feature(enable = \"avx2\")]\nunsafe fn f() {}\n", "unsafe fn"},
		{disp, "rust", "#[inline(always)]\nfn f() {}\n", "inlining hint"},
		{fixed, "rust", "#[target_feature(enable = \"avx2\")]\nfn f() {}\n", "target_feature"},
	}
	for _, c := range cases {
		vs := rowViolations(c.r, c.lang, c.src)
		got := strings.Join(vs, "; ")
		if (c.want == "" && len(vs) > 0) || (c.want != "" && !strings.Contains(got, c.want)) {
			t.Errorf("row %s, %s %q: violations %q, want %q", c.r.Name, c.lang, c.src, got, c.want)
		}
	}
	for _, r := range treeRowsAll(t) {
		if len(r.Dispatch) == 0 {
			continue
		}
		if r.Table != "simd" || r.cpu() != "x86-64-v2" {
			t.Errorf("%s: a dispatch row is a SIMD row built at x86-64-v2, not table %q at %s", r.id(), r.Table, r.cpu())
		}
		for lang := range r.Sources {
			if r.Dispatch[lang] == "" {
				t.Errorf("%s: %s has a source and no dispatch label", r.id(), lang)
			}
		}
		for lang := range r.Dispatch {
			if _, ok := r.Sources[lang]; !ok {
				t.Errorf("%s: dispatch names %s, which has no source", r.id(), lang)
			}
		}
		if !strings.Contains(strings.Join(r.Notes, " "), "labelled dispatch row") {
			t.Errorf("%s: the notes do not say it is the labelled dispatch row", r.id())
		}
	}
}
