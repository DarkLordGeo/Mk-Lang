// Why: any change to lowered MIR must be seen and reviewed, and lowering
// must be deterministic.
// What: TestCorpusGolden and TestDeterministic.
// How: the printed MIR of every lowered body of the verdict-ok doc blocks
// and the corpus programs is kept in testdata/corpus.golden, rewritten only
// by -update; the same bytes must come from lowering twice and on 8
// goroutines at once.

package lower

import (
	"flag"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"mk/internal/mir"
	"mk/internal/source"
)

var update = flag.Bool("update", false, "rewrite testdata/*.golden")

// corpusText lowers every corpus unit and prints its bodies, units in
// corpus order, bodies by item key. A doc block's header names it by its
// place among the file's verdict-ok blocks, not by its line, so a prose
// edit that moves a block leaves the golden unchanged.
func corpusText(t *testing.T) string {
	core, pre := corpusPrelude(t), testPrelude(t)
	var sb strings.Builder
	nth := map[string]int{}
	for _, u := range corpusUnits(t) {
		label := u.key
		if doc, _, ok := strings.Cut(u.key, ".md:"); ok {
			nth[doc]++
			label = doc + ".md, ok block " + itoa(nth[doc])
		}
		w, b, ok := corpusWorld(t, core, pre, u)
		if !ok {
			continue
		}
		bodies, _ := lowerUnitsOf(w, b)
		keys := make([]string, 0, len(bodies))
		for k := range bodies {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		sb.WriteString("== " + label + "\n")
		p := &mir.Printer{Types: w.tab, Items: w.items}
		for _, k := range keys {
			sb.WriteString(p.Print(bodies[k]))
		}
	}
	return sb.String()
}

func TestCorpusGolden(t *testing.T) {
	got := corpusText(t)
	path := filepath.Join("testdata", "corpus.golden")
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run go test ./internal/lower -run TestCorpusGolden -update)", err)
	}
	if string(want) != got {
		gl, wl := strings.Split(got, "\n"), strings.Split(string(want), "\n")
		for i := 0; i < len(gl) && i < len(wl); i++ {
			if gl[i] != wl[i] {
				t.Fatalf("corpus.golden differs at line %d:\n got: %s\nwant: %s", i+1, gl[i], wl[i])
			}
		}
		t.Fatalf("corpus.golden differs in length: got %d lines, want %d", len(gl), len(wl))
	}
}

// TestDeterministic: the same input lowers to byte-identical printed MIR
// twice in a row and from 8 goroutines at once.
func TestDeterministic(t *testing.T) {
	src := `error E
F(xs: []int, n: int): E!int {
	defer G()
	var s := 0
	for x in xs {
		s += x
	}
	if n > 3 {
		fail E
	}
	return s
}
G() {
}
`
	w := newWorld(t, true)
	b := w.add("m", opt{}, src)
	b.wantClean(t)
	cfg := w.lowerCfg()
	id := b.item("F")
	body := w.c.Check(b.m, id)
	p := &mir.Printer{Types: w.tab, Items: w.items}
	one := func() string {
		var out string
		if ie := source.Guard("F", source.Span{}, func() { out = p.Print(Lower(cfg, FromCheck(cfg, w.c, body))) }); ie != nil {
			t.Errorf("ICE: %v", ie)
		}
		return out
	}
	first := one()
	if second := one(); second != first {
		t.Fatalf("second lowering differs:\n%s\n%s", first, second)
	}
	var wg sync.WaitGroup
	outs := make([]string, 8)
	for i := range outs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			outs[i] = one()
		}()
	}
	wg.Wait()
	for i, o := range outs {
		if o != first {
			t.Fatalf("goroutine %d differs", i)
		}
	}
}
