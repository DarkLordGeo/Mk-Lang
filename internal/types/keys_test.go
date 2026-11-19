// Why: keys name items across builds and caches, so they must be injective and
// round-trip through text.
// What: TestDependentKeysAreInjective, TestKeyFromStringRoundTrips,
// TestRefuseGlobalString and TestMarkInteriorOnlyIsDistinct.
// How: builds keys of every kind and compares them, and pins the refusal fact
// value and the interior-only mark bit.

package types

import (
	"flag"
	"testing"
)

// update is accepted so that go test ./... -update works in every package;
// this package has no golden files.
var _ = flag.Bool("update", false, "no golden files in this package")

// TestDependentKeysAreInjective: impl methods, contract predicates and
// invariants have keys of their own that never equal an item, member, literal
// or each other's key.
func TestDependentKeysAreInjective(t *testing.T) {
	impl := ImplKey("geo", "std.core.Eq", "geo.Point")
	fn := MemberKey("geo", "Point", "Norm")
	pt := ItemKey("geo", "Point")
	keys := []Key{
		ImplMemberKey(impl, "Eq"),
		ContractKey(fn, "requires", 0),
		ContractKey(fn, "ensures", 0),
		ContractKey(fn, "requires", 1),
		InvariantKey(pt, 0),
		InvariantKey(pt, 1),
		LiteralKey(fn, 0),
		// No name holds '#' (names are identifiers), which is what keeps
		// the '#' tags apart from written members.
		MemberKey("geo", "Point", "Eq"),
		MemberKey("geo", "Point", "Normrequires0"),
		ItemKey("geo", "Eq"),
		impl, fn, pt,
	}
	seen := map[Key]int{}
	for i, k := range keys {
		if j, dup := seen[k]; dup {
			t.Errorf("key %d %q equals key %d", i, k, j)
		}
		seen[k] = i
	}
	want := map[Key]string{
		ImplMemberKey(impl, "Eq"):      "geo#impl(std.core.Eq for geo.Point):Eq",
		ContractKey(fn, "requires", 0): "geo.Point:Norm#requires0",
		ContractKey(fn, "ensures", 1):  "geo.Point:Norm#ensures1",
		InvariantKey(pt, 0):            "geo.Point#invariant0",
	}
	for k, s := range want {
		if k.String() != s {
			t.Errorf("got %q, want %q", k, s)
		}
	}
	if !isICE(func() { ContractKey(fn, "invariant", 0) }) {
		t.Error("ContractKey accepted kind invariant")
	}
}

// TestKeyFromStringRoundTrips: KeyFromString reads back what Key.String
// writes, which the incremental cache relies on.
func TestKeyFromStringRoundTrips(t *testing.T) {
	for _, k := range []Key{ItemKey("std.fs", "File"), MemberKey("std.fs", "File", "Close"),
		ImplMemberKey(ImplKey("m", "Share", "m.T"), "X"), TestKey("m", "a \"b\""), {}} {
		if got := KeyFromString(k.String()); got != k {
			t.Errorf("KeyFromString(%q) = %q", k, got)
		}
	}
}

// TestRefuseGlobalString: the refusal fact values compile-time evaluation
// records.
func TestRefuseGlobalString(t *testing.T) {
	if RefuseGlobal.String() != "global" || RefuseRawAccess.String() != "raw-access" {
		t.Fatal("refusal fact values")
	}
}

// TestMarkInteriorOnlyIsDistinct: MarkInteriorOnly is a bit of its own.
func TestMarkInteriorOnlyIsDistinct(t *testing.T) {
	all := []TermMarks{MarkInterior, MarkNoFreeze, MarkNoShare, MarkNoSend, MarkInteriorOnly}
	var or TermMarks
	for _, m := range all {
		if or&m != 0 || m == 0 {
			t.Fatalf("mark %d overlaps", m)
		}
		or |= m
	}
}
