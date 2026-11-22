// Why: a receiver's `into` is part of a method's public contract, as a
// parameter's is, so the tools that show and compare contracts must carry it.
// What: TestQueryReceiverInto and TestAPIDiffReceiverInto.
// How: mk query spells the clause on the receiver, and mk apidiff reports
// adding it as a breaking flows change.

package driver

import (
	"strings"
	"testing"
)

const recvIntoV1 = "export struct Names {\n    all: List[str]\n}\n\nexport Names.Keys(&self, arena: &Arena) {\n}\n"

func TestQueryReceiverInto(t *testing.T) {
	src := strings.Replace(recvIntoV1, "(&self, arena", "(&self into arena, arena", 1)
	dir := testPkg(t, map[string]string{"q.mk": src})
	answerHas(t, queryOne(t, dir, "sig", "Names.Keys"), "Keys(&self into arena, arena: &Arena)")
}

func TestAPIDiffReceiverInto(t *testing.T) {
	v2 := strings.Replace(recvIntoV1, "(&self, arena", "(&self into arena, arena", 1)
	res := apiDiffFiles(t, recvIntoV1, v2)
	if text := res.Text(); !strings.Contains(text, "breaking  main.Names.Keys") || !strings.Contains(text, "self into arena") {
		t.Errorf("adding the receiver's into:\n%s", text)
	}
}
