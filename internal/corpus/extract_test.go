// Why: the corpus is only as trustworthy as its reading of the documents:
// a block missed or a marker misread silently drops a test.
// What: tests of extraction, verdict lines and markers, Documents (every
// doc/**/*.md in byte order), FindRoot and the test prelude.
// How: synthetic Markdown for the grammar, and the repository's own doc/
// for the documents, the marker count and the prelude.

package corpus

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"mk/internal/diag"
)

const F = fence // three backticks

func mustExtract(t *testing.T, text string) []Block {
	t.Helper()
	bs, err := Extract("doc.md", text)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	return bs
}

func wantExtractError(t *testing.T, text, want string) {
	t.Helper()
	_, err := Extract("doc.md", text)
	if err == nil {
		t.Fatalf("Extract accepted %q; want an error containing %q", text, want)
	}
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("Extract error %q; want it to contain %q", err, want)
	}
}

// A rejecting block is read with its codes and markers.
func TestReadsARejectingBlock(t *testing.T) {
	text := "Prose.\n\n" + F + "mk\n// verdict: reject E0502\nF() {\n    n = 2 // <- E0502\n}\n" + F + "\nMore prose.\n"
	bs := mustExtract(t, text)
	if len(bs) != 1 {
		t.Fatalf("got %d blocks, want 1", len(bs))
	}
	b := bs[0]
	if b.Document != "doc.md" || b.Line != 4 || b.Verdict != VerdictReject {
		t.Errorf("block = %s line %d verdict %s; want doc.md line 4 reject", b.Document, b.Line, b.Verdict)
	}
	if !slices.Equal(b.Codes, []diag.Code{diag.E0502}) {
		t.Errorf("codes = %v", b.Codes)
	}
	if want := []Marker{{Line: 2, Code: diag.E0502}}; !slices.Equal(b.Expected, want) {
		t.Errorf("expected = %v, want %v", b.Expected, want)
	}
	if b.Source != "F() {\n    n = 2 // <- E0502\n}\n" {
		t.Errorf("source = %q", b.Source)
	}
	if b.Key() != "doc.md:4" {
		t.Errorf("key = %q", b.Key())
	}
}

// A block with no verdict line is an error.
func TestMissingVerdictIsAnError(t *testing.T) {
	wantExtractError(t, F+"mk\nF() { }\n"+F+"\n", "doc.md:2: bad verdict line")
}

func TestFenceOnLastLineIsAnError(t *testing.T) {
	wantExtractError(t, "x\n"+F+"mk\n", "doc.md:2: empty block at end of file")
	wantExtractError(t, "x\n"+F+"mk", "doc.md:2: empty block at end of file")
}

func TestMissingClosingFenceIsAnError(t *testing.T) {
	wantExtractError(t, F+"mk\n// verdict: ok\nF() { }\n", "doc.md:1: block has no closing fence")
}

func TestIndentedFenceIsNotABlock(t *testing.T) {
	for _, text := range []string{
		"  " + F + "mk\n// verdict: ok\n" + F + "\n",
		"~~~mk\n// verdict: ok\n~~~\n",
		F + "mk extra\n// verdict: ok\n" + F + "\n",
		F + "MK\n// verdict: ok\n" + F + "\n",
		F + "go\n// verdict: ok\n" + F + "\n",
	} {
		if bs := mustExtract(t, text); len(bs) != 0 {
			t.Errorf("%q gave %d blocks, want 0", text, len(bs))
		}
	}
	// Trailing whitespace after the fence is allowed.
	if bs := mustExtract(t, F+"mk \t\n// verdict: ok\nF() { }\n"+F+"  \n"); len(bs) != 1 {
		t.Errorf("fence with trailing blanks gave %d blocks, want 1", len(bs))
	}
	// An indented closing fence does not close the block.
	bs := mustExtract(t, F+"mk\n// verdict: ok\n  "+F+"\nG() { }\n"+F+"\n")
	if len(bs) != 1 || bs[0].Source != "  "+F+"\nG() { }\n" {
		t.Errorf("indented closing fence: %+v", bs)
	}
}

func TestTrailingCRStripped(t *testing.T) {
	lf := "a\n" + F + "mk\n// verdict: reject E0502\nF() {\n    n = 2 // <- E0502\n}\n" + F + "\n"
	crlf := strings.ReplaceAll(lf, "\n", "\r\n")
	a, b := mustExtract(t, lf), mustExtract(t, crlf)
	if len(a) != 1 || len(b) != 1 {
		t.Fatalf("blocks %d and %d, want 1 and 1", len(a), len(b))
	}
	if a[0].Source != b[0].Source || a[0].Line != b[0].Line || !slices.Equal(a[0].Expected, b[0].Expected) {
		t.Errorf("CRLF block differs: %+v vs %+v", a[0], b[0])
	}
	if strings.Contains(b[0].Source, "\r") {
		t.Errorf("source keeps a CR: %q", b[0].Source)
	}
}

func TestTwoCodesOnOneMarker(t *testing.T) {
	bs := mustExtract(t, F+"mk\n// verdict: reject E0502 E0501\nF() {\n    x // <- E0502  E0501\n}\n"+F+"\n")
	want := []Marker{{Line: 2, Code: diag.E0501}, {Line: 2, Code: diag.E0502}}
	if !slices.Equal(bs[0].Expected, want) {
		t.Errorf("expected = %v, want %v", bs[0].Expected, want)
	}
	if !slices.Equal(bs[0].Codes, []diag.Code{diag.E0501, diag.E0502}) {
		t.Errorf("codes = %v, want sorted E0501 E0502", bs[0].Codes)
	}
	// The last "// <- " on a line counts.
	bs = mustExtract(t, F+"mk\n// verdict: reject E0501\nx // <- not this // <- E0501\n"+F+"\n")
	if want := []Marker{{Line: 1, Code: diag.E0501}}; !slices.Equal(bs[0].Expected, want) {
		t.Errorf("last marker: %v", bs[0].Expected)
	}
}

func TestUnknownCodeIsAnError(t *testing.T) {
	wantExtractError(t, F+"mk\n// verdict: reject E9999\nx // <- E9999\n"+F+"\n", `unknown code "E9999" in verdict`)
	wantExtractError(t, F+"mk\n// verdict: reject E0501\nx // <- E0501 E05\n"+F+"\n", `doc.md:3: unknown code "E05" in marker`)
	wantExtractError(t, F+"mk\n// verdict: reject E0501\nx // <- \n"+F+"\n", "marker with no code")
	wantExtractError(t, F+"mk\n// verdict: reject E0000\nx // <- E0000\n"+F+"\n", "unknown code")
}

func TestMarkerNotInVerdictIsAnError(t *testing.T) {
	wantExtractError(t, F+"mk\n// verdict: reject E0501\nx // <- E0502\n"+F+"\n", "doc.md:3: marker E0502 is not in the verdict's codes")
	wantExtractError(t, F+"mk\n// verdict: ok\nx // <- E0502\n"+F+"\n", "doc.md:3: marker E0502 in a block whose verdict is ok")
	wantExtractError(t, F+"mk\n// verdict: parse\nx // <- E0502\n"+F+"\n", "verdict is parse")
	// The other side: a verdict code with no marker.
	wantExtractError(t, F+"mk\n// verdict: reject E0501 E0502\nx // <- E0502\n"+F+"\n", "doc.md:2: verdict code E0501 has no marker")
}

// Prose is never a marker.
func TestMarkerOutsideBlockIsNotAMarker(t *testing.T) {
	text := "| `// verdict: reject E0501` | x // <- E0501 |\n\n" + F + "mk\n// verdict: ok\nF() { }\n" + F + "\nafter // <- E0502\n"
	bs := mustExtract(t, text)
	if len(bs) != 1 || len(bs[0].Expected) != 0 {
		t.Fatalf("got %+v; want one ok block with no marker", bs)
	}
	root := repoRoot(t)
	docs, err := Documents(root)
	if err != nil {
		t.Fatal(err)
	}
	lines := 0
	for _, d := range docs {
		text, err := readDoc(root, d)
		if err != nil {
			t.Fatal(err)
		}
		for _, l := range splitLines(text) {
			if strings.Contains(l, "// <- E") {
				lines++
			}
		}
	}
	bs, err = DesignBlocks(root)
	if err != nil {
		t.Fatal(err)
	}
	inBlocks := 0
	for _, b := range bs {
		seen := map[int]bool{}
		for _, m := range b.Expected {
			seen[m.Line] = true
		}
		inBlocks += len(seen)
	}
	t.Logf("doc/: %d lines hold \"// <- E\", %d marker lines inside blocks", lines, inBlocks)
	if inBlocks >= lines {
		t.Errorf("marker lines in blocks %d, lines holding a marker %d: the prose row of reference.md is not prose any more?", inBlocks, lines)
	}
}

// TestVerdictGrammar: the verdict line grammar. A reject verdict may name
// codes of different phases.
func TestVerdictGrammar(t *testing.T) {
	good := []struct {
		line  string
		v     Verdict
		codes []diag.Code
	}{
		{"// verdict: ok", VerdictOK, nil},
		{"// verdict: ok  \t", VerdictOK, nil},
		{"// verdict: parse", VerdictParse, nil},
		{"// verdict: reject E0501", VerdictReject, []diag.Code{diag.E0501}},
		{"// verdict: reject E0204 E0301", VerdictReject, []diag.Code{diag.E0204, diag.E0301}},
		{"// verdict: reject   E0502\tE0501 ", VerdictReject, []diag.Code{diag.E0501, diag.E0502}},
	}
	for _, g := range good {
		v, codes, err := parseVerdict(g.line)
		if err != nil || v != g.v || !slices.Equal(codes, g.codes) {
			t.Errorf("parseVerdict(%q) = %v %v %v; want %v %v", g.line, v, codes, err, g.v, g.codes)
		}
	}
	for _, bad := range []string{
		"", "// verdict:ok", "//verdict: ok", "// verdict: OK", "// verdict: okay", "// verdict: reject",
		"// verdict: reject ", "// verdict: rejected E0501", "// verdict: reject E0501 E0501",
		"// verdict: reject e0501", "// verdict: reject E0501,E0502", "  // verdict: ok", "// Verdict: ok",
		"// verdict: pending",
	} {
		if _, _, err := parseVerdict(bad); err == nil {
			t.Errorf("parseVerdict(%q) accepted", bad)
		}
	}
}

func repoRoot(t testing.TB) string {
	t.Helper()
	root, err := FindRoot("")
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func TestFindRoot(t *testing.T) {
	root := repoRoot(t)
	if _, err := os.Stat(filepath.Join(root, "doc", "reference.md")); err != nil {
		t.Fatal(err)
	}
	if got, err := FindRoot(root); err != nil || got != root {
		t.Errorf("FindRoot(root) = %q, %v; want %q", got, err, root)
	}
	// An explicit start is the root itself, never walked up.
	if got, err := FindRoot(filepath.Join(root, "internal", "corpus")); err == nil {
		t.Errorf("FindRoot(internal/corpus) = %q; want an error, not the enclosing root", got)
	}
	if _, err := FindRoot(t.TempDir()); err == nil {
		t.Errorf("FindRoot(empty temp dir) found a root")
	}
}

// Every doc/**/*.md, the module pages of doc/std/ included, in byte order
// of path, nothing else.
func TestDocumentsAreAllDocFiles(t *testing.T) {
	root := repoRoot(t)
	docs, err := Documents(root)
	if err != nil {
		t.Fatal(err)
	}
	var want []string
	for _, dir := range []string{"doc", "doc/std"} {
		ents, err := os.ReadDir(filepath.Join(root, filepath.FromSlash(dir)))
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range ents {
			if e.Type().IsRegular() && strings.HasSuffix(e.Name(), ".md") {
				want = append(want, dir+"/"+e.Name())
			}
		}
	}
	slices.Sort(want)
	if !slices.Equal(docs, want) {
		t.Errorf("Documents = %v\nwant %v", docs, want)
	}
	if !slices.IsSorted(docs) {
		t.Errorf("Documents not in byte order")
	}
	for _, d := range []string{"doc/reference.md", "doc/memory.md", "doc/cookbook.md", "doc/std/fs.md"} {
		if !slices.Contains(docs, d) {
			t.Errorf("Documents lacks %s", d)
		}
	}
}

// Every document extracts cleanly, with more than 100 blocks; counts are
// logged, never pinned.
func TestDocCorpusWellFormed(t *testing.T) {
	bs, err := DesignBlocks(repoRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(bs) <= 100 {
		t.Errorf("%d blocks, want more than 100", len(bs))
	}
	count := map[Verdict]int{}
	perDoc := map[string]int{}
	codes := map[diag.Code]bool{}
	markerLines := 0
	for _, b := range bs {
		count[b.Verdict]++
		perDoc[b.Document]++
		for _, c := range b.Codes {
			codes[c] = true
			if !c.IsKnown() {
				t.Errorf("%s: code %s not in the catalogue", b.Key(), c)
			}
		}
		seen := map[int]bool{}
		for _, m := range b.Expected {
			seen[m.Line] = true
		}
		markerLines += len(seen)
		if (b.Verdict == VerdictReject) != (len(b.Codes) > 0) || (b.Verdict == VerdictReject) != (len(b.Expected) > 0) {
			t.Errorf("%s: verdict %s with codes %v and markers %v", b.Key(), b.Verdict, b.Codes, b.Expected)
		}
	}
	var docs []string
	for d, n := range perDoc {
		docs = append(docs, d+" "+strconv.Itoa(n))
	}
	slices.Sort(docs)
	t.Logf("%d blocks: %d ok, %d parse, %d reject; %d marker lines; %d distinct reject codes; per document: %s",
		len(bs), count[VerdictOK], count[VerdictParse], count[VerdictReject], markerLines, len(codes), strings.Join(docs, ", "))
}

// The prelude is the reference's test prelude block.
func TestPreludeFromReference(t *testing.T) {
	p, err := Prelude(repoRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"struct Point {", "linear struct Res {", "Res.Close(self)", "Acquire(name: str): Res",
		"MakePoint(): Point", "Cond(): bool", "Use[T](x: &T) {}", "UseMut[T](x: mut T) {}", "Take[T](x: T)"} {
		if !strings.Contains(p, want) {
			t.Errorf("prelude lacks %q", want)
		}
	}
	// Exchange left the test prelude; blocks swap with the prelude's Swap.
	if strings.Contains(p, "Exchange[") {
		t.Errorf("prelude still declares Exchange")
	}
	if strings.Contains(p, "verdict") || !strings.HasSuffix(p, "\n") {
		t.Errorf("prelude holds the verdict line or is not LF-terminated: %q", p)
	}
	if _, err := preludeFrom("# no heading\n"); err == nil {
		t.Errorf("preludeFrom without the heading succeeded")
	}
	if _, err := preludeFrom(PreludeHeading + "\n\ntext only\n"); err == nil {
		t.Errorf("preludeFrom without a block succeeded")
	}
	// A block before the heading is not the prelude.
	got, err := preludeFrom(F + "mk\n// verdict: ok\nA()\n" + F + "\n" + PreludeHeading + "\n" + F + "mk\n// verdict: parse\nB()\n" + F + "\n")
	if err != nil || got != "B()\n" {
		t.Errorf("preludeFrom = %q, %v; want B()", got, err)
	}
}

// FuzzExtract: no panic and no hang on any document; every block it returns
// keeps its invariants.
func FuzzExtract(f *testing.F) {
	f.Add(F + "mk\n// verdict: reject E0502\nF() {\n    n = 2 // <- E0502\n}\n" + F + "\n")
	f.Add(F + "mk\n// verdict: ok\r\nx\r\n" + F + "\r\n")
	f.Add(F + "mk\n")
	f.Add("// <- E0501\n" + F + "mk\n// verdict: parse\n" + F + "\n")
	f.Fuzz(func(t *testing.T, text string) {
		bs, err := Extract("fuzz.md", text)
		if err != nil {
			return
		}
		n := len(splitLines(text))
		for _, b := range bs {
			if b.Line < 2 || b.Line > n {
				t.Fatalf("block line %d outside 2..%d", b.Line, n)
			}
			if (b.Verdict == VerdictReject) != (len(b.Codes) > 0) {
				t.Fatalf("verdict %s with codes %v", b.Verdict, b.Codes)
			}
			src := strings.Count(b.Source, "\n")
			for _, m := range b.Expected {
				if m.Line < 1 || m.Line > src || !slices.Contains(b.Codes, m.Code) {
					t.Fatalf("marker %+v outside the block or verdict", m)
				}
			}
		}
	})
}
