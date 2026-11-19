// Why: some rows build their input inside the timed run, so their cells
// must be shown with that set-up time taken out before they are compared.
//
// What: kernelCells, the kernel view of a row with a baseline, and the cell
// lookups renderTables makes (publish.go).
//
// How: a cell with a baseline gets its times minus the baseline's median.

package main

import "slices"

// kernelCells is the kernel view of a row's cells: a cell with a baseline gets
// its times minus the baseline's median, so its medians, intervals and
// ratios are the kernel's alone. kernel reports whether any cell had a
// baseline; then a timed cell without one (which no publish writes) is
// shown excluded rather than compared with kernel times.
func kernelCells(cs []cell) (out []cell, kernel bool) {
	out = slices.Clone(cs)
	for i := range out {
		c := &out[i]
		if c.Excluded != "" || len(c.Base) == 0 || c.BaseSummary == nil {
			continue
		}
		kernel = true
		b := c.BaseSummary.Median
		net := make([]float64, len(c.Times))
		for k, t := range c.Times {
			net[k] = t - b
		}
		c.Times, c.Summary = net, summarize(net)
	}
	if kernel {
		for i := range out {
			c := &out[i]
			if c.Excluded == "" && c.BaseSummary == nil {
				c.Excluded = "no baseline in a row whose other cells have one"
			}
		}
	}
	return out, kernel
}

func rowCells(res *results, id string) []cell {
	var cs []cell
	for _, c := range res.Cells {
		if c.Row == id {
			cs = append(cs, c)
		}
	}
	return cs
}

func findCell(cs []cell, col string) *cell {
	for i := range cs {
		if cs[i].Col == col {
			return &cs[i]
		}
	}
	return nil
}
