// Why: Mk's checks have run-time budgets (bounds checks at most 3% median,
// overflow checks at most 5% median, both on the same-algorithm table), and
// only a table of Mk against itself can show what each check costs.
//
// What: the check-budget table of `tools/bench publish -budgets`, written to
// results/<date>/budgets: the budgets, medianPriceCI and budgetVerdict.
//
// How: each same-algorithm row's Mk program is built three ways by one mk
// executable, as built (checks on), with --no-bounds-checks and with
// --no-overflow-checks, plus the row's mk-wrap program where it has one. The
// price of a kind of check is the ratio of medians of the checked program to
// the one without it. The table never feeds a cross-language ratio.

package main

import (
	"fmt"
	"math"
	"math/rand/v2"
	"strings"
)

// budgetColumnIDs is the columns of the check-budget table, in table order.
var budgetColumnIDs = []string{"mk", "mk-wrap", "mk-nobounds", "mk-noovf"}

// The run-time budgets, as ratios of medians.
const (
	boundsBudget   = 1.03
	overflowBudget = 1.05
)

// budgetPrice is one row's price of one kind of check.
type budgetPrice struct {
	Row      string
	Price    ratioEntry // checked / unchecked
	Num, Den []float64  // the two cells' times, for medianPriceCI
}

// budgetPrices is, for each row of res with both cells, the ratio of the
// checked column's median to the reference column's, with its interval.
func budgetPrices(res *results, checked, ref string) []budgetPrice {
	var out []budgetPrice
	for _, r := range res.Rows {
		cs := rowCells(res, r.ID)
		a, b := findCell(cs, checked), findCell(cs, ref)
		if a == nil || b == nil || a.Excluded != "" || b.Excluded != "" || len(a.Times) == 0 || len(b.Times) == 0 {
			continue
		}
		out = append(out, budgetPrice{Row: r.ID, Price: ratioTo(a, b), Num: a.Times, Den: b.Times})
	}
	return out
}

func medianPrice(ps []budgetPrice) float64 {
	if len(ps) == 0 {
		return math.NaN()
	}
	var xs []float64
	for _, p := range ps {
		xs = append(xs, p.Price.Ratio)
	}
	return median(xs)
}

// medianPriceCI is the 95 percent interval of the median price over the
// rows. The rows are the table's (the budget is set on the same-algorithm
// table), so they stay fixed: each resample redraws every
// row's two cells' times with replacement, as ratioCI does for one row, and
// takes the median of the rows' resampled ratios of medians.
func medianPriceCI(ps []budgetPrice) interval {
	if len(ps) == 0 {
		return interval{math.NaN(), math.NaN()}
	}
	r := rand.New(rand.NewPCG(bootstrapSeed, uint64(len(ps))))
	draw := func(xs, buf []float64) float64 {
		for i := range buf {
			buf[i] = xs[r.IntN(len(xs))]
		}
		return median(buf)
	}
	bufs := make([][2][]float64, len(ps))
	for i, p := range ps {
		bufs[i] = [2][]float64{make([]float64, len(p.Num)), make([]float64, len(p.Den))}
	}
	rows := make([]float64, len(ps))
	meds := make([]float64, bootstrapResamples)
	for k := range meds {
		for i, p := range ps {
			rows[i] = draw(p.Num, bufs[i][0]) / draw(p.Den, bufs[i][1])
		}
		meds[k] = median(rows)
	}
	return percentiles(meds)
}

// budgetVerdict classes a median price against its budget by its interval,
// not its point (bars and gates cite the class, not the point ratio):
// within when the whole interval is below the budget, over when it
// is above, otherwise inconclusive.
func budgetVerdict(m float64, ci interval, budget float64) string {
	if math.IsNaN(m) || math.IsNaN(ci.Lo) {
		return "not measured"
	}
	limit := fmt.Sprintf("at most %.0f%%", (budget-1)*100)
	switch classify(ci, budget) {
	case "within":
		return "within the budget (" + limit + "): the whole interval is below it"
	case "over":
		return "over the budget (" + limit + "): the whole interval is above it"
	}
	return "inconclusive against the budget (" + limit + "): the interval contains it"
}

// fmtPctCI is a median price with its interval, in percent.
func fmtPctCI(m float64, ci interval) string {
	if math.IsNaN(m) {
		return "-"
	}
	return fmt.Sprintf("%s [%s, %s]", fmtPct(m), fmtPct(ci.Lo), fmtPct(ci.Hi))
}

func fmtPct(r float64) string {
	if math.IsNaN(r) {
		return "-"
	}
	return fmt.Sprintf("%+.1f%%", (r-1)*100)
}

// renderBudgets is the check-budget section of RESULTS.md.
func renderBudgets(res *results, date string) string {
	var w strings.Builder
	p := func(format string, a ...any) { fmt.Fprintf(&w, format, a...) }
	p("# Check budgets\n\n")
	p("From `tests/bench/results/%s/budgets/results.json`. Mk against itself on the same-algorithm rows: each row's Mk program built by one mk executable as published (`mk`: bounds checked, overflow trapped), with the hidden benchmark-only flag `--no-bounds-checks` (`mk-nobounds`) and with `--no-overflow-checks` (`mk-noovf`), and the row's wrapping-operator program where it has one (`mk-wrap`). The price of a check is the ratio of medians, checked over unchecked, with its 95%% bootstrap interval. Both flags remove the checks from the whole program, std included, so `mk-noovf` is not the same measurement as `mk-wrap` (which changes only the row's own operators); in a row whose overflow class is `wraps` the row's own arithmetic already wraps, and `mk-noovf` changes only std. Each program still prints the row's checksum, which tools/bench checks before timing it. The `mk` of this section may differ from the one of the same-algorithm table above (see its facts); compare its cells only with each other. This table never feeds a cross-language ratio.\n\n", date)
	for i := range res.Runs {
		renderFacts(&w, res, i)
	}
	bounds := budgetPrices(res, "mk", "mk-nobounds")
	ovf := budgetPrices(res, "mk", "mk-noovf")
	wrap := budgetPrices(res, "mk", "mk-wrap")
	find := func(ps []budgetPrice, id string) string {
		for _, x := range ps {
			if x.Row == id {
				return fmt.Sprintf("%s %s", fmtPct(x.Price.Ratio), fmtRatio(x.Price))
			}
		}
		return "-"
	}
	p("| Row | Overflow class | mk | mk-wrap | mk-nobounds | mk-noovf | Bounds checks: mk / mk-nobounds [95%%] | Overflow checks: mk / mk-noovf [95%%] | mk / mk-wrap [95%%] |\n|---|---|---|---|---|---|---|---|---|\n")
	for _, r := range res.Rows {
		cs := rowCells(res, r.ID)
		p("| %s | %s |", r.ID, orDash(r.Overflow))
		for _, col := range budgetColumnIDs {
			c := findCell(cs, col)
			switch {
			case c == nil:
				p(" - |")
			case c.Excluded != "":
				p(" excluded (%s) |", cellText(c.Excluded))
			default:
				noisy := ""
				if c.Noisy {
					noisy = ", noisy"
				}
				p(" %s (min %s, MAD %s, RSD %.1f%%%s) |", fmtSec(c.Summary.Median), fmtSec(c.Summary.Min), fmtSec(c.Summary.MAD), c.Summary.RSD*100, noisy)
			}
		}
		p(" %s | %s | %s |\n", find(bounds, r.ID), find(ovf, r.ID), find(wrap, r.ID))
	}
	mb, mo, mw := medianPrice(bounds), medianPrice(ovf), medianPrice(wrap)
	cb, co, cw := medianPriceCI(bounds), medianPriceCI(ovf), medianPriceCI(wrap)
	p("\nEach median price carries a 95%% bootstrap interval with the rows fixed (every resample redraws each row's times and takes the median of the rows' ratios), and its verdict cites that interval, not the point.\n")
	p("\n- **Bounds checks:** median price %s over %d rows, %s.\n", fmtPctCI(mb, cb), len(bounds), budgetVerdict(mb, cb, boundsBudget))
	p("- **Overflow checks:** median price %s over %d rows (`--no-overflow-checks`), %s.", fmtPctCI(mo, co), len(ovf), budgetVerdict(mo, co, overflowBudget))
	if len(wrap) > 0 {
		p(" Against the wrapping operators, where a row has them: median %s over %d rows, %s.", fmtPctCI(mw, cw), len(wrap), budgetVerdict(mw, cw, overflowBudget))
	}
	p("\n\nArguments and checksums are the same-algorithm rows' (`bench.json`); every build command is in `results.json` (`builds`).\n\n")
	return w.String()
}
