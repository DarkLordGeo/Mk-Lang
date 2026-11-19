// Why: the cookbook's "Load a library at run time" block is judged by the
// corpus harness, whose std.dl and std.raw stubs (corpushelp_test.go) must
// carry what it uses.
// What: TestDlRecipeLowers.
// How: the block builds, checks and lowers as a verdict-ok block would, so
// its verdict in doc/cookbook.md can say ok.

package lower

import (
	"strings"
	"testing"
)

// TestDlRecipeLowers: the recipe, whatever its verdict line says today,
// is built by the harness, accepted by check and lowered with no ICE.
func TestDlRecipeLowers(t *testing.T) {
	const marker = "lib.Symbol("
	keys, texts := docBlocks(t)
	core, pre := corpusPrelude(t), testPrelude(t)
	found := 0
	for _, k := range keys {
		src := texts[k]
		if !strings.Contains(src, marker) || !strings.Contains(src, "import std.dl\n") {
			continue
		}
		found++
		_, rest, _ := strings.Cut(src, "\n")
		u := corpusUnit{key: k, src: "// verdict: ok\n" + rest}
		w, b, ok := corpusWorld(t, core, pre, u)
		if !ok {
			t.Fatalf("%s: not built by the harness: %s", k, whyUnbuilt(w, b))
		}
		bodies, ices, rejected := lowerUnitsJudged(w, b, false)
		for _, s := range append(ices, rejected...) {
			t.Errorf("%s: %s", k, s)
		}
		if len(bodies) == 0 {
			t.Errorf("%s: no body lowered", k)
		}
	}
	if found != 1 {
		t.Fatalf("found %d blocks of the recipe, want 1", found)
	}
}
