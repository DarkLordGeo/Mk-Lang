// Why: interned symbols are shared by every stage and goroutine, and the
// prelude's symbols sit at fixed IDs.
// What: TestPreludeSymbolsFixed, TestInternRoundTrip, TestInternConcurrent and
// TestChunkListAcrossChunks.
// How: round trips, parallel interning and growth across chunks.

package source

import (
	"fmt"
	"sync"
	"testing"
)

// TestPreludeSymbolsFixed pins the predeclared numbering: each constant spells
// its name in a fresh Interner, and the groups hold exactly the reference's
// names.
func TestPreludeSymbolsFixed(t *testing.T) {
	t.Parallel()
	in := NewInterner()
	if in.Len() != NumPredeclared {
		t.Fatalf("fresh Len = %d, want %d", in.Len(), NumPredeclared)
	}
	for s := Symbol(1); int(s) < NumPredeclared; s++ {
		name, ok := PredeclaredName(s)
		if !ok || name == "" || in.Str(s) != name {
			t.Fatalf("symbol %d: %q %v, interner says %q", s, name, ok, in.Str(s))
		}
		if got := in.Intern(name); got != s {
			t.Fatalf("Intern(%q) = %d, want %d", name, got, s)
		}
	}
	pin := map[Symbol]string{
		SymUnderscore: "_", SymSelf: "self", SymSelfType: "Self", SymFields: "fields",
		SymFieldsBound: "Fields", SymEvalArena: "EvalArena", SymNever: "never", SymItemsMut: "ItemsMut",
	}
	for s, want := range pin {
		if got := in.Str(s); got != want {
			t.Errorf("Str(%d) = %q, want %q", s, got, want)
		}
	}
	group := func(pred func(Symbol) bool) string {
		out := ""
		for s := Symbol(0); int(s) < NumPredeclared; s++ {
			if pred(s) {
				out += in.Str(s) + " "
			}
		}
		return out
	}
	if got := group(IsContextual); got != "from into ends fuel packed self Self result " {
		t.Errorf("contextual: %q", got)
	}
	if got := group(IsUniverse); got != "true false some none ok err fields Assert Panic Cast Trunc SizeOf AlignOf Target Plain Send Share Fields EvalArena " {
		t.Errorf("universe: %q", got)
	}
	if got := group(IsPrimitiveType); got != "i8 i16 i32 i64 u8 u16 u32 u64 int uint f32 f64 bool char str void never " {
		t.Errorf("primitive: %q", got)
	}
	if _, ok := PredeclaredName(Symbol(NumPredeclared)); ok {
		t.Error("PredeclaredName past the end")
	}
}

func TestInternRoundTrip(t *testing.T) {
	t.Parallel()
	in := NewInterner()
	a := in.Intern("Point")
	if a != Symbol(NumPredeclared) || in.Str(a) != "Point" {
		t.Fatalf("first new symbol %d %q", a, in.Str(a))
	}
	if in.Intern("Point") != a || in.InternBytes([]byte("Point")) != a {
		t.Fatal("re-intern changed the symbol")
	}
	if in.Intern("point") == a {
		t.Fatal("interning must be case-sensitive")
	}
	if s, ok := in.Lookup("Point"); !ok || s != a {
		t.Fatal("Lookup hit")
	}
	if _, ok := in.Lookup("Absent"); ok {
		t.Fatal("Lookup miss")
	}
	if in.Intern("") != NoSymbol || in.InternBytes(nil) != NoSymbol || in.Str(NoSymbol) != "" {
		t.Fatal("empty string is NoSymbol")
	}
	b := in.InternBytes([]byte("Fresh"))
	if in.Str(b) != "Fresh" || in.Intern("Fresh") != b {
		t.Fatal("InternBytes round trip")
	}
	if ie := Guard("t", Span{}, func() { in.Str(Symbol(1 << 30)) }); ie == nil || ie.Runtime {
		t.Fatal("unknown symbol must ICE")
	}
}

func TestInternConcurrent(t *testing.T) {
	t.Parallel()
	in := NewInterner()
	const names = 5000
	const workers = 8
	got := make([][]Symbol, workers)
	var wg sync.WaitGroup
	for w := range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got[w] = make([]Symbol, names)
			for i := range names {
				j := (i*7 + w*131) % names // each worker interns in its own order
				got[w][j] = in.Intern(fmt.Sprintf("name_%d", j))
				_ = in.Str(got[w][j])
			}
		}()
	}
	wg.Wait()
	seen := make(map[Symbol]bool, names)
	for j := range names {
		s := got[0][j]
		for w := 1; w < workers; w++ {
			if got[w][j] != s {
				t.Fatalf("name_%d: workers disagree %d vs %d", j, s, got[w][j])
			}
		}
		if seen[s] || in.Str(s) != fmt.Sprintf("name_%d", j) {
			t.Fatalf("name_%d: symbol %d reused or wrong %q", j, s, in.Str(s))
		}
		seen[s] = true
	}
	if in.Len() != NumPredeclared+names {
		t.Fatalf("Len = %d", in.Len())
	}
}

func TestChunkListAcrossChunks(t *testing.T) {
	t.Parallel()
	var c AppendList[int]
	for i := range 5000 {
		if got := c.Append(i * 3); got != uint32(i) {
			t.Fatalf("append returned %d, want %d", got, i)
		}
	}
	for i := range 5000 {
		if v, ok := c.Get(uint32(i)); !ok || v != i*3 {
			t.Fatalf("get(%d) = %d, %v", i, v, ok)
		}
	}
	if _, ok := c.Get(5000); ok {
		t.Fatal("get past the end")
	}
	for _, i := range []uint32{0, 1023, 1024, 3071, 3072, 1<<32 - 1} {
		k, off := locate(i)
		if k < 0 || k >= chunkCount || off >= 1<<(k+chunkBaseBits) {
			t.Fatalf("locate(%d) = %d, %d", i, k, off)
		}
	}
}
