// Why: a per-field flows clause is part of a function's public contract, so
// the tools that show and compare contracts must carry it.
// What: TestQueryPerFieldClause and TestAPIDiffPerFieldClause.
// How: mk query spells a per-field clause where it spells `from`, and mk
// apidiff reports a change of the clause as a breaking flows change, on a
// function and on a fn type.

package driver

import (
	"strings"
	"testing"
)

const c73Source = `export struct Pair {
    export r: &i32
    export s: &i32
}

export Mk(a: &i32, b: &i32): Pair from (r: a, s: b) {
    return Pair(r: a, s: b)
}

export Apply(g: fn(a: &i32, b: &i32): Pair from (r: a, s: b)) {
}

export Main(sys: &System) {
}
`

func TestQueryPerFieldClause(t *testing.T) {
	dir := testPkg(t, map[string]string{"q.mk": c73Source})
	answerHas(t, queryOne(t, dir, "sig", "Mk"), "export Mk(a: &i32, b: &i32): Pair from (r: a, s: b)")
	answerHas(t, queryOne(t, dir, "flows", "Mk"), "per field: from (r: a, s: b)")
}

func TestAPIDiffPerFieldClause(t *testing.T) {
	v2 := strings.ReplaceAll(c73Source, "from (r: a, s: b)", "from (r: b, s: a)")
	res := apiDiffFiles(t, c73Source, v2)
	text := res.Text()
	for _, want := range []string{
		"breaking  main.Mk  flows: ",
		"breaking  main.Apply  fn flows: g: from #1, from #2, field 0 from #1, field 1 from #2  ->  g: from #2, from #1, field 0 from #2, field 1 from #1\n",
		"per field: from (r: a, s: b)  ->  ",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("lacks %q:\n%s", want, text)
		}
	}
	if res.ExitStatus() != 1 {
		t.Errorf("exit %d, want 1 (breaking):\n%s", res.ExitStatus(), text)
	}
	if same := apiDiffFiles(t, c73Source, c73Source); len(same.Changes) != 0 {
		t.Errorf("same root:\n%s", same.Text())
	}
}
