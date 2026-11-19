// Why: std's own bodies must obey the rules for where a returned reference
// comes from, as user code must.
// What: TestHeapAllocAndFmtArenaBodies, with diagsIn.
// How: mk check over std must be clean in Heap.Alloc and FmtArena; with each old
// body planted back it must give E0602 or E0603.
//
// Heap.Alloc once returned `&self.own` (E0602: a `&Heap` supplies only `view`
// results, so an `&Alloc` result has no parameter source), and FmtArena
// returned `t.Str()` of a local Text (E0603: Text is a view, so Str borrows t,
// which ends at the return).

package std

import (
	"os"
	"strings"
	"testing"
)

// resultBodies are the std files the finding named, with the old body text
// that the checker must reject and the diagnostic it gives; drop is text the
// fix added outside the body (an import the old body did not use).
var resultBodies = []struct {
	file, fixed, old, drop, code string
}{
	{
		file: "core/mem_alloc.mk",
		drop: "import std.raw\n",
		fixed: "    p := raw.Of(&self.own)\n" +
			"    unsafe \"every Heap is never released: the process heap is static memory, an evaluation's lives in a page freed only when the evaluation ends; so own outlives every caller\" {\n" +
			"        return &*p\n" +
			"    }\n",
		old:  "    return &self.own\n",
		code: " E0602 ",
	},
	{
		file:  "core/text_fmt.mk",
		fixed: "    return TextInto(t) // the arena's bytes, from a; t.Str() would borrow t (E0603)\n",
		old:   "    return t.Str()\n",
		code:  " E0603 ",
	},
}

// diagsIn returns the diagnostic lines of out located in std file f.
func diagsIn(out, f string) []string {
	var got []string
	for _, line := range strings.Split(out, "\n") {
		if diagLine.MatchString(line) && strings.HasPrefix(line, "std/"+f+":") {
			got = append(got, line)
		}
	}
	return got
}

// TestHeapAllocAndFmtArenaBodies: mk check of an empty program over std
// gives no diagnostic in either file; with each old body planted back, the
// same check gives that file's E0602 or E0603, so the clean run is a
// judgement and not a skipped body. Other std files are not asserted here:
// other tests own their diagnostics.
func TestHeapAllocAndFmtArenaBodies(t *testing.T) {
	out, _ := mkCheckWith(t, nil)
	for _, b := range resultBodies {
		if d := diagsIn(out, b.file); len(d) > 0 {
			t.Errorf("%s: want no diagnostic, got:\n%s", b.file, strings.Join(d, "\n"))
		}
	}
	planted := map[string]string{}
	for _, b := range resultBodies {
		src, err := os.ReadFile(b.file)
		if err != nil {
			t.Fatal(err)
		}
		text := strings.ReplaceAll(string(src), "\r\n", "\n")
		if strings.Count(text, b.fixed) != 1 {
			t.Fatalf("%s: the fixed body text is not found once; update resultBodies with the file", b.file)
		}
		text = strings.Replace(text, b.fixed, b.old, 1)
		if b.drop != "" {
			text = strings.Replace(text, b.drop, "", 1)
		}
		planted[b.file] = text
	}
	out, _ = mkCheckWith(t, planted)
	for _, b := range resultBodies {
		d := diagsIn(out, b.file)
		if len(d) != 1 || !strings.Contains(d[0], b.code) {
			t.Errorf("%s with the old body: want one%sdiagnostic, got:\n%s", b.file, b.code, strings.Join(d, "\n"))
		}
	}
}
