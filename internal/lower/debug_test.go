// Why: a debugging aid for the check adapter, which reads check's facts.
// What: TestDebugFacts.
// How: with MKDBG naming a .mk file, dumps check's facts per node; skipped
// otherwise.

package lower

import (
	"fmt"
	"os"
	"testing"

	"mk/internal/ast"
)

func TestDebugFacts(t *testing.T) {
	src, err := os.ReadFile(os.Getenv("MKDBG"))
	if err != nil {
		t.Skip("no MKDBG")
	}
	w := newWorld(t, true)
	b := w.add("m", opt{}, string(src))
	t.Log(dump(w, b.diags()))
	cfg := w.lowerCfg()
	for _, id := range b.m.Units() {
		body := w.c.Check(b.m, id)
		t.Log(dump(w, body.Diags))
		in := FromCheck(cfg, w.c, body).(*checkInput)
		ast.Inspect(body.Node, func(n ast.Node) bool {
			if n == nil {
				return false
			}
			fmt.Printf("%d %T %s use=%+v type=%s adj=%+v lit=%+v\n", n.ID(), n, w.lc(n.Span()), in.use(n.ID()), w.tab.TypeString(in.Type(n.ID()), nil), in.Adjust(n.ID()), in.Literal(n.ID()))
			return true
		})
	}
}
