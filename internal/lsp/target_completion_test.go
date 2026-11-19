// Why: the prelude's TargetInfo and Os are aliases of std.target's types.
// What: TestLSPCompletionThroughTheAliases.
// How: completion after a value of each alias must list the fields and variants.

package lsp

import (
	"slices"
	"strings"
	"testing"
)

// TestLSPCompletionThroughTheAliases: after a TargetInfo value's dot the
// four fields are offered, and after Os's dot its five variants; through
// the aliases members answered "TargetInfo has no members", and completion
// offered the type's own name.
func TestLSPCompletionThroughTheAliases(t *testing.T) {
	_, mainURI, _ := writeFixture(t)
	c := start(t)
	c.init(true)
	text := strings.Replace(fixtureMain, "    _ = p.Width()\n", "    _ = p.Width()\n    ti: TargetInfo = Target\n    _ = ti.os\n    o := Os.Linux\n    _ = o\n", 1)
	if text == fixtureMain {
		t.Fatal("fixture has no `_ = p.Width()` line")
	}
	c.open(mainURI, text)
	if p := c.diagnostics(mainURI); len(p.Diagnostics) != 0 {
		t.Fatalf("diagnostics: %+v", p.Diagnostics)
	}
	for _, tc := range []struct {
		sub, dot string
		want     []string
	}{
		{"_ = ti.os", "_ = ti.", []string{"arch", "os", "pointer_bytes", "vector_bits"}},
		{"o := Os.Linux", "o := Os.", []string{"Freestanding", "Linux", "Macos", "Nutshell", "Windows"}},
	} {
		var got []string
		for k := range completeAt(t, c, mainURI, text, tc.sub, len(tc.dot)) {
			got = append(got, k)
		}
		slices.Sort(got)
		if !slices.Equal(got, tc.want) {
			t.Errorf("completion after %q = %q, want %q", tc.dot, got, tc.want)
		}
	}
	c.shutdown()
}
