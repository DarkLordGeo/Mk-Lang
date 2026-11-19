// Why: the IR builder and printer sit on the compiler's hot path.
// What: BenchmarkLLVMPrint (bytes per second of WriteTo), BenchmarkLLVMBuild
// (instructions per second of the builder) and TestEmitAllocations.
// How: runs over the every module and over generated 20,000-instruction packs;
// the allocation test counts allocations per pack, block and function.

package llvm

import (
	"bytes"
	"io"
	"runtime"
	"strconv"
	"testing"
)

// genPack builds funcs functions of blocks blocks each; a block holds
// perBlock instructions (load, add nsw, store, with a call to a declared
// function with attributes every 12th) and one terminator. Every third
// instruction moves the block's debug location, as codegen does per
// statement. It returns the module and its instruction count, terminators
// included.
func genPack(tg target, funcs, blocks, perBlock int) (*Module, int) {
	m := NewModule("bench.mk", tg.triple, tg.layout)
	mt := m.Metadata()
	i64, p := m.I(64), m.Ptr()
	nu := Attrs{}.Flag("noundef")
	file := mt.DIFile("bench.mk", ".")
	cu := mt.DICompileUnit(file, false)
	mt.DebugFlags(tg.os == "windows")
	callee := m.Declare("mk.bench.Callee", FuncSig{Ret: i64, RetAttrs: nu, Params: []Type{i64, p},
		ParamAttrs: []Attrs{nu, nu.Flag("nonnull")}, FnAttrs: Attrs{}.Flag("nounwind")})
	one := m.ConstInt(i64, 1)
	n, line := 0, 1
	for fi := 0; fi < funcs; fi++ {
		name := "mk.bench.F" + strconv.Itoa(fi)
		f := m.AddFunction(name, FuncSig{Ret: i64, Params: []Type{p, i64}, ParamAttrs: []Attrs{nu.Flag("nonnull"), nu},
			FnAttrs: Attrs{}.Flag("nounwind")})
		sp := mt.DISubprogram(name, name, file, line, cu, false)
		f.SetDbg(sp)
		bs := make([]*Block, blocks)
		for i := range bs {
			bs[i] = f.NewBlock("b" + strconv.Itoa(i))
		}
		ptr, acc := f.Param(0), f.Param(1)
		for bi, b := range bs {
			for k := 0; k < perBlock; k++ {
				if k%3 == 0 {
					line++
					b.SetDebugLoc(mt.DILocation(line, 1+line%80, sp))
				}
				switch {
				case k%12 == 11:
					acc = b.CallFunc(callee, []Value{acc, ptr})
				case k%3 == 0:
					acc = b.Load(i64, ptr, 8)
				case k%3 == 1:
					acc = b.Bin(Add, acc, one, NSW)
				default:
					b.Store(acc, ptr, 8)
				}
				n++
			}
			if bi+1 < len(bs) {
				b.Br(bs[bi+1])
			} else {
				b.Ret(&acc)
			}
			n++
		}
	}
	return m, n
}

var benchPacks = []struct {
	name                    string
	funcs, blocks, perBlock int
}{
	{"func20k", 1, 1, 20000},   // one function of 20,000 instructions
	{"pack20k", 500, 4, 10},    // 500 functions of 4 blocks of 10
	{"pack200k", 2000, 10, 10}, // a large pack
}

func BenchmarkLLVMPrint(b *testing.B) {
	run := func(b *testing.B, m *Module) {
		var buf bytes.Buffer
		if _, err := m.WriteTo(&buf); err != nil {
			b.Fatal(err)
		}
		b.SetBytes(int64(buf.Len()))
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			buf.Reset()
			if _, err := m.WriteTo(&buf); err != nil {
				b.Fatal(err)
			}
		}
	}
	b.Run("every", func(b *testing.B) { run(b, buildEvery(goldenTarget)) })
	for _, c := range benchPacks {
		b.Run(c.name, func(b *testing.B) {
			m, _ := genPack(goldenTarget, c.funcs, c.blocks, c.perBlock)
			run(b, m)
		})
	}
}

func BenchmarkLLVMBuild(b *testing.B) {
	for _, c := range benchPacks {
		b.Run(c.name, func(b *testing.B) {
			_, n := genPack(goldenTarget, c.funcs, c.blocks, c.perBlock)
			b.ReportAllocs()
			var ms0, ms1 runtime.MemStats
			runtime.ReadMemStats(&ms0)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				genPack(goldenTarget, c.funcs, c.blocks, c.perBlock)
			}
			b.StopTimer()
			runtime.ReadMemStats(&ms1)
			total := float64(n) * float64(b.N)
			b.ReportMetric(float64(b.Elapsed().Nanoseconds())/total, "ns/inst")
			b.ReportMetric(float64(ms1.Mallocs-ms0.Mallocs)/total, "allocs/inst")
		})
	}
}

// printed returns the module's text and the bytes WriteTo allocated to
// produce it, measured around one call into io.Discard.
func printed(t *testing.T, m *Module) (int64, uint64) {
	t.Helper()
	var ms0, ms1 runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&ms0)
	n, err := m.WriteTo(io.Discard)
	runtime.ReadMemStats(&ms1)
	if err != nil {
		t.Fatal(err)
	}
	return n, ms1.TotalAlloc - ms0.TotalAlloc
}

// TestEmitAllocations: building and printing a pack allocates per
// pack, block and function, not per instruction, and printing
// allocates no more than its output. Not parallel: AllocsPerRun and the
// memory statistics count every goroutine's allocations.
func TestEmitAllocations(t *testing.T) {
	for _, c := range []struct {
		name                    string
		funcs, blocks, perBlock int
		maxPerInst              float64
	}{
		{"func20k", 1, 1, 20000, 0.05}, // about 0.008 per instruction
		{"pack20k", 500, 4, 10, 1.0},   // about 0.62: per function and block
	} {
		_, n := genPack(goldenTarget, c.funcs, c.blocks, c.perBlock)
		allocs := testing.AllocsPerRun(3, func() { genPack(goldenTarget, c.funcs, c.blocks, c.perBlock) })
		if per := allocs / float64(n); per > c.maxPerInst {
			t.Errorf("%s: %.3f allocations per instruction, want at most %.3f", c.name, per, c.maxPerInst)
		}
		m, _ := genPack(goldenTarget, c.funcs, c.blocks, c.perBlock)
		out, bytes := printed(t, m)
		if bytes > uint64(out) {
			t.Errorf("%s: first WriteTo allocated %d bytes for %d bytes of output", c.name, bytes, out)
		}
		if a := testing.AllocsPerRun(3, func() { m.WriteTo(io.Discard) }); a != 0 {
			t.Errorf("%s: WriteTo allocates %.0f times after the first", c.name, a)
		}
	}
}
