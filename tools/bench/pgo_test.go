// Why: PGO numbers must never leak into another table, and a changed
// profile must rebuild.
// What: tests of the mk-pgo column's isolation, the stamp, renderPGO and
// the training run.
// How: unit checks on columns and stamps; TestPGOTrainingRun trains a real
// row with an mk it builds.

package main

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// TestPGOColumnIsolated: mk-pgo and the training build are in no table's
// column list, so no other subcommand builds, times or renders them; every
// other Mk build keeps --pgo=off; the PGO table's builds name the profile
// (and hash it) or --pgo-gen.
func TestPGOColumnIsolated(t *testing.T) {
	for _, c := range columns {
		if c.PGO || c.ID == pgoColumn.ID || c.ID == pgoGenColumn.ID {
			t.Fatalf("columns holds %s", c.ID)
		}
	}
	if _, ok := columnByID(pgoColumn.ID); ok {
		t.Fatal("columnByID finds mk-pgo")
	}
	wd, _ := os.Getwd()
	root, err := repoRoot(wd)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := discoverTree(t, root)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		for _, c := range allRowColumns(r) {
			if c.PGO {
				t.Fatalf("%s: allRowColumns has %s", r.id(), c.ID)
			}
		}
	}
	r := &row{Name: "r", Dir: "D", Table: "same", Overflow: "never", Sources: map[string]string{"mk": "mk", "c": "r.c"}}
	tc := &toolchain{Mk: "mk", Work: "W"}
	for _, c := range append(slices.Clone(columns), pgoColumn, pgoGenColumn) {
		if !isMkLang(c.Lang) {
			continue
		}
		r.Sources[c.Lang] = "mk"
		cmd, err := tc.command(r, c, "o.exe")
		if err != nil {
			t.Fatal(err)
		}
		s := cmd.String()
		switch c.ID {
		case pgoColumn.ID:
			prof := filepath.Join("W", "pgo", "r", "r.mkprof")
			if !strings.Contains(s, "--pgo="+prof) || strings.Contains(s, "--pgo=off") || !slices.Equal(cmd.Inputs, []string{prof}) {
				t.Errorf("mk-pgo: %q inputs %q", s, cmd.Inputs)
			}
		case pgoGenColumn.ID:
			if !strings.Contains(s, "--pgo-gen") || strings.Contains(s, "--pgo=") || len(cmd.Inputs) > 0 {
				t.Errorf("mk-pgo-gen: %q inputs %q", s, cmd.Inputs)
			}
		default:
			if !strings.Contains(s, "--pgo=off") || strings.Contains(s, "--pgo-gen") || len(cmd.Inputs) > 0 {
				t.Errorf("%s: %q inputs %q", c.ID, s, cmd.Inputs)
			}
		}
	}
	// pgoRows keeps the same rows with an Mk source only.
	in := []*row{{Name: "a", Table: "same", Sources: map[string]string{"mk": "mk"}}, {Name: "b", Table: "same", Sources: map[string]string{"c": "b.c"}},
		{Name: "c", Table: "idiomatic", Sources: map[string]string{"mk": "mk"}}, {Name: "d", Table: "app", Sources: map[string]string{"mk": "mk"}}}
	if got := pgoRows(in); len(got) != 1 || got[0].Name != "a" {
		t.Errorf("pgoRows: %v", got)
	}
}

// TestPGOStampHashesTheProfile: a new profile at the same path is a changed
// mk-pgo build.
func TestPGOStampHashesTheProfile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "row", "mk", "r.mk"), "// r, Mk\n")
	mk := filepath.Join(dir, "mk.exe")
	writeFile(t, mk, "mk")
	r := &row{Name: "r", Dir: filepath.Join(dir, "row"), Table: "same", Sources: map[string]string{"mk": "mk"}}
	tc := &toolchain{Mk: mk, Work: filepath.Join(dir, "work")}
	cmd, err := tc.command(r, pgoColumn, "o.exe")
	if err != nil {
		t.Fatal(err)
	}
	prof := pgoProfile(tc.Work, r)
	writeFile(t, prof, "one")
	a, err := stamp(r, pgoColumn, cmd)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, prof, "two")
	b, err := stamp(r, pgoColumn, cmd)
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Fatal("the stamp does not change with the profile")
	}
}

// TestRenderPGO: the ratio is mk-pgo over mk with its interval, the median
// is over the rows with both cells, and a failed build is shown excluded.
func TestRenderPGO(t *testing.T) {
	times := func(m float64) []float64 { return []float64{m * 0.99, m, m * 1.01} }
	cellOf := func(row, col string, m float64) cell {
		c := cell{Row: row, Table: "same", Col: col, Times: times(m)}
		c.Summary = summarize(c.Times)
		return c
	}
	bad := cell{Row: "c", Table: "same", Col: "mk-pgo", Excluded: "failed: the link failed: undefined symbol __udivti3"}
	res := &results{Runs: []facts{{Commit: "abc", MkBinary: "mk.exe", Started: "2026-11-17T03:00:00Z"}}, Kind: "pgo", Sweeps: 3,
		Columns: append(slices.Clone(columns), pgoColumn),
		Rows:    []rowInfo{{ID: "a", Table: "same"}, {ID: "b", Table: "same"}, {ID: "c", Table: "same"}},
		Cells:   []cell{cellOf("a", "mk", 1.00), cellOf("a", "mk-pgo", 0.90), cellOf("b", "mk", 2.00), cellOf("b", "mk-pgo", 2.00), cellOf("c", "mk", 1.00), bad}}
	md := renderPGO(res, "D")
	for _, w := range []string{"# PGO\n", "results/D/pgo/results.json", "trained on the row's own input", "| a | 1.000 s", "| 0.90 [", "| b |", "| 1.00 [",
		"| c | 1.000 s (min 990.0 ms, MAD 10.0 ms, RSD 1.0%) | excluded (failed: the link failed: undefined symbol __udivti3) | - |",
		"**Median over 2 rows:** mk-pgo / mk 0.95", "never feeds a cross-language ratio"} {
		if !strings.Contains(md, w) {
			t.Errorf("PGO section lacks %q:\n%s", w, md)
		}
	}
}

// TestPGOTrainingRun: one same row through buildPGO with a real mk: the
// training run writes and merges a profile into the work directory, never
// into the tree, both columns print the row's checksum, and a second call
// neither retrains nor rebuilds.
func TestPGOTrainingRun(t *testing.T) {
	if testing.Short() {
		t.Skip("builds an mk and a same row three ways")
	}
	wd, _ := os.Getwd()
	root, err := repoRoot(wd)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := discoverTree(t, root)
	if err != nil {
		t.Fatal(err)
	}
	rows, err = selectRows(rows, "same", []string{"intsum"})
	if err != nil {
		t.Fatal(err)
	}
	if err := prepareRows(root, rows); err != nil {
		t.Fatal(err)
	}
	tc, err := findToolchain(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	tag := "test-pgo-" + strconv.Itoa(os.Getpid())
	if tc.Mk, err = testMkExe(t, tc, root, tag); err != nil {
		t.Fatal(err)
	}
	var lines []string
	log := func(s string) { lines = append(lines, s) }
	bs := buildPGO(tc, tag, rows, log)
	if len(bs) != 2 || bs[0].Col.ID != "mk" || bs[1].Col.ID != "mk-pgo" {
		t.Fatalf("built %d columns: %v", len(bs), lines)
	}
	res, _, ok := verify(bs, rows[0].runQuick, rows[0].QuickSum)
	if !ok {
		t.Fatalf("verify: %+v %v", res, lines)
	}
	prof := pgoProfile(tc.Work, rows[0])
	if st, err := os.Stat(prof); err != nil || st.Size() == 0 {
		t.Fatalf("no profile at %s: %v", prof, err)
	}
	if s := strayProfiles(root); len(s) > 0 {
		t.Fatalf("a profile in the tree: %v", s)
	}
	lines = nil
	buildPGO(tc, tag, rows, log)
	for _, l := range lines {
		if strings.HasPrefix(l, "trained") || strings.HasPrefix(l, "built") {
			t.Errorf("second call: %s", l)
		}
	}
}
