// Why: the design fmt step must find every mk block of doc/ and name it as
// the corpus does, or a badly formatted example slips through.
// What: tests of designBlocks and judgeDesignFmt.
// How: reads the repository's doc/ (a module page under doc/std/ included)
// and feeds judgeDesignFmt a recorded mk fmt -l output.

package main

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestDesignFmtBlocks(t *testing.T) {
	blocks, err := designBlocks(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if len(blocks) < 100 {
		t.Fatalf("%d blocks under doc/, want at least 100", len(blocks))
	}
	names, files := map[string]bool{}, map[string]bool{}
	for _, b := range blocks {
		if names[b.name] || files[b.file] {
			t.Errorf("block %s (%s) twice", b.name, b.file)
		}
		names[b.name], files[b.file] = true, true
		if !strings.HasPrefix(b.text, "// verdict: ") {
			t.Errorf("%s: first line %q is not a verdict", b.name, strings.SplitN(b.text, "\n", 2)[0])
		}
	}
	// Two blocks of doc/std/core.md are found, and named by their verdict
	// line.
	for _, want := range []struct{ marker, verdict string }{
		{"_ = m.Remove(k)", "// verdict: reject E0701"},
		{`try Print(w, "{name:{>5}\n", (name))`, "// verdict: reject E0706"},
	} {
		i := slices.IndexFunc(blocks, func(b designBlock) bool { return strings.Contains(b.text, want.marker) })
		if i < 0 {
			t.Errorf("no design block holds %q", want.marker)
			continue
		}
		b := blocks[i]
		if !strings.HasPrefix(b.name, "std/core.md:") || !strings.HasPrefix(b.text, want.verdict+"\n") {
			t.Errorf("block holding %q: %s, text starts %q", want.marker, b.name, b.text[:min(40, len(b.text))])
		}
		if b.file != "std_core_"+strings.TrimPrefix(b.name, "std/core.md:")+".mk" {
			t.Errorf("%s written to %s", b.name, b.file)
		}
	}
	// A module page's blocks are read too, named by their path under doc/.
	i := slices.IndexFunc(blocks, func(b designBlock) bool { return strings.HasPrefix(b.name, "std/") })
	if i < 0 {
		t.Errorf("no block of doc/std/ found")
	} else if b := blocks[i]; !strings.HasPrefix(b.file, "std_") || strings.Contains(b.file, "/") {
		t.Errorf("%s written to %s", b.name, b.file)
	}
}

func TestDesignFmtJudge(t *testing.T) {
	blocks := []designBlock{
		{name: "reference.md:1359", file: "reference_1359.mk"},
		{name: "reference.md:2519", file: "reference_2519.mk"},
		{name: "memory.md:12", file: "memory_12.mk"},
		{name: "style.md:40", file: "style_40.mk"},
	}
	out := []string{
		`C:\Users\dev\AppData\Local\mk-work\designfmt\reference_2519.mk`,
		`C:\Users\dev\AppData\Local\mk-work\designfmt\memory_12.mk: 2 syntax errors, the first at 1:1: a fn literal is not an item`,
		`C:\Users\dev\AppData\Local\mk-work\designfmt\reference_1359.mk`,
		`exit status 1`,
		`C:\Users\dev\AppData\Local\mk-work\designfmt\other.mk`,
	}
	changed, syntax := judgeDesignFmt(blocks, out)
	if want := []string{"reference.md:1359", "reference.md:2519"}; !slices.Equal(changed, want) {
		t.Errorf("changed %q, want %q", changed, want)
	}
	if syntax != 1 {
		t.Errorf("%d syntax errors, want 1", syntax)
	}
	if changed, syntax := judgeDesignFmt(blocks, nil); len(changed) != 0 || syntax != 0 {
		t.Errorf("no output: changed %q, syntax %d", changed, syntax)
	}
}
