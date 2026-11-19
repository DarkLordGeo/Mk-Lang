// Why: type hashes key every cache record, so the type tags must stay frozen
// and the hashes must not depend on IDs.
// What: the frozen type tags, the type hash golden, type round trips into a
// fresh session, corrupt and stale decodes, and span rebasing.
// How: compares against a golden file and builds the same types in a noisy
// session.

package iface

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"

	"mk/internal/source"
	"mk/internal/types"
)

func TestTypeTagsFrozen(t *testing.T) {
	want := []struct {
		kind types.TypeKind
		tag  uint64
	}{
		{types.KInt, 1}, {types.KIntPtr, 2}, {types.KFloat, 3}, {types.KBool, 4}, {types.KChar, 5},
		{types.KVoid, 6}, {types.KNever, 7}, {types.KRef, 8}, {types.KMut, 9}, {types.KSlice, 10},
		{types.KMutSlice, 11}, {types.KStr, 12}, {types.KArray, 13}, {types.KOptional, 14},
		{types.KErrorUnion, 15}, {types.KErrorSet, 16}, {types.KRecord, 17}, {types.KFn, 18},
		{types.KRawPtr, 19}, {types.KNamed, 20}, {types.KParam, 21}, {types.KSelf, 22},
	}
	if len(typeTag) != len(want) {
		t.Fatalf("typeTag has %d rows, want %d", len(typeTag), len(want))
	}
	for _, w := range want {
		if got := typeTag[w.kind]; got != w.tag {
			t.Errorf("%v: tag %d, want %d", w.kind, got, w.tag)
		}
		if tagKind[w.tag] != w.kind {
			t.Errorf("tag %d decodes to %v, want %v", w.tag, tagKind[w.tag], w.kind)
		}
	}
	s := newGeo(false)
	for _, k := range []types.TypeID{s.tab.Infer(3), types.Poison} {
		if !catchICE(func() { s.h.Type(k) }) {
			t.Errorf("Hasher.Type(%v) did not raise an ICE (M11)", s.tab.Kind(k))
		}
		if !catchICE(func() { encodeBundle(s, []types.TypeID{k}, nil, nil) }) {
			t.Errorf("store form of %v did not raise an ICE (M11)", s.tab.Kind(k))
		}
	}
}

func TestTypeHashGolden(t *testing.T) {
	s := newGeo(false)
	var b bytes.Buffer
	seen := map[types.TypeKind]bool{}
	for _, ty := range fixtureTypes(s) {
		seen[s.tab.Kind(ty)] = true
		fmt.Fprintf(&b, "%s %s\n", s.h.Type(ty), s.tab.TypeString(ty, nil))
	}
	for k := range typeTag {
		if !seen[k] {
			t.Errorf("fixture has no type of kind %v", k)
		}
	}
	golden(t, "typehash.golden", b.Bytes())
}

func TestRoundTripTypes(t *testing.T) {
	a := newGeo(false)
	tys := fixtureTypes(a)
	enc := encodeBundle(a, tys, nil, nil)

	b := newGeo(true)
	got, _, _, err := decodeBundle(b.env, enc)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(tys) {
		t.Fatalf("decoded %d types, want %d", len(got), len(tys))
	}
	for i := range tys {
		if as, bs := a.tab.TypeString(tys[i], nil), b.tab.TypeString(got[i], nil); as != bs {
			t.Errorf("type %d: %q decoded as %q", i, as, bs)
		}
		if a.h.Type(tys[i]) != b.h.Type(got[i]) {
			t.Errorf("type %d (%s): hash differs across sessions", i, a.tab.TypeString(tys[i], nil))
		}
	}
	if re := encodeBundle(b, got, nil, nil); !bytes.Equal(re, enc) {
		t.Errorf("re-encoding in a fresh session changed the bytes")
	}
	// Built independently in the noisy session, the same types hash and
	// encode the same.
	own := fixtureTypes(b)
	if re := encodeBundle(b, own, nil, nil); !bytes.Equal(re, enc) {
		t.Errorf("encoding depends on intern order")
	}
}

func TestDecodeUnknownForeignItemIsStale(t *testing.T) {
	a := newGeo(false)
	enc := encodeBundle(a, []types.TypeID{a.tab.Named(a.id("geo.Point"), nil)}, nil, nil)
	b := newSession()
	m := b.items.AddModule(&types.Module{Path: "geo"})
	b.item(m, types.KeyFromString("geo.Other"), "Other", types.KindStruct, true)
	if _, _, _, err := decodeBundle(b.env, enc); !errors.Is(err, ErrStale) {
		t.Fatalf("err = %v, want ErrStale", err)
	}
	// Same key, other kind: stale too.
	c := newSession()
	m = c.items.AddModule(&types.Module{Path: "geo"})
	c.item(m, types.KeyFromString("geo.Point"), "Point", types.KindEnum, true)
	if _, _, _, err := decodeBundle(c.env, enc); !errors.Is(err, ErrStale) {
		t.Fatalf("kind change: err = %v, want ErrStale", err)
	}
}

func TestDecodeCorruptTypes(t *testing.T) {
	a := newGeo(false)
	enc := encodeBundle(a, fixtureTypes(a), nil, nil)
	for cut := 0; cut < len(enc); cut++ {
		b := newGeo(true)
		if _, _, _, err := decodeBundle(b.env, enc[:cut]); err == nil {
			t.Fatalf("truncation at %d of %d decoded without error", cut, len(enc))
		} else if !errors.Is(err, ErrCorrupt) {
			t.Fatalf("truncation at %d: %v, want ErrCorrupt", cut, err)
		}
	}
	for i := 0; i < len(enc); i++ {
		bad := bytes.Clone(enc)
		bad[i] ^= 0x5a
		b := newGeo(true)
		if ice := source.Guard("decode", source.Span{}, func() { decodeBundle(b.env, bad) }); ice != nil {
			t.Fatalf("byte %d flipped: decode raised %v", i, ice.String())
		}
	}
}

const spanText1 = "fn F(a: int) {\n  return a / 0\n}\n"
const spanText2 = "fn F(a: int) {\n  // a comment\n\n  return  a / 0\n}\n"

func TestSpanRebaseCommentInside(t *testing.T) {
	a := newGeo(false)
	f := a.addFile("geo/f.mk", spanText1, "geo.F")
	at := strings.Index(spanText1, "a / 0")
	sp := source.NewSpan(f.ID, uint32(at), uint32(at+len("a / 0")))
	ins := source.NewSpan(f.ID, uint32(at+len("a / 0")), uint32(at+len("a / 0")))
	enc := encodeBundle(a, nil, nil, []source.Span{sp, ins, {}}, "geo.F")

	b := newGeo(true)
	b.sm.Add("other.mk", []byte("x"))
	f2 := b.addFile("geo/f.mk", spanText2, "geo.F")
	// The token hash is over token texts only, so a comment leaves it equal.
	if x, _ := a.anchors.Anchor("geo.F"); x.TokenHash != mustAnchor(t, b, "geo.F").TokenHash {
		t.Fatal("fixture: token hash changed under a comment")
	}
	_, _, spans, err := decodeBundle(b.env, enc)
	if err != nil {
		t.Fatal(err)
	}
	if spans[0].File != f2.ID || b.sm.Text(spans[0]) != "a / 0" {
		t.Errorf("span decoded onto %q", b.sm.Text(spans[0]))
	}
	if !spans[1].IsEmpty() || int(spans[1].Start) != strings.Index(spanText2, "a / 0")+len("a / 0") {
		t.Errorf("insertion point decoded at %d", spans[1].Start)
	}
	if spans[2] != (source.Span{}) {
		t.Errorf("zero span decoded as %v", spans[2])
	}
}

func mustAnchor(t *testing.T, s *session, k AnchorKey) Anchor {
	t.Helper()
	a, ok := s.anchors.Anchor(k)
	if !ok {
		t.Fatalf("no anchor %s", k)
	}
	return a
}

func TestSpanStaleForeignAnchor(t *testing.T) {
	a := newGeo(false)
	f := a.addFile("geo/f.mk", spanText1, "geo.F")
	at := strings.Index(spanText1, "a / 0")
	enc := encodeBundle(a, nil, nil, []source.Span{source.NewSpan(f.ID, uint32(at), uint32(at+5))})

	b := newGeo(true)
	b.addFile("geo/f.mk", strings.Replace(spanText1, "a / 0", "(a) / 0", 1), "geo.F")
	if _, _, _, err := decodeBundle(b.env, enc); !errors.Is(err, ErrStale) {
		t.Fatalf("changed tokens: err = %v, want ErrStale", err)
	}
	c := newGeo(true)
	if _, _, _, err := decodeBundle(c.env, enc); !errors.Is(err, ErrStale) {
		t.Fatalf("missing anchor: err = %v, want ErrStale", err)
	}
}

func TestSpanLocatesInnermostAnchor(t *testing.T) {
	s := newGeo(false)
	text := "fn A() { x := fn(b) { b } }\n"
	f := s.sm.Add("geo/a.mk", []byte(text))
	st, en, _ := tokens(text)
	s.anchors.Add("geo.A", Anchor{File: f.ID, Path: "geo/a.mk", Starts: st, Ends: en})
	in := strings.Index(text, "fn(b)")
	var ist, ien []uint32
	for i := range st {
		if st[i] >= uint32(in) && en[i] <= uint32(strings.LastIndex(text, "}")-1) {
			ist, ien = append(ist, st[i]), append(ien, en[i])
		}
	}
	s.anchors.Add("geo.A#fn0", Anchor{File: f.ID, Path: "geo/a.mk", Starts: ist, Ends: ien})
	s.anchors.Add(FileAnchorKey("geo/a.mk"), Anchor{File: f.ID, Path: "geo/a.mk"})
	if k, _ := s.anchors.Locate(f.ID, uint32(strings.Index(text, "{ b }")+2)); k != "geo.A#fn0" {
		t.Errorf("Locate inside the literal: %s", k)
	}
	if k, _ := s.anchors.Locate(f.ID, uint32(strings.Index(text, "x"))); k != "geo.A" {
		t.Errorf("Locate in A: %s", k)
	}
	if k, _ := s.anchors.Locate(f.ID, uint32(len(text))); k != FileAnchorKey("geo/a.mk") {
		t.Errorf("Locate outside items: %s", k)
	}
}
