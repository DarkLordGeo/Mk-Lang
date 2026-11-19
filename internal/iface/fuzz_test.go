// Why: the cache reads bytes from disk that may be truncated or corrupt, and
// a bad record must never crash the compiler or leave half an item behind.
// What: FuzzDecodeInterface and FuzzDecodeFuncResult.
// How: arbitrary bytes decode to ErrCorrupt, ErrStale or a value, never a
// panic, and a failed decode leaves no item in the session.

package iface

import (
	"errors"
	"testing"

	"mk/internal/diag"
	"mk/internal/source"
	"mk/internal/types"
)

func checkDecodeErr(t *testing.T, err error) {
	if err != nil && !errors.Is(err, ErrCorrupt) && !errors.Is(err, ErrStale) && !errors.Is(err, errLoaded) {
		t.Fatalf("decode error %v is neither corrupt nor stale", err)
	}
}

func FuzzDecodeInterface(f *testing.F) {
	a, fa := ifaceSession(false)
	f.Add(EncodeInterface(a.env, a.h, geoInterface(a, fa)))
	f.Add([]byte{})
	f.Add([]byte{1, 2, 0, 0, 0, 0, 0})
	f.Fuzz(func(t *testing.T, b []byte) {
		s := loadSession(bodyText)
		items, mods := s.items.NumItems(), s.items.NumModules()
		var err error
		if ice := source.Guard("fuzz", source.Span{}, func() { _, err = DecodeInterface(s.env, b) }); ice != nil {
			t.Fatalf("decode raised %s", ice.String())
		}
		checkDecodeErr(t, err)
		if err != nil && (s.items.NumItems() != items || s.items.NumModules() != mods) {
			t.Fatal("a failed decode left items or a module behind")
		}
	})
}

func FuzzDecodeFuncResult(f *testing.F) {
	a, fa := ifaceSession(false)
	i := len("fn Main() {\n  x := ")
	d := diag.New(diag.E0501, source.NewSpan(fa.ID, uint32(i), uint32(i+4)), "conflict").
		Fact("l", diag.ListFact(diag.IntFact(1), diag.LoanFact("x", diag.LoanShared, source.Span{})))
	f.Add(EncodeFuncResult(a.env, a.h, &FuncResult{Item: types.KeyFromString("geo.Main"), Body: everyBody(a, fa), Diags: []diag.Diagnostic{*d}}))
	f.Add(EncodeEvalRecord(a.env, a.h, &EvalRecord{Value: strConst(a, "x")}))
	f.Fuzz(func(t *testing.T, b []byte) {
		s, _ := ifaceSession(true)
		var err error
		if ice := source.Guard("fuzz", source.Span{}, func() {
			_, err = DecodeFuncResult(s.env, b)
			checkDecodeErr(t, err)
			_, err = DecodeEvalRecord(s.env, b)
			checkDecodeErr(t, err)
			_, err = DecodeGenOutput(s.env, b)
			checkDecodeErr(t, err)
			_, err = DecodeParseSummary(b)
			checkDecodeErr(t, err)
			_, err = DecodeManifest(b)
			checkDecodeErr(t, err)
		}); ice != nil {
			t.Fatalf("decode raised %s", ice.String())
		}
	})
}
