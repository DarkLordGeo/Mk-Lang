// Why: the runtime's panic texts and the compiler's types.PanicCode names
// must stay the same words, or reports and diagnostics disagree.
// What: TestPanicTextsMatchPanicCode.
// How: reads std/rt/tests_panic.mk's assertion block from the embedded std
// and checks it names every code once with types.PanicCode(c).String().

package driver

import (
	"io/fs"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"mk/internal/types"
	"mk/std"
)

// TestPanicTextsMatchPanicCode: std/rt/tests_panic.mk's block
// "RT-F8-PanicTextIsCodeName" asserts rt.PanicText(c) == "<text>" for each
// code, and TestStdRuntimeTests runs that block on both targets, so the
// runtime returns those texts. This test closes the loop on the Go side:
// the block names every code from 1 to the last, once each, with the text
// types.PanicCode(c).String() gives. std.rt is internal to std, so no user
// program can call PanicText; the block is the runtime's own probe.
func TestPanicTextsMatchPanicCode(t *testing.T) {
	src, err := fs.ReadFile(std.FS, "rt/tests_panic.mk")
	if err != nil {
		t.Fatal(err)
	}
	text := string(src)
	start := strings.Index(text, `test "RT-F8-PanicTextIsCodeName`)
	if start < 0 {
		t.Fatal(`std/rt/tests_panic.mk has no block "RT-F8-PanicTextIsCodeName ..."`)
	}
	block := text[start:]
	if end := strings.Index(block, "\n}"); end >= 0 {
		block = block[:end]
	}
	line := regexp.MustCompile(`Assert\(PanicText\(([0-9]+)\) == "([^"]*)"\)`)
	got := map[int]string{}
	for _, m := range line.FindAllStringSubmatch(block, -1) {
		c, _ := strconv.Atoi(m[1])
		if _, dup := got[c]; dup {
			t.Errorf("code %d asserted twice", c)
		}
		got[c] = m[2]
	}
	last := 0
	for c := 1; !strings.HasPrefix(types.PanicCode(c).String(), "panic("); c++ {
		last = c
		want := types.PanicCode(c).String()
		if g, ok := got[c]; !ok {
			t.Errorf("code %d (%q) is not asserted in the block", c, want)
		} else if g != want {
			t.Errorf("code %d: the runtime's text %q, types.PanicCode %q", c, g, want)
		}
	}
	for c := range got {
		if c < 1 || c > last {
			t.Errorf("code %d is asserted but types.PanicCode has codes 1 to %d", c, last)
		}
	}
}
