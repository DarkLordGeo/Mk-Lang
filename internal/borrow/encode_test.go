// Why: borrow facts are cached between builds, so their encoding must round
// trip and reject damaged bytes.
// What: tests of encode.go: TestR3_FactsRoundTrip,
// TestR3_FactsDecodeRejectsMalformed and FuzzDecodeFacts.
// How: encodes facts of bodies built by guardBody, decodes them back, and
// feeds truncated and altered bytes to DecodeFacts.

package borrow

import (
	"bytes"
	"errors"
	"testing"

	"mk/internal/mir"
)

// guardBody is a body of n blocks whose blocks in guards end in
// TermOnerrGuard; the rest return.
func guardBody(n int, guards ...int) *mir.Body {
	b := &mir.Body{Blocks: make([]mir.Block, n)}
	for i := range b.Blocks {
		b.Blocks[i].Term.Kind = mir.TermReturn
	}
	for _, g := range guards {
		b.Blocks[g].Term.Kind = mir.TermOnerrGuard
	}
	return b
}

// TestR3_FactsRoundTrip: EncodeFacts then DecodeFacts gives the same runs,
// two encodings of equal Facts are equal bytes, and no runs (nil or empty)
// is one encoding.
func TestR3_FactsRoundTrip(t *testing.T) {
	body := guardBody(300, 1, 3, 200)
	for _, f := range []Facts{
		{},
		{OnerrRuns: []OnerrRun{{Block: 1, Run: true}}},
		{OnerrRuns: []OnerrRun{{Block: 1}, {Block: 3, Run: true}, {Block: 200, Run: true}}},
	} {
		enc := EncodeFacts(&f)
		again := EncodeFacts(&Facts{OnerrRuns: append([]OnerrRun(nil), f.OnerrRuns...)})
		if !bytes.Equal(enc, again) {
			t.Fatalf("two encodings differ: %x and %x", enc, again)
		}
		got, err := DecodeFacts(enc, body)
		if err != nil {
			t.Fatalf("decode %x: %v", enc, err)
		}
		if len(got.OnerrRuns) != len(f.OnerrRuns) {
			t.Fatalf("decode %x: %d runs, want %d", enc, len(got.OnerrRuns), len(f.OnerrRuns))
		}
		for i, r := range f.OnerrRuns {
			if got.OnerrRuns[i] != r {
				t.Errorf("run %d: %+v, want %+v", i, got.OnerrRuns[i], r)
			}
		}
		if g := got.GuardDecisions(len(body.Blocks)); len(f.OnerrRuns) > 0 && g[f.OnerrRuns[len(f.OnerrRuns)-1].Block] != f.OnerrRuns[len(f.OnerrRuns)-1].Run {
			t.Errorf("GuardDecisions of the decoded facts disagree with the runs")
		}
	}
	if !bytes.Equal(EncodeFacts(nil), EncodeFacts(&Facts{OnerrRuns: []OnerrRun{}})) {
		t.Errorf("nil and empty Facts encode differently")
	}
	if _, err := DecodeFacts(EncodeFacts(nil), nil); err != nil {
		t.Errorf("no runs with a nil body: %v", err)
	}
}

// TestR3_FactsDecodeRejectsMalformed: every malformed input is an error that
// wraps ErrFactsCorrupt, never a panic, including every truncation and
// every single-byte change of a valid encoding that is not itself valid.
func TestR3_FactsDecodeRejectsMalformed(t *testing.T) {
	body := guardBody(8, 2, 5)
	valid := EncodeFacts(&Facts{OnerrRuns: []OnerrRun{{Block: 2, Run: true}, {Block: 5}}})
	cases := map[string][]byte{
		"empty":             nil,
		"version":           {2, 0},
		"no count":          {factsVersion},
		"count past bytes":  {factsVersion, 5, 2, 1},
		"huge count":        {factsVersion, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x7f},
		"run byte 2":        {factsVersion, 1, 2, 2},
		"block out of body": {factsVersion, 1, 9, 1},
		"not a guard":       {factsVersion, 1, 3, 1},
		"not increasing":    {factsVersion, 2, 5, 1, 2, 1},
		"repeated":          {factsVersion, 2, 2, 1, 2, 1},
		"trailing":          append(append([]byte(nil), valid...), 0),
		"nil body":          valid,
	}
	for name, b := range cases {
		bd := body
		if name == "nil body" {
			bd = nil
		}
		if _, err := DecodeFacts(b, bd); err == nil || !errors.Is(err, ErrFactsCorrupt) {
			t.Errorf("%s (%x): err = %v, want ErrFactsCorrupt", name, b, err)
		}
	}
	for i := 0; i < len(valid); i++ {
		if _, err := DecodeFacts(valid[:i], body); err == nil {
			t.Errorf("truncation to %d bytes decoded", i)
		}
		for v := 0; v < 256; v++ {
			c := append([]byte(nil), valid...)
			c[i] = byte(v)
			_, _ = DecodeFacts(c, body) // must not panic
		}
	}
}

// FuzzDecodeFacts: no input panics, and whatever decodes re-encodes to the
// same bytes (the encoding is canonical).
func FuzzDecodeFacts(f *testing.F) {
	f.Add(EncodeFacts(&Facts{OnerrRuns: []OnerrRun{{Block: 2, Run: true}, {Block: 5}}}))
	f.Add([]byte{factsVersion, 0})
	body := guardBody(8, 2, 5, 7)
	f.Fuzz(func(t *testing.T, b []byte) {
		got, err := DecodeFacts(b, body)
		if err != nil {
			return
		}
		if enc := EncodeFacts(&got); !bytes.Equal(enc, b) {
			t.Fatalf("decoded %x re-encodes as %x", b, enc)
		}
	})
}
