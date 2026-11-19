// Why: mono expands a forwarded Print per instance, and an unpadded one must
// come out straight-line, as internal/lower/format.go lowers a literal one.
// What: TestFmtStraight_ForwardedPrint over the stand-in std fmtStraightStd.
// How: reads the instance's MIR for Writer.Str and value calls in text order on
// the call's error edge, no piece table and no runner; padded keeps FmtPadRun.

package mono

import (
	"strings"
	"testing"

	"mk/internal/mir"
	"mk/internal/types"
)

const fmtStraightStd = `export error WriteError

export view struct Writer {
    n: int
}

export Writer.Str(mut self, s: str): WriteError!void

export struct FmtPiece {
    export lit: str
    export arg: i32
    export spec: u32
}

export struct FmtArg {
    export p: *u8
    export f: fn(*u8, u32, mut Writer): WriteError!void
}

export FmtValue[T](p: *u8, spec: u32, w: mut Writer): WriteError!void
export FmtSpecValue[T](p: *u8, spec: u32, w: mut Writer): WriteError!void
export FmtRun(w: mut Writer, pieces: []FmtPiece, args: []FmtArg): WriteError!void
export FmtPadRun(w: mut Writer, pieces: []FmtPiece, args: []FmtArg): WriteError!void
export Print[A](w: mut Writer, const f: str, args: A): WriteError!void
export Of[T](r: &T): *T
export PtrCast[T, U](p: *T): *U

export interface Format {
    Format(&self): int
}

Line[A](w: mut Writer, const f: str, args: A): WriteError!void {
    unsafe "std.raw's own functions" {
        try Print(w, f, args)
    }
}

Main(w: mut Writer): WriteError!void {
    unsafe "std.raw's own functions" {
        try Line(w, "a {x:x} {{b}} {y}{x}", (x: 7, y: 2))
        try Line(w, "{y:>4}", (y: 3))
    }
}
`

// TestFmtStraight_ForwardedPrint: the unpadded instance of Line is
// straight-line: "a ", x through FmtSpecValue (the entry a valued spec
// reads) with spec 1, " {b} ", y through FmtValue with spec 0, x again with
// spec 0, each call on the error edge the Print call had; it names no
// runner and builds no piece. The padded instance keeps FmtPadRun.
func TestFmtStraight_ForwardedPrint(t *testing.T) {
	sw := newSrcWorldAt(t, "std.raw", true, fmtStraightStd)
	k := &types.KnownItems{Print: sw.item(t, "Print"), FmtRun: sw.item(t, "FmtRun"), FmtPiece: sw.item(t, "FmtPiece"),
		FmtArg: sw.item(t, "FmtArg"), FmtValue: sw.item(t, "FmtValue"), Format: sw.item(t, "Format")}
	sw.impls[implKey{k.Format, 0, types.Int}] = k.FmtValue
	in, ds := Collect(sw, sw.limitRoots(), Options{Known: k, Validate: true})
	if len(ds) != 0 {
		t.Fatalf("diagnostics: %v", ds[0].Message)
	}
	str, spec, pad := sw.item(t, "Writer:Str"), sw.item(t, "FmtSpecValue"), sw.item(t, "FmtPadRun")
	lines := instancesOf(in, sw.item(t, "Line"))
	if len(lines) != 2 {
		t.Fatalf("want two Line instances, got %d", len(lines))
	}
	straight, padded := 0, 0
	for _, l := range lines {
		b := l.Body
		var first mir.BlockID = mir.NoBlock
		runs, pads, pieces := 0, 0, 0
		for bi, blk := range b.Blocks {
			for _, st := range blk.Stmts {
				if st.Kind == mir.StAssign && st.Rvalue.Kind == mir.RvAggregate && (st.Rvalue.Item == k.FmtPiece || st.Rvalue.Item == k.FmtArg) {
					pieces++
				}
			}
			tm := blk.Term
			if tm.Kind != mir.TermCall || tm.Callee.Kind != mir.CallDirect {
				continue
			}
			switch tm.Callee.Fn.Item {
			case k.Print:
				t.Fatalf("a Print call survived")
			case k.FmtRun:
				runs++
			case pad:
				pads++
			case str, k.FmtValue, spec:
				if first == mir.NoBlock {
					first = mir.BlockID(bi)
				}
			}
		}
		if pads == 1 {
			padded++
			if first != mir.NoBlock || runs != 0 {
				t.Errorf("the padded instance has straight-line calls or FmtRun")
			}
			continue
		}
		straight++
		if runs != 0 || pieces != 0 {
			t.Fatalf("the unpadded instance has %d FmtRun calls and %d FmtPiece or FmtArg values", runs, pieces)
		}
		// Walk the chain from the first call; every call takes the same
		// error edge, and the last one goes on to the code after Print.
		var got []string
		var edge mir.ErrorEdge
		ptrs := map[string]mir.LocalID{}
		for bi := first; bi != mir.NoBlock; {
			tm := b.Blocks[bi].Term
			if tm.Kind != mir.TermCall || tm.Callee.Kind != mir.CallDirect {
				break
			}
			args := b.Args[tm.Args.Start:tm.Args.End()]
			var s string
			switch tm.Callee.Fn.Item {
			case str:
				lit, ok := strBytes(sw.consts, args[1].Operand)
				if !ok || args[0].Mode != mir.ArgReborrow {
					t.Fatalf("Writer.Str call with %v", args)
				}
				s = "str " + string(lit)
			case k.FmtValue, spec:
				name := "value"
				if tm.Callee.Fn.Item == spec {
					name = "spec"
				}
				if args[2].Mode != mir.ArgReborrow || args[1].Operand.Kind != mir.OpImm {
					t.Fatalf("value call with %v", args)
				}
				s = name + " " + itoa(int32(args[1].Operand.Bits))
				ptrs[s] = args[0].Operand.Place.Local
			default:
				bi = mir.NoBlock
				continue
			}
			if !tm.HasErr {
				t.Fatalf("%s: no error edge", s)
			}
			if len(got) == 0 {
				edge = tm.Err
			} else if tm.Err != edge {
				t.Fatalf("%s: error edge %v, want %v", s, tm.Err, edge)
			}
			got = append(got, s)
			bi = tm.Target
		}
		if g, want := strings.Join(got, ";"), "str a ;spec 1;str  {b} ;value 0;spec 0"; g != want {
			t.Fatalf("calls %q, want %q", g, want)
		}
		// x's two holes read one entry: the same address.
		if ptrs["spec 1"] != ptrs["spec 0"] || ptrs["spec 1"] == ptrs["value 0"] {
			t.Fatalf("entry addresses %v: x's holes differ or y shares x's", ptrs)
		}
	}
	if straight != 1 || padded != 1 {
		t.Fatalf("%d straight-line and %d padded instances, want one of each", straight, padded)
	}
}
