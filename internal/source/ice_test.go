// Why: an internal compiler error must become a diagnostic, not a crash.
// What: TestGuardNoPanic, TestGuardICE, TestGuardRuntimePanic and
// TestGuardNested.
// How: runs ICE panics, runtime panics and nested guards under Guard.

package source

import (
	"strings"
	"testing"
)

func TestGuardNoPanic(t *testing.T) {
	t.Parallel()
	ran := false
	if ie := Guard("item", Span{}, func() { ran = true }); ie != nil || !ran {
		t.Fatalf("Guard = %+v, ran %v", ie, ran)
	}
}

func TestGuardICE(t *testing.T) {
	t.Parallel()
	at := NewSpan(3, 1, 2)
	ie := Guard("m.F", NewSpan(1, 0, 0), func() { ICEf(at, "bad %s", "thing") })
	if ie == nil || ie.Runtime || ie.What != "bad thing" || ie.Span != at || ie.Where != "m.F" {
		t.Fatalf("got %+v", ie)
	}
	if !strings.Contains(ie.Stack, "goroutine") {
		t.Fatal("Stack missing")
	}
	if ie.String() != "internal compiler error in m.F: bad thing" {
		t.Fatalf("String = %q", ie.String())
	}
	// An ICE without a span keeps the guard's span.
	guardSpan := NewSpan(1, 4, 5)
	if ie := Guard("w", guardSpan, func() { Assertf(false, Span{}, "x") }); ie == nil || ie.Span != guardSpan {
		t.Fatalf("Assertf: %+v", ie)
	}
	if ie := Guard("w", guardSpan, func() { Assertf(true, Span{}, "x") }); ie != nil {
		t.Fatalf("Assertf(true): %+v", ie)
	}
}

func TestGuardRuntimePanic(t *testing.T) {
	t.Parallel()
	ie := Guard("w", Span{}, func() {
		var xs []int
		_ = xs[3]
	})
	if ie == nil || !ie.Runtime || !strings.Contains(ie.What, "index out of range") {
		t.Fatalf("got %+v", ie)
	}
	ie = Guard("w", Span{}, func() { panic(42) })
	if ie == nil || !ie.Runtime || ie.What != "42" {
		t.Fatalf("got %+v", ie)
	}
}

func TestGuardNested(t *testing.T) {
	t.Parallel()
	var inner *InternalError
	outer := Guard("outer", Span{}, func() {
		inner = Guard("inner", Span{}, func() { ICEf(Span{}, "deep") })
	})
	if outer != nil || inner == nil || inner.Where != "inner" {
		t.Fatalf("outer %+v inner %+v", outer, inner)
	}
}
