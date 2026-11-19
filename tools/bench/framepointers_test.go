// Why: the frame-pointer measurement decides a default, so its arithmetic,
// flags and row choice must be right.
// What: tests of geoMean, fpDefault's threshold, the two columns' flags and
// fpRows.
// How: calls the functions on fixed inputs; nothing is built or run.

package main

import (
	"math"
	"slices"
	"strings"
	"testing"
)

func TestGeoMean(t *testing.T) {
	if g := geoMean([]float64{1, 4}); math.Abs(g-2) > 1e-12 {
		t.Fatalf("geoMean(1,4) = %v", g)
	}
	if g := geoMean([]float64{1.01, 1.01, 1.01}); math.Abs(g-1.01) > 1e-12 {
		t.Fatalf("geoMean = %v", g)
	}
	if !math.IsNaN(geoMean(nil)) || !math.IsNaN(geoMean([]float64{1, 0})) {
		t.Fatal("geoMean of nothing or a zero is a number")
	}
}

// TestFPSummary: each row's price is its median paired ratio, excluded rows
// count for nothing, and the default follows the 2% threshold.
func TestFPSummary(t *testing.T) {
	cs := []fpCell{
		{Row: "a", Ratios: []float64{1.00, 1.01, 1.50}}, // median 1.01
		{Row: "b", Ratios: []float64{1.03, 1.03}},       // 1.03
		{Row: "c", Excluded: "build failed"},
	}
	prices, g := fpSummary(cs)
	if !slices.Equal(prices, []float64{1.01, 1.03}) {
		t.Fatalf("prices %v", prices)
	}
	if want := math.Sqrt(1.01 * 1.03); math.Abs(g-want) > 1e-12 {
		t.Fatalf("geomean %v, want %v", g, want)
	}
	if !strings.HasPrefix(fpDefault(g, 0), "on") {
		t.Fatalf("%v: %s", g, fpDefault(g, 0))
	}
	if !strings.HasPrefix(fpDefault(1.0201, 0), "off") || !strings.HasPrefix(fpDefault(1.02, 0), "on") {
		t.Fatal("the threshold is not at most 2%")
	}
}

// TestFPFlags: the two frame-pointer columns build the row's mk source with the
// explicit switch, release and no cache, and no other column takes it.
func TestFPFlags(t *testing.T) {
	r := &row{Name: "x", Table: "same", Dir: "D", Sources: map[string]string{"mk": "mk"}}
	tc := &toolchain{Mk: "mk.exe", Work: "W", versions: map[string]string{}}
	for col, flag := range map[column]string{fpOffColumn: "--frame-pointers=off", fpOnColumn: "--frame-pointers=on"} {
		c, err := tc.command(r, col, "out.exe")
		if err != nil {
			t.Fatal(err)
		}
		args := strings.Join(c.Args, " ")
		if !strings.Contains(args, flag) || !strings.Contains(args, "--release") || strings.Count(args, "--frame-pointers") != 1 {
			t.Fatalf("%s: %s", col.ID, args)
		}
	}
	mk, _ := columnByID("mk")
	c, err := tc.command(r, mk, "out.exe")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(c.Args, " "), "frame-pointers") {
		t.Fatalf("the mk column takes the switch: %v", c.Args)
	}
	for _, x := range columns {
		if x.ID == fpOffColumn.ID || x.ID == fpOnColumn.ID {
			t.Fatalf("%s is in columns: every other table would build it", x.ID)
		}
	}
}

func TestParsePerfInstructions(t *testing.T) {
	ok := "# started on Wed\n\n123456789,,instructions:u,1000,100.00,,\n"
	if v, err := parsePerfInstructions(ok); err != nil || v != 123456789 {
		t.Fatalf("%v %v", v, err)
	}
	for _, bad := range []string{"<not supported>,,instructions:u,0,100.00,,\n", "<not counted>,,instructions:u,0,0.00,,\n", "nothing\n"} {
		if _, err := parsePerfInstructions(bad); err == nil {
			t.Fatalf("%q accepted", bad)
		}
	}
}

func TestFPRows(t *testing.T) {
	all := []*row{
		{Name: "a", Table: "same", Sources: map[string]string{"mk": "mk", "c": "c"}},
		{Name: "b", Table: "same", Sources: map[string]string{"c": "c"}},
		{Name: "c", Table: "idiomatic", Sources: map[string]string{"mk": "mk"}},
		{Name: "d", Table: "same", Sources: map[string]string{"mk": "mk"}, Broken: "x"},
		{Name: "e", Lane: "std-dyn", Table: "same", Sources: map[string]string{"mk": "mk"}},
	}
	rs, err := fpRows(all, "same", nil)
	if err != nil || len(rs) != 1 || rs[0].Name != "a" {
		t.Fatalf("%v %v", rs, err)
	}
	// A tests/bench-lanes row is measured only when named.
	if rs, err := fpRows(all, "same", []string{"std-dyn/e"}); err != nil || len(rs) != 1 {
		t.Fatalf("named lane row: %v %v", rs, err)
	}
}

// TestFPPairsABBA: even pairs run off then on, odd pairs on then off, and
// each pair's values land on their own side.
func TestFPPairsABBA(t *testing.T) {
	off, on := &built{Col: fpOffColumn}, &built{Col: fpOnColumn}
	var order []string
	n := 0
	measure := func(b *built) (float64, error) {
		order = append(order, b.Col.ID)
		n++
		if b == on {
			return 2, nil
		}
		return 1, nil
	}
	var c fpCell
	if err := fpPairs(&c, 4, off, on, measure); err != nil {
		t.Fatal(err)
	}
	want := []string{"mk-fpoff", "mk-fpon", "mk-fpon", "mk-fpoff", "mk-fpoff", "mk-fpon", "mk-fpon", "mk-fpoff"}
	if !slices.Equal(order, want) {
		t.Fatalf("order %v, want %v", order, want)
	}
	for i := range c.Ratios {
		if c.Off[i] != 1 || c.On[i] != 2 || c.Ratios[i] != 2 {
			t.Fatalf("pair %d: off %v on %v ratio %v", i, c.Off[i], c.On[i], c.Ratios[i])
		}
	}
}

// TestFPNoVerdictWithExcluded: a mean that leaves rows out gives no default.
func TestFPNoVerdictWithExcluded(t *testing.T) {
	if v := fpDefault(1.0, 1); !strings.HasPrefix(v, "no verdict") {
		t.Fatalf("%s", v)
	}
	var b strings.Builder
	renderFP(&b, []fpCell{{Row: "a", Ratios: []float64{1}}, {Row: "b", Excluded: "build failed"}}, "", "mk-x", "time", "test")
	if !strings.Contains(b.String(), "no verdict") || !strings.Contains(b.String(), "mk-x (the mk that cross-built") {
		t.Fatalf("%s", b.String())
	}
}
