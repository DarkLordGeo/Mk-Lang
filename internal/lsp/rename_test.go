// Why: rename must refuse what would change meaning.
// What: TestLSPRename.
// How: renames locals, items and methods, and checks refusals.

package lsp

import (
	"strings"
	"testing"
)

// TestLSPRename: prepareRename gives the name's range; a local renames in
// its item, an item across modules, a field with its construction
// arguments; a rename to a keyword, a used name or of a keyword is
// refused with the reason.
func TestLSPRename(t *testing.T) {
	_, mainURI, utilURI := writeFixture(t)
	c := start(t)
	c.init(true)
	c.open(mainURI, fixtureMain)
	c.diagnostics(mainURI)
	line, col := lineOf(t, fixtureMain, "Sum(xs")
	var pr struct {
		Range       Range  `json:"range"`
		Placeholder string `json:"placeholder"`
	}
	c.result(c.call("textDocument/prepareRename", at(mainURI, line, col)), &pr)
	if pr.Placeholder != "Sum" || pr.Range.Start != (Position{line, col}) {
		t.Errorf("prepareRename: %+v", pr)
	}
	line, col = lineOf(t, fixtureMain, "return self.hi")
	if m := c.call("textDocument/prepareRename", at(mainURI, line, col)); m.Error == nil || m.Error.Code != codeRequestFailed {
		t.Errorf("prepareRename on a keyword: %+v, want RequestFailed", m)
	}
	rename := func(sub string, off int, name string) (workspaceEdit, *rpcError) {
		line, col := lineOf(t, fixtureMain, sub)
		p := at(mainURI, line, col+off)
		p["newName"] = name
		m := c.call("textDocument/rename", p)
		var we workspaceEdit
		if m.Error == nil {
			c.result(m, &we)
		}
		return we, m.Error
	}
	edits := func(we workspaceEdit, uri string) []textEdit {
		for u, es := range we.Changes {
			if sameURI(u, uri) {
				return es
			}
		}
		return nil
	}
	we, err := rename("t += x", 0, "total")
	if err != nil || len(we.Changes) != 1 || len(edits(we, mainURI)) != 3 {
		t.Errorf("rename local t: %+v %v, want 3 edits in main.mk", we, err)
	}
	we, err = rename("util.Twice", 5, "Double")
	if err != nil || len(edits(we, mainURI)) != 1 || len(edits(we, utilURI)) != 1 || edits(we, utilURI)[0].NewText != "Double" {
		t.Errorf("rename Twice: %+v %v, want one edit in each module", we, err)
	}
	we, err = rename("export lo", 7, "low")
	if err != nil || len(edits(we, mainURI)) != 3 {
		t.Errorf("rename field lo: %+v %v, want the field, self.lo and the construction's lo:", we, err)
	}
	for _, tc := range []struct {
		sub       string
		off       int
		name, why string
	}{
		{"t += x", 0, "x", "already used"},
		{"Sum(xs", 0, "match", "keyword"},
		{"Sum(xs", 0, "Pair", "already uses"},
		{"Sum(xs", 0, "9lives", "not a name"},
	} {
		if _, err := rename(tc.sub, tc.off, tc.name); err == nil || !strings.Contains(err.Message, tc.why) {
			t.Errorf("rename %q to %s: %v, want a refusal saying %q", tc.sub, tc.name, err, tc.why)
		}
	}
	c.shutdown()
}
