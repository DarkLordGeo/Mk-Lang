// Why: RESULTS.md is read by people, so a stray pipe or a wrong budget
// verdict corrupts what they see.
// What: tests of cell text, the check-budget columns and renderBudgets.
// How: renders small hand-made results and checks the markdown.

package main

import (
	"slices"
	"strings"
	"testing"
)

// TestCellTextKeepsTablesWhole: a multi-line diagnostic with pipes in it
// (an excluded cell's reason) renders as one cell on one line.
func TestCellTextKeepsTablesWhole(t *testing.T) {
	in := "failed: exit status 1: hello.mk:5:9: E0304\n    |     try Print(mut out)\n    |         ^^^^"
	got := cellText(in)
	if strings.Contains(got, "\n") {
		t.Fatalf("newline survived: %q", got)
	}
	if strings.Count(got, "|") != strings.Count(got, `\|`) {
		t.Fatalf("unescaped pipe: %q", got)
	}
	if !strings.HasPrefix(got, "failed: exit status 1: hello.mk:5:9: E0304 \\| try Print") {
		t.Fatalf("got %q", got)
	}
	long := cellText(strings.Repeat("x", 500))
	if len(long) != 203 || !strings.HasSuffix(long, "...") {
		t.Fatalf("long text: %d bytes", len(long))
	}
}

// TestBudgetColumns: the check-budget columns stay out of the
// cross-language tables, are built only when named, carry their flag and
// the MK_STD of the run, and are classed by what they remove.
func TestBudgetColumns(t *testing.T) {
	r := &row{Name: "r", Dir: "D", Table: "same", Overflow: "never", Sources: map[string]string{"mk": "mk", "c": "r.c"}}
	for _, c := range rowColumns(r) {
		if c.Budget {
			t.Fatalf("rowColumns has %s", c.ID)
		}
	}
	var ids []string
	for _, c := range allRowColumns(r) {
		ids = append(ids, c.ID)
	}
	if !slices.Contains(ids, "mk-nobounds") || !slices.Contains(ids, "mk-noovf") {
		t.Fatalf("allRowColumns %v", ids)
	}
	nb, _ := columnByID("mk-nobounds")
	no, _ := columnByID("mk-noovf")
	if wantColumn(nb, nil) || !wantColumn(nb, func(c column) bool { return c.ID == "mk-nobounds" }) {
		t.Fatal("budget column selection")
	}
	if mk, _ := columnByID("mk"); !wantColumn(mk, nil) {
		t.Fatal("mk not built by default")
	}
	if nb.class(r) != "unchecked" || no.class(r) != "bounds" {
		t.Fatalf("classes %s %s", nb.class(r), no.class(r))
	}
	tc := &toolchain{Mk: "mk", Work: "W", MkStd: `S:\std`}
	for _, c := range []column{nb, no} {
		cmd, err := tc.command(r, c, "o.exe")
		if err != nil {
			t.Fatal(err)
		}
		s := cmd.String()
		for _, w := range []string{budgetFlags[c.ID], "--release", "--pgo=off", "--no-cache", `MK_STD=S:\std`} {
			if !strings.Contains(s, w) {
				t.Errorf("%s: %q lacks %q", c.ID, s, w)
			}
		}
	}
	mk, _ := columnByID("mk")
	cmd, _ := (&toolchain{Mk: "mk", Work: "W"}).command(r, mk, "o.exe")
	if s := cmd.String(); strings.Contains(s, "--no-bounds-checks") || strings.Contains(s, "--no-overflow-checks") || !slices.Contains(cmd.Env, "MK_STD=") {
		t.Errorf("mk column: %q env %q", s, cmd.Env)
	}
}

// TestRenderBudgets: the prices are checked over unchecked, their medians
// are judged against 3 and 5 percent, and a missing cell is a dash.
func TestRenderBudgets(t *testing.T) {
	times := func(m float64) []float64 { return []float64{m * 0.99, m, m * 1.01} }
	cellOf := func(row, col string, m float64) cell {
		c := cell{Row: row, Table: "same", Col: col, Times: times(m)}
		c.Summary = summarize(c.Times)
		return c
	}
	res := &results{Runs: []facts{{Commit: "abc", MkBinary: "mk.exe", MkStd: `S:\std`, Started: "2026-11-07T03:00:00Z"}}, Kind: "budgets", Sweeps: 3,
		Rows: []rowInfo{{ID: "a", Table: "same", Overflow: "never"}, {ID: "b", Table: "same", Overflow: "wraps"}},
		Cells: []cell{cellOf("a", "mk", 1.10), cellOf("a", "mk-nobounds", 1.00), cellOf("a", "mk-noovf", 1.05), cellOf("a", "mk-wrap", 1.04),
			cellOf("b", "mk", 1.00), cellOf("b", "mk-nobounds", 1.00), cellOf("b", "mk-noovf", 1.00)}}
	md := renderBudgets(res, "D")
	for _, w := range []string{"# Check budgets", "results/D/budgets/results.json", "| a | never |", "+10.0% 1.10 [", "+4.8% 1.05 [",
		"median price +5.0% [+3.5%, +6.6%] over 2 rows, over the budget (at most 3%)", "median price +2.4% [+0.9%, +3.9%] over 2 rows (`--no-overflow-checks`), within the budget (at most 5%)",
		"median +5.8% [+3.7%, +7.9%] over 1 rows, inconclusive", "| std (MK_STD) | `S:\\std` |", "| Started | 2026-11-07T03:00:00Z |"} {
		if !strings.Contains(md, w) {
			t.Errorf("budgets section lacks %q:\n%s", w, md)
		}
	}
	if !strings.Contains(md, "| b | wraps | 1.000 s (min 990.0 ms, MAD 10.0 ms, RSD 1.0%) | - | 1.000 s") || !strings.Contains(md, "+0.0% 1.00 [0.98, 1.02] | - |") {
		t.Errorf("row b:\n%s", md)
	}
}
