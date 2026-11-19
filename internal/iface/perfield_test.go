// Why: a per-field result source clause is part of a function's public
// signature, so it must survive the cache and change the hashes.
// What: TestPerFieldSignatureRoundTrip and TestPerFieldFnTypeHash.
// How: the signature and fn type encodings carry Flows.Fields and
// FnType.Fields after the sources; the tests round-trip them and compare
// hashes.

package iface

import (
	"reflect"
	"testing"

	"mk/internal/types"
)

func TestPerFieldSignatureRoundTrip(t *testing.T) {
	a := withDecls(newGeo(false))
	sig := divSig(a)
	flat := a.h.Signature(&sig)
	sig.Flows.Fields = []types.FieldSources{{Field: 0, Sources: []types.Source{{Param: 2, Behind: true, Path: []types.FieldStep{{Field: 0, Type: types.I32}}}}}}
	if a.h.Signature(&sig) == flat {
		t.Error("a per-field clause left the signature hash equal")
	}
	write := func(w *walker) { w.writeSig(&sig, false) }
	var got types.Signature
	read := func(r *reader, d *Decoder) { got = r.readSig(d) }
	if _, err := storeTrip(a, a.env, write, read); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, sig) {
		t.Errorf("per-field signature:\n got %+v\nwant %+v", got.Flows, sig.Flows)
	}
	b := withDecls(newGeo(true))
	if _, err := storeTrip(a, b.env, write, read); err != nil {
		t.Fatal(err)
	}
	if a.h.Signature(&sig) != b.h.Signature(&got) {
		t.Error("per-field signature hash differs across sessions")
	}
}

func TestPerFieldFnTypeHash(t *testing.T) {
	s := newGeo(false)
	tab := s.tab
	ri := tab.Ref(types.I32)
	mk := func(fields ...types.FieldSources) types.TypeID {
		return tab.Fn(types.FnType{Params: []types.FnParam{{Type: ri}, {Type: ri}}, Result: ri,
			Sources: []types.Source{{Param: 1}, {Param: 2}}, Fields: fields})
	}
	whole := mk()
	split := mk(types.FieldSources{Field: 0, Sources: []types.Source{{Param: 1}}})
	other := mk(types.FieldSources{Field: 0, Sources: []types.Source{{Param: 2}}})
	if s.h.Type(whole) == s.h.Type(split) || s.h.Type(split) == s.h.Type(other) {
		t.Error("the fn type hash ignores the per-field clause")
	}
	b := newGeo(true)
	enc := encodeBundle(s, []types.TypeID{split}, nil, nil)
	tys, _, _, err := decodeBundle(b.env, enc)
	if err != nil || len(tys) != 1 {
		t.Fatalf("decode: %v %v", tys, err)
	}
	if got := b.tab.Get(tys[0]).Fn; len(got.Fields) != 1 || got.Fields[0].Field != 0 || got.Fields[0].Sources[0].Param != 1 {
		t.Errorf("fn type Fields after the trip: %+v", got.Fields)
	}
	if s.h.Type(split) != b.h.Type(tys[0]) {
		t.Error("the per-field fn type's hash differs across sessions")
	}
}
