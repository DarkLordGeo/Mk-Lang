// Why: one store can reach an abstract node through several holders, and the
// merged E0604 must still name each clause once.
// What: TestMergeStoreFixNamesEachClauseOnce, with the e0604 builder.
// How: merges diagnostics at one span and checks the kept clause list. The
// rule itself, an abstract node holding what the body stored into it, is
// tested by tests/conformance/rules/R11_reject_stored_selfreach.mk and
// R11_accept_stored_selfreach_declared.mk (mk corpus --all --strict) and by
// the witnesses tests/soundness/witness/FH-selfreach-r3*.mk.

package borrow

import (
	"testing"

	"mk/internal/diag"
	"mk/internal/source"
)

// e0604 is an E0604 at at whose clause is clause, written at the slot w.
func e0604(at, w source.Span, clause string) *diag.Diagnostic {
	d := diag.New(diag.E0604, at, "this stores a loan; write `"+clause+"`")
	d.Text("clause", clause)
	d.Fix(diag.Fix{Class: diag.Compiles, Edits: []diag.Edit{{Span: w, Text: clause}}, Why: "write `" + clause + "`"})
	return d
}

// One store reaching prog through several holders (lox's Vm.Run) drops one
// E0604 per holder at the same span: the kept one lists `*prog into prog,
// *self into prog`, never `*self into prog` again.
func TestMergeStoreFixNamesEachClauseOnce(t *testing.T) {
	at := source.Span{File: 1, Start: 10, End: 20}
	w := source.Span{File: 1, Start: 2, End: 2}
	k := e0604(at, w, "*prog into prog")
	for range 4 {
		mergeStoreFix(k, e0604(at, w, "*self into prog"))
	}
	if got, want := storeFactText(k, "clause"), "*prog into prog, *self into prog"; got != want {
		t.Errorf("clause %q, want %q", got, want)
	}
	if got, want := k.Message, "this stores a loan; write `*prog into prog, *self into prog`"; got != want {
		t.Errorf("message %q, want %q", got, want)
	}
	if len(k.Fixes) != 1 || len(k.Fixes[0].Edits) != 1 || k.Fixes[0].Edits[0].Text != "*prog into prog, *self into prog" {
		t.Errorf("fix %+v", k.Fixes)
	}
}
