// Why: the range pass must remove the overflow and bounds checks of real hot
// loops, not only of the shapes its unit tests build.
// What: TestRange_CountByteVectorLoopUnchecked and
// TestRange_SortLomutoLoopUnchecked, with the IR helpers llBlocks and llCycle.
// How: builds each kernel in release, reads its IR as codegen writes it
// (TestHooks.OnLL), and checks that no block on a loop cycle keeps the check.

package driver

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"

	"mk/internal/types"
)

// llBlocks splits one LLVM function's text into its blocks, by label, and
// each block's successors (the labels its terminator names).
func llBlocks(fn string) (map[string]string, map[string][]string) {
	body := map[string]string{}
	succ := map[string][]string{}
	label := regexp.MustCompile(`^([A-Za-z0-9_.]+):`)
	target := regexp.MustCompile(`label %([A-Za-z0-9_.]+)`)
	cur := ""
	for _, ln := range strings.Split(fn, "\n") {
		if m := label.FindStringSubmatch(ln); m != nil {
			cur = m[1]
			continue
		}
		if cur == "" {
			continue
		}
		body[cur] += ln + "\n"
		for _, m := range target.FindAllStringSubmatch(ln, -1) {
			succ[cur] = append(succ[cur], m[1])
		}
	}
	return body, succ
}

// llCycle is the blocks on a cycle through from: those from reaches that
// reach from.
func llCycle(succ map[string][]string, from string) []string {
	reach := func(a string) map[string]bool {
		seen := map[string]bool{}
		work := []string{a}
		for len(work) > 0 {
			x := work[len(work)-1]
			work = work[:len(work)-1]
			for _, s := range succ[x] {
				if !seen[s] {
					seen[s] = true
					work = append(work, s)
				}
			}
		}
		return seen
	}
	fwd := reach(from)
	var out []string
	for b := range fwd {
		if reach(b)[from] {
			out = append(out, b)
		}
	}
	return out
}

// rangeFuncIR builds root release and returns the IR codegen wrote
// (TestHooks.OnLL) for the function whose symbol head matches; it fails t
// when the build fails or no pack holds the function.
func rangeFuncIR(t *testing.T, root, out string, head *regexp.Regexp) string {
	t.Helper()
	tools := needTools(t)
	var mu sync.Mutex
	var fn string
	cfg := Config{Root: root, Target: pkTarget(), Profile: types.Release, Tools: tools, CacheDir: t.TempDir()}
	cfg.TestHooks.OnLL = func(name, ll string) {
		loc := head.FindStringIndex(ll)
		if loc == nil {
			return
		}
		end := strings.Index(ll[loc[0]:], "\n}\n")
		if end < 0 {
			return
		}
		mu.Lock()
		fn = ll[loc[0] : loc[0]+end+3]
		mu.Unlock()
	}
	s, err := NewSession(cfg)
	if err != nil {
		t.Fatal(err)
	}
	r, paths, err := s.Build(out + exeSuffix(cfg.Target))
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Report.Diags) > 0 || len(paths) != 1 {
		t.Fatalf("build: %d paths\n%s", len(paths), r.Report.Text(r.Sources))
	}
	if fn == "" {
		t.Fatalf("no function %s in the packs' IR", head)
	}
	return fn
}

// TestRange_CountByteVectorLoopUnchecked tests the range pass's rules 11 and
// 5 end to end (internal/lower rangexfer.go callResult, rangecheck.go
// refineStep): in a release build of the lang-sys countbyte kernel,
// CountByte's 16-lane loop, the cycle of blocks through its CountSet
// (llvm.ctpop), has no overflow check left: n += CountSet() is bounded by
// rule 8 once rule 11 bounds CountSet by 16 and rule 5 bounds i by the
// header's i + 16 <= xs.Len(), and i + 16 and the step are proved. The
// scalar tail's n += 1 keeps its check (n arrives widened from the loop).
// It builds the countbyte kernel release and reads CountByte's IR as
// codegen wrote it (TestHooks.OnLL).
func TestRange_CountByteVectorLoopUnchecked(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "..", "tests", "bench-lanes", "lang-sys", "countbyte", "mk", "countbyte.mk"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	file := filepath.Join(dir, "countbyte.mk")
	if err := os.WriteFile(file, src, 0o644); err != nil {
		t.Fatal(err)
	}
	fn := rangeFuncIR(t, file, filepath.Join(dir, "countbyte"), regexp.MustCompile(`(?m)^define [^\n]*@mk\.main\.CountByte\(`))
	body, succ := llBlocks(fn)
	var pop string
	for b, text := range body {
		if strings.Contains(text, "@llvm.ctpop") {
			if pop != "" {
				t.Fatalf("two blocks call llvm.ctpop (%s, %s):\n%s", pop, b, fn)
			}
			pop = b
		}
	}
	if pop == "" {
		t.Fatalf("no block calls llvm.ctpop:\n%s", fn)
	}
	loop := llCycle(succ, pop)
	if len(loop) < 3 {
		t.Fatalf("the CountSet block %s is on a cycle of %d blocks, want the 16-lane loop:\n%s", pop, len(loop), fn)
	}
	adds := 0
	for _, b := range loop {
		if strings.Contains(body[b], "with.overflow") {
			t.Errorf("block %s of the 16-lane loop keeps an overflow check:\n%s", b, body[b])
		}
		adds += strings.Count(body[b], "add nsw i64")
	}
	// i + 16 in the header, n += CountSet() and i += 16 in the latch.
	if adds < 3 {
		t.Errorf("the 16-lane loop has %d add nsw i64, want its three proved adds:\n%s", adds, fn)
	}
	// The tail's n += 1 stays checked: one overflow check in the function.
	if n := strings.Count(fn, "with.overflow"); n != 1 {
		t.Errorf("CountByte has %d overflow checks, want 1 (the scalar tail's n += 1):\n%s", n, fn)
	}
}

// TestRange_SortLomutoLoopUnchecked builds the idiomatic sort row (std's
// Slice[u64].Sort) release and reads its PdqPartitionLomuto instance as
// codegen wrote it: rule 12 carries the guard `a < 0 || b > s.Len() || b -
// a < 2`'s false edge past the || joins, so the loop's s[r] and s[a] are
// proved, and no block on a cycle reaches a bounds panic (code 2). The
// guard's own raw.Fail stays reachable.
func TestRange_SortLomutoLoopUnchecked(t *testing.T) {
	row := filepath.Join("..", "..", "tests", "bench", "idiomatic", "sort", "mk")
	files := map[string]string{}
	for _, name := range []string{"pkg.mk", "sort.mk"} {
		b, err := os.ReadFile(filepath.Join(row, name))
		if err != nil {
			t.Fatal(err)
		}
		files[name] = string(b)
	}
	dir := writeTree(t, files)
	fn := rangeFuncIR(t, dir, filepath.Join(t.TempDir(), "sort"), regexp.MustCompile(`(?m)^define [^\n]*@mk\.std\.core\.PdqPartitionLomuto\.[0-9a-f]+\(`))
	body, succ := llBlocks(fn)
	bounds := map[string]bool{}
	fail := false
	for b, text := range body {
		if strings.Contains(text, "@mk.std.rt.PanicAt(i32 noundef 2,") {
			bounds[b] = true
		}
		if strings.Contains(text, "@mk.std.rt.PanicMsg(i32 noundef 2,") {
			fail = true // raw.Fail(2, "sort: partition range out of bounds")
		}
	}
	if !fail {
		t.Errorf("the guard's raw.Fail is gone:\n%s", fn)
	}
	cycles := 0
	for b := range body {
		if len(llCycle(succ, b)) == 0 {
			continue
		}
		cycles++
		for _, s := range succ[b] {
			if bounds[s] {
				t.Errorf("block %s, on a cycle, branches to the bounds panic %s:\n%s", b, s, body[b])
			}
		}
	}
	if cycles < 3 {
		t.Fatalf("%d blocks on a cycle: no partition loop found\n%s", cycles, fn)
	}
}
