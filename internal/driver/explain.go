// Why: mk explain E0501 answers what an error code means without a
// network or a docs checkout: its trigger, one accepting and one rejecting
// example, and the usual fixes.
//
// What: Explain, ExplainEntry and ExplainExample, with Text and JSON.
//
// How: the data is explain_table.go, generated from doc/errors.md, the docs'
// verdict blocks and tests/conformance by `go test ./internal/driver -run
// TestExplainTable -update` (explain_gen_test.go);
// TestExplainTableMatchesDesign fails when it is stale.

package driver

import (
	"encoding/json"
	"strconv"
	"strings"

	"mk/internal/diag"
)

// ExplainExample is one docs example block: where it is and its text (the
// verdict line included). The zero value means the docs have none.
type ExplainExample struct {
	At   string `json:"at"` // doc/<doc>.md:<line of the verdict>, or a tests/conformance file
	Text string `json:"text"`
}

// ExplainEntry is mk explain's answer for one code.
type ExplainEntry struct {
	Code     string         `json:"code"`
	Name     string         `json:"name"`
	Phase    string         `json:"phase"`
	Trigger  string         `json:"trigger"`
	FixClass string         `json:"fix_class"`
	Reject   ExplainExample `json:"reject"`
	Accept   ExplainExample `json:"accept"`
}

// explainRow is one generated row: the parts that come from doc/ text.
type explainRow struct {
	code    string // E0501: spelled, so this table constructs no diagnostic (TestDriverEmitsOnlyItsCodes)
	trigger string
	reject  ExplainExample
	accept  ExplainExample
}

// explainMaxLines caps each example in the answer, so a terminal answer
// stays short (the JSON form carries the whole block).
const explainMaxLines = 14

// Explain returns the entry for a code spelled E0501 (case-insensitive), or
// false for a string that is not a catalogue code.
func Explain(code string) (ExplainEntry, bool) {
	c, ok := diag.ParseCode(strings.ToUpper(strings.TrimSpace(code)))
	if !ok {
		return ExplainEntry{}, false
	}
	e := ExplainEntry{Code: c.String(), Name: c.Name(), Phase: diag.DefaultPhase(c).String()}
	if row, ok := c.Entry(); ok {
		e.FixClass = row.FixClass
	}
	for _, r := range explainTable {
		if r.code == e.Code {
			e.Trigger, e.Reject, e.Accept = r.trigger, r.reject, r.accept
		}
	}
	return e, true
}

// Text renders the entry for a terminal: the code line, the trigger, the
// fix class, then the two examples, each cut to explainMaxLines lines,
// except a flow code's, which print whole: the accepted block of a flow
// code shows each clause the rejected one lacks, and E0604's showed
// `*self into self` only past the cut.
func (e ExplainEntry) Text() string {
	maxLines := explainMaxLines
	if e.Phase == diag.PhaseFlow.String() {
		maxLines = 1 << 30
	}
	var b strings.Builder
	b.WriteString(e.Code + " " + e.Name + " (" + e.Phase + ")\n")
	if e.Trigger != "" {
		b.WriteString("trigger: " + e.Trigger + "\n")
	}
	b.WriteString("fix: " + e.FixClass + "\n")
	write := func(label string, x ExplainExample) {
		if x.At == "" {
			b.WriteString(label + ": no example in doc/\n")
			return
		}
		b.WriteString(label + " (" + x.At + "):\n")
		lines := strings.Split(strings.TrimRight(x.Text, "\n"), "\n")
		for i, l := range lines {
			if i == maxLines {
				b.WriteString("    ... " + strconv.Itoa(len(lines)-i) + " more lines\n")
				break
			}
			b.WriteString("    " + l + "\n")
		}
	}
	write("rejected", e.Reject)
	write("accepted", e.Accept)
	return b.String()
}

// JSON renders the entry as one JSON object and a newline.
func (e ExplainEntry) JSON() []byte {
	b, _ := json.Marshal(e) // plain strings: cannot fail
	return append(b, '\n')
}
