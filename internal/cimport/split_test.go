// Why: clang's JSON AST of a large header is too big to decode at once, so
// cimport streams its top-level inner array element by element.
// What: TestSplitInner.
// How: compares the scanner's cuts with encoding/json's over strings with
// braces, quotes and backslashes, nested values and one-byte reads.

package cimport

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"testing/iotest"
)

// TestSplitInner: the element scanner cuts the TU's inner array exactly
// where encoding/json does, with braces, brackets, quotes and backslashes
// inside strings, other keys holding objects and arrays, and every chunk
// boundary (a one-byte reader).
func TestSplitInner(t *testing.T) {
	tu := `{"id":"0x1","kind":"TranslationUnitDecl","loc":{},"range":{"begin":{"a":[1,{"b":2}]},"end":{}},` +
		`"note":"inner [ { \" \\",` +
		`"inner":[` +
		`{"kind":"TypedefDecl","name":"a{b}","type":{"qualType":"char [2]"}},` +
		` {"kind":"VarDecl","name":"q\"}]\\","inner":[{"kind":"StringLiteral","value":"\"{\\\"}\""}]} ,` +
		"\n  {\"kind\":\"FunctionDecl\",\"inner\":[[],{\"x\":[{}]}]}" +
		`],"after":{"inner":[{"no":1}]}}`
	var want [][]byte
	var top struct {
		Inner []json.RawMessage `json:"inner"`
	}
	if err := json.Unmarshal([]byte(tu), &top); err != nil {
		t.Fatal(err)
	}
	for _, r := range top.Inner {
		want = append(want, r)
	}
	for _, one := range []bool{false, true} {
		var got [][]byte
		rd := iotestReader(tu, one)
		if err := splitInner(rd, func(b []byte) { got = append(got, b) }); err != nil {
			t.Fatal(err)
		}
		if len(got) != len(want) {
			t.Fatalf("one-byte %v: %d elements, want %d: %q", one, len(got), len(want), got)
		}
		for i := range got {
			if !bytes.Equal(got[i], want[i]) {
				t.Errorf("one-byte %v: element %d = %q, want %q", one, i, got[i], want[i])
			}
		}
	}
	if err := splitInner(strings.NewReader(`{"inner":[{"a":1}`), func([]byte) {}); err == nil {
		t.Error("a cut AST is no error")
	}
}

func iotestReader(s string, one bool) interface{ Read([]byte) (int, error) } {
	if one {
		return iotest.OneByteReader(strings.NewReader(s))
	}
	return strings.NewReader(s)
}
