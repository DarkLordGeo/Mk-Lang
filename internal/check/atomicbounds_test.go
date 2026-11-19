// Why: an atomic word must be a size the hardware can update in one
// instruction.
// What: TestAtomicBounds.
// How: `raw.Atomic[T]` and std.atomic's `Atomic[T]` take an integer of 1,
// 2, 4 or 8 bytes or a raw pointer; their arithmetic read-modify-writes
// take an integer; std.atomic's own generic code over either is their
// definition; an atomic word is Send and Share whatever its T.

package check

import (
	"testing"

	"mk/internal/types"
)

// rawAtomicDecls is the part of std/raw/raw.mk the cases below use.
const rawAtomicDecls = "export struct Atomic[T: Plain] {\n\tv: T\n}\n\nexport Atomic[T].New(x: T): Atomic[T]\n\nexport Atomic[T].Load(&self): T\n\n" +
	"export Atomic[T].Add(&self, x: T): T\n\nexport Atomic[T].SwapOrd(&self, x: T, const o: u8): T\n\nexport Atomic[T].AddOrd(&self, x: T, const o: u8): T\n"

// stdAtomicSrc is a std.atomic with the shape of std/atomic/atomic_t.mk.
const stdAtomicSrc = "import std.raw\n\nexport struct Atomic[T: Plain] {\n\tv: raw.Atomic[T]\n}\n\n" +
	"export Atomic[T].New(x: T): Atomic[T] {\n\treturn Atomic(v: raw.Atomic[T].New(x))\n}\n\n" +
	"export Atomic[T].Swap(&self, x: T): T {\n\treturn self.v.SwapOrd(x, 4)\n}\n\n" +
	"export Atomic[T].FetchAdd(&self, x: T): T {\n\treturn self.v.AddOrd(x, 4)\n}\n"

func TestAtomicBounds(t *testing.T) {
	w := newWorld(t, true)
	raw := w.add("std.raw", opt{std: true}, rawAtomicDecls)
	if len(raw.diags()) > 0 {
		t.Fatalf("std.raw:\n%s", dump(w, raw.diags()))
	}
	w.c.known = w.rs.Known()
	if w.c.known.Atomic == types.NoItem {
		t.Fatal("Known.Atomic not set")
	}
	at := w.add("std.atomic", opt{std: true}, stdAtomicSrc)
	if ds := append(at.diags(), at.bodyDiags(t)...); len(ds) > 0 {
		t.Fatalf("std.atomic (its generic code is the definition):\n%s", dump(w, ds))
	}
	const imp = "import std.raw\n\n"
	const ia = "import std.atomic\n\n"
	for i, tc := range []bodyCase{
		{"raw u16 and pointer fields", imp + "export struct S {\n\ta: raw.Atomic[u16]\n\tb: raw.Atomic[i16]\n\tc: raw.Atomic[*u8]\n}\n", ""},
		{"raw pointer swap", imp + "F(p: *u8): *u8 {\n\ta := raw.Atomic[*u8].New(p)\n\treturn a.SwapOrd(p, 4)\n}\n", ""},
		{"raw pointer add", imp + "F(p: *u8): *u8 {\n\ta := raw.Atomic[*u8].New(p)\n\treturn a.Add(p)\n}\n", "E0701@5:11"},
		{"raw pointer add ordered", imp + "F(p: *u8): *u8 {\n\ta := raw.Atomic[*u8].New(p)\n\treturn a.AddOrd(p, 0)\n}\n", "E0701@5:11"},
		{"raw u16 add ordered", imp + "F(): u16 {\n\ta := raw.Atomic[u16].New(1)\n\treturn a.AddOrd(2, 0)\n}\n", ""},
		{"raw f32 field", imp + "export struct S {\n\ta: raw.Atomic[f32]\n}\n", "E0701@4:5"},
		// A private field of atomic.Atomic makes an exported struct unique,
		// as one of AtomicInt or Mutex does (E0410), so these are exported.
		{"atomic every width", ia + "export struct S {\n\texport a: atomic.Atomic[u8]\n\texport b: atomic.Atomic[i16]\n\texport c: atomic.Atomic[u32]\n\texport d: atomic.Atomic[i64]\n\texport e: atomic.Atomic[uint]\n\texport f: atomic.Atomic[*u64]\n}\n", ""},
		{"atomic f64 field", ia + "export struct S {\n\texport a: atomic.Atomic[f64]\n}\n", "E0701@4:12"},
		{"atomic bool field", ia + "export struct S {\n\texport a: atomic.Atomic[bool]\n}\n", "E0701@4:12"},
		{"atomic generic field", ia + "export struct S[T] {\n\ta: atomic.Atomic[T]\n}\n", "E0701@4:5"},
		{"atomic f64 head", ia + "F(): f64 {\n\ta := atomic.Atomic[f64].New(1.5)\n\treturn a.Swap(2.5)\n}\n", "E0701@4:7"},
		{"atomic f64 inferred", ia + "F() {\n\t_ = atomic.Atomic.New(1.5)\n}\n", "E0701@4:20"},
		{"atomic pointer fetch add", ia + "F(p: *u8): *u8 {\n\ta := atomic.Atomic[*u8].New(p)\n\treturn a.FetchAdd(p)\n}\n", "E0701@5:11"},
		{"atomic pointer swap", ia + "F(p: *u8): *u8 {\n\ta := atomic.Atomic[*u8].New(p)\n\treturn a.Swap(p)\n}\n", ""},
		{"atomic i16 fetch add", ia + "F(): i16 {\n\ta := atomic.Atomic[i16].New(1)\n\treturn a.FetchAdd(2)\n}\n", ""},
		{"atomic pointer is Share and Send", ia + "NeedShare[T: Share + Send](x: &T) {\n}\n\nF(p: *u8) {\n\ta := atomic.Atomic[*u8].New(p)\n\tNeedShare(&a)\n}\n", ""},
		{"a raw pointer is not Share", ia + "NeedShare[T: Share](x: &T) {\n}\n\nF(p: *u8) {\n\tNeedShare(&p)\n}\n", "E0701@7:2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := w.add("std.s"+string(rune('a'+i)), opt{std: true}, tc.src)
			var got []string
			for _, d := range append(b.diags(), b.bodyDiags(t)...) {
				if s := d.Code.String(); s != "E0208" && s != "E1101" {
					got = append(got, s+"@"+w.lc(d.At))
				}
			}
			if g := sortStrings(got); g != tc.want {
				t.Fatalf("diagnostics %q, want %q\n%s", g, tc.want, dump(w, append(b.diags(), b.bodyDiags(t)...)))
			}
		})
	}
}
