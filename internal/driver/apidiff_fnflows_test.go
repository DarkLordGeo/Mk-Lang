// Why: a flow clause inside a fn type (such as `*res into res` on std.http's
// handler type) is a public fact, but TypeString does not spell it, so mk
// apidiff could miss a breaking change.
// What: TestAPIDIFF_FN_FLOWS_INVISIBLE.
// How: adds a flow clause inside fn types in three positions and checks that
// apidiff reports a "fn flows" change while the other facts stay the same.

package driver

import (
	"strings"
	"testing"
)

const f1APIFnV1 = `export view struct Resp {
    arena: &Arena
}

export struct Route {
    export handler: fn(r: mut Resp): OutOfMemory!void
}

export interface Server {
    Run(&self, handle: fn(r: mut Resp): OutOfMemory!void)
}

export Serve(handle: fn(r: mut Resp): OutOfMemory!void) {
}
`

// TestAPIDIFF_FN_FLOWS_INVISIBLE: `*r into r` added inside a fn-typed
// exported field, a fn-typed parameter and an interface method's fn-typed
// parameter is a breaking "fn flows" change on each item, while the
// signature, fields and methods facts stay as they were.
func TestAPIDIFF_FN_FLOWS_INVISIBLE(t *testing.T) {
	const clause = "(fn(r: mut Resp): OutOfMemory!void *r into r)"
	v2 := strings.ReplaceAll(f1APIFnV1, "fn(r: mut Resp): OutOfMemory!void", clause)
	if v2 == f1APIFnV1 {
		t.Fatal("no fn type replaced")
	}
	res := apiDiffFiles(t, f1APIFnV1, v2)
	text := res.Text()
	for _, want := range []string{
		"breaking  main.Route  fn flows: handler: *#1 into #1\n",
		"breaking  main.Serve  fn flows: handle: *#1 into #1\n",
		"breaking  main.Server  fn flows: Run.handle: *#1 into #1\n",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("lacks %q:\n%s", want, text)
		}
	}
	if len(res.Changes) != 3 || res.ExitStatus() != 1 {
		t.Errorf("want exactly the 3 fn flows changes, exit 1; exit %d:\n%s", res.ExitStatus(), text)
	}
	// Control: the same root has no change, and a root whose fn types have
	// no flows has an empty fn flows fact on both sides.
	if same := apiDiffFiles(t, v2, v2); len(same.Changes) != 0 {
		t.Errorf("same root:\n%s", same.Text())
	}
	if same := apiDiffFiles(t, f1APIFnV1, f1APIFnV1); len(same.Changes) != 0 {
		t.Errorf("same root without flows:\n%s", same.Text())
	}
}
