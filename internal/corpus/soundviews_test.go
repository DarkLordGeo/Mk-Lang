// Why: value arms, iterator payloads, index receivers and the slice view
// methods make new loans, and the soundness test must cover them.
// What: TestSoundAccepted, _Rejected, _NoDefects, _Smoke, _DefectSeeds and
// _T7Breakdown, over the m7 profile's forms (GenVersion 8).
// How: each form in the generator's spelling is checked and run in the
// tagged mode; misuses must be rejected and their Rejected runs must show
// the violation the rule prevents.

package corpus

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"

	"mk/internal/driver"
)

// viewsAccepted are the m7 forms in the generator's spelling, after the header:
// value arms in every value position, for over an iterator with a
// destructured payload, index receivers, Swap, Replace, SplitAt, GetPair,
// ChunksMut under a scope, and Pool's GetPair and Iter.
var viewsAccepted = []struct {
	name  string
	entry string
}{
	{"arm-binding-ref", `Run(var k: i64): i64 {
    var h: i64 = 0
    var v1: i64 = 5
    var v2: i64 = 6
    k = k *% 6364136223846793005 +% 1442695040888963407
    var r3 := if ((k >> 33) & 1) == 1 { &v1 } else { &v2 }
    h = h *% 31 +% *r3
    k = k *% 6364136223846793005 +% 1442695040888963407
    v4 := match (k >> 33) & 3 {
        0 { v1 }
        1 {
            h = h *% 31 +% 3
            *r3 +% 1
        }
        _ { 7 }
    }
    h = h *% 31 +% v4
    h = h *% 31 +% v2
    h = h *% 31 +% v1
    h = h *% 31 +% k
    return h
}`},
	{"arm-inline", `Run(var k: i64): i64 {
    var h: i64 = 0
    var v1: i64 = 5
    var v2: i64 = 6
    var r3 := match (k >> 3) & 3 {
        0 { &v1 }
        1 { Id(&v2) }
        _ { Find(k ^ 1, &v1) else { &v2 } }
    }
    var v4: i64 = Ident[i64](match (k >> 5) & 3 {
        0 { 1 }
        1 { v1 }
        _ { *r3 }
    })
    var r5 := Pick(k ^ 2, if ((k >> 9) & 1) == 1 { r3 } else if ((k >> 10) & 1) == 1 { &v1 } else { Get(k ^ 3, &v2) else { r3 } }, &v2)
    h = h *% 31 +% *r5
    h = h *% 31 +% v4
    h = h *% 31 +% *r3
    h = h *% 31 +% v2
    h = h *% 31 +% v1
    h = h *% 31 +% k
    return h
}`},
	{"return-arm", `H1(k: i64, r1: &i64, r2: &i64): &i64 from r1, r2 {
    return if ((k >> 3) & 1) == 1 { r1 } else { match (k >> 5) & 3 {
        0 { r2 }
        1 { Find(k ^ 1, r1) else { r2 } }
        _ { Id(r1) }
    } }
}

Run(var k: i64): i64 {
    var h: i64 = 0
    var v1: i64 = 5
    var v2: i64 = 6
    var r3 := H1(k, &v1, &v2)
    h = h *% 31 +% *r3
    h = h *% 31 +% v2
    h = h *% 31 +% v1
    h = h *% 31 +% k
    return h
}`},
	{"wrapped-views", `Run(var k: i64, io: Io): i64 {
    var h: i64 = 0
    var xs1: [4]i64 = [1, 2, 3, 4]
    (left: lf2, right: rt2) := Halves(mut xs1[..], 1)
    lf2[0] = 5
    h = h *% 31 +% rt2[2]
    if pr3 := Pair(mut xs1[..], 0, 3) {
        *pr3.a = *pr3.b +% k
    }
    for c4 in Pieces(xs1[..], 3) {
        for x5 in c4 {
            h = h *% 31 +% x5
        }
    }
    scope s6 := io.Scope() {
        for c7 in PiecesMut(mut xs1[..], 2) {
            s6.go Fill(c7, k)
        }
    }
    h = h *% 31 +% xs1[0] +% xs1[1] +% xs1[2] +% xs1[3]
    h = h *% 31 +% k
    return h
}`},
	{"arm-positions", `Run(var k: i64): i64 {
    var h: i64 = 0
    var v1: i64 = 5
    var v2: i64 = 6
    var r3 := &v1
    k = k *% 6364136223846793005 +% 1442695040888963407
    r3 = if ((k >> 33) & 1) == 1 { &v2 } else { Id(r3) }
    h = h *% 31 +% *r3
    k = k *% 6364136223846793005 +% 1442695040888963407
    h = h *% 31 +% *Id(if ((k >> 33) & 1) == 1 { &v1 } else { r3 })
    k = k *% 6364136223846793005 +% 1442695040888963407
    var l4 := Lx(src: if ((k >> 33) & 1) == 1 { r3 } else { &v2 }, pos: if ((k >> 34) & 1) == 1 { 1 } else { 2 })
    k = k *% 6364136223846793005 +% 1442695040888963407
    var xs5: [4]i64 = [if ((k >> 33) & 1) == 1 { v1 } else { *r3 }, 2, match (k >> 40) & 1 {
        0 { 3 }
        _ { v2 }
    }, 4]
    k = k *% 6364136223846793005 +% 1442695040888963407
    var v6: i64 = if ((k >> 33) & 1) == 1 {
        if ((k >> 35) & 1) == 1 { 1 } else { v1 }
    } else {
        match (k >> 36) & 1 {
            0 { 2 }
            _ { *r3 }
        }
    }
    h = h *% 31 +% v6
    h = h *% 31 +% xs5[0] +% xs5[1] +% xs5[2] +% xs5[3]
    h = h *% 31 +% *l4.src +% l4.pos
    h = h *% 31 +% v2
    h = h *% 31 +% v1
    h = h *% 31 +% k
    return h
}`},
	{"arm-diverging", `Run(var k: i64): i64 {
    var h: i64 = 0
    var v1: i64 = 5
    var v2: i64 = 6
    var n7 := 0
    for n7 < 4 {
        n7 += 1
        k = k *% 6364136223846793005 +% 1442695040888963407
        var v8: i64 = if ((k >> 33) & 1) == 1 { continue } else { v1 }
        k = k *% 6364136223846793005 +% 1442695040888963407
        var r9 := if ((k >> 33) & 1) == 1 { &v8 } else { break }
        h = h *% 31 +% *r9
        h = h *% 31 +% v8
    }
    k = k *% 6364136223846793005 +% 1442695040888963407
    var v10: i64 = if ((k >> 33) & 1) == 1 { v2 } else { return h }
    h = h *% 31 +% v10
    h = h *% 31 +% v2
    h = h *% 31 +% v1
    h = h *% 31 +% k
    return h
}`},
	{"else-values", `Run(var k: i64): i64 {
    var h: i64 = 0
    var v1: i64 = 5
    var v2: i64 = 6
    var r11 := Find(k ^ 1, &v1) else { &v2 }
    var r12 := Get(k ^ 2, r11) else e {
        _ = e
        &v2
    }
    var r13 := Get(k ^ 3, &v1) else { return h }
    h = h *% 31 +% *r13
    h = h *% 31 +% *r12
    h = h *% 31 +% *r11
    h = h *% 31 +% v2
    h = h *% 31 +% v1
    h = h *% 31 +% k
    return h
}`},
	{"for-iter", `Run(var k: i64): i64 {
    var h: i64 = 0
    var xs1: [4]i64 = [1, 2, 3, 4]
    for (i, x) in It(xs: xs1[..], i: 0) {
        h = h *% 31 +% i +% *x
    }
    for (i: j2, x: y2) in It(xs: xs1[1..3], i: 0) {
        h = h *% 31 +% j2 *% *y2
    }
    for p3 in It(xs: xs1[..], i: 1) {
        h = h *% 31 +% *p3.x
    }
    for c4 in xs1.Chunks(3) {
        for x5 in c4 {
            h = h *% 31 +% x5
        }
    }
    var ps6: [2](a: i64, b: i64) = [(a: 1, b: 2), (a: 3, b: 4)]
    for (a, b) in ps6 {
        h = h *% 31 +% a *% b
    }
    for i7, (a: a7, b: b7) in ps6 {
        h = h *% 31 +% i64(i7) +% a7 +% b7
    }
    h = h *% 31 +% xs1[0] +% xs1[1] +% xs1[2] +% xs1[3]
    h = h *% 31 +% k
    return h
}`},
	{"index-receivers", `Run(var k: i64): i64 {
    var h: i64 = 0
    var v1: i64 = 5
    var cs2: [2]Ct = [Ct(n: 1), Ct(n: 2)]
    cs2[0].Add(v1)
    for ix3 in 0..2 {
        cs2[ix3].Add(k)
    }
    var z4 := mut cs2[..]
    z4[1].Add(3)
    h = h *% 31 +% z4[0].n
    h = h *% 31 +% cs2[0].n +% cs2[1].n
    h = h *% 31 +% v1
    h = h *% 31 +% k
    return h
}`},
	{"swap-replace", `Run(var k: i64): i64 {
    var h: i64 = 0
    var v1: i64 = 5
    var v2: i64 = 6
    Swap(mut v1, mut v2)
    var v5 := Replace(mut v1, 9)
    var r3 := &v1
    var r4 := &v2
    Swap(mut r3, mut r4)
    var r6 := Replace(mut r3, &v1)
    h = h *% 31 +% *r6
    h = h *% 31 +% v5
    h = h *% 31 +% *r4
    h = h *% 31 +% *r3
    h = h *% 31 +% v2
    h = h *% 31 +% v1
    h = h *% 31 +% k
    return h
}`},
	{"split-pair", `Run(var k: i64): i64 {
    var h: i64 = 0
    var xs1: [4]i64 = [1, 2, 3, 4]
    (left: lf2, right: rt2) := xs1.SplitAt(2)
    lf2[0] = 5
    rt2[1] = 6
    h = h *% 31 +% lf2[1] +% rt2[0]
    if pr3 := xs1.GetPair(0, 3) {
        *pr3.a = *pr3.a +% 1
        *pr3.b = *pr3.b *% 2
        Swap(mut *pr3.a, mut *pr3.b)
    }
    pq4 := xs1.GetPair(1, 2) else { return h }
    *pq4.a = *pq4.b +% k
    h = h *% 31 +% xs1[0] +% xs1[1] +% xs1[2] +% xs1[3]
    h = h *% 31 +% k
    return h
}`},
	{"chunks-scope", `Run(var k: i64, io: Io): i64 {
    var h: i64 = 0
    var xs1: [4]i64 = [1, 2, 3, 4]
    scope s2 := io.Scope() {
        for c3 in xs1.ChunksMut(1) {
            s2.go Fill(c3, k)
        }
    }
    scope s4 := io.Scope() {
        for c5 in xs1.ChunksMut(3) {
            s4.go Fill(c5, 7)
        }
    }
    h = h *% 31 +% xs1[0] +% xs1[1] +% xs1[2] +% xs1[3]
    h = h *% 31 +% k
    return h
}`},
	{"pool", `import std.mem

Run(var k: i64, a: &Arena): i64 {
    var h: i64 = 0
    var p1 := mem.Pool[i64].New(a)
    g2 := p1.Add(5) else { return h }
    g3 := p1.Add(6) else { return h }
    if pr4 := p1.GetPair(g2, g3) {
        *pr4.a = *pr4.a +% k
        Swap(mut *pr4.a, mut *pr4.b)
    }
    for (handle, value) in p1.Iter() {
        _ = handle
        h = h *% 31 +% *value
    }
    h = h *% 31 +% k
    return h
}`},
}

// TestSoundAccepted: each form checks clean and runs clean in the
// tagged mode on seeds 0 to 15, with FailAlloc 0 and 3.
func TestSoundAccepted(t *testing.T) {
	s := soundSess(t)
	for _, c := range viewsAccepted {
		t.Run(c.name, func(t *testing.T) {
			name := fmt.Sprintf("sound/m7acc%d", soundUnitN.Add(1))
			out := s.CheckUnit(driver.UnitInput{Name: name, Files: []driver.UnitFile{{Path: "sound.mk", Text: soundHeader + "\n" + c.entry + "\n"}}})
			if got := unitCodes(out); len(got) > 0 {
				t.Fatalf("codes %v, want none:\n%s", got, out.Report.Text(out.Sources))
			}
			for _, fa := range []uint32{0, 3} {
				for k := uint64(0); k < 16; k++ {
					r := SoundRunOn(s, SoundRunSpec{Unit: name, Fn: "Run", Args: []uint64{k}, Tag: &SoundTag{Seed: k, FailAlloc: fa}})
					if r.ErrKind != "" || r.Skipped != "" || len(r.Violations) > 0 {
						t.Fatalf("FailAlloc %d k %d: err %s %s, skipped %q, violations %v; want a clean run", fa, k, r.ErrKind, r.ErrText, r.Skipped, r.Violations)
					}
				}
			}
		})
	}
}

// viewsText is a probe's unit text: its import lines first, then the header,
// then the rest of the entry.
func viewsText(entry string) string {
	var imports string
	for strings.HasPrefix(entry, "import ") {
		line, rest, _ := strings.Cut(entry, "\n")
		imports += line + "\n"
		entry = strings.TrimLeft(rest, "\n")
	}
	if imports != "" {
		imports += "\n"
	}
	return imports + soundHeader + "\n" + entry + "\n"
}

// viewsRejected are the m7 forms misused, each rejected by one loan code, with
// the kinds a Rejected run may report: the tagged mode sees the loans of a
// value arm, an iterator's payload, an index receiver, Swap, Replace and
// Pool.GetPair. With no kinds, every Rejected run ends Skipped "poisoned":
// the run reaches a Slice[T] method (SplitAt, GetPair, ChunksMut), which
// eval runs through mono's instance, and mono serves no rejected unit
// (driver.RunUnit), so such a form's violation is shown only by a mutant
// compiler that accepts it (TestSound_Mutants), and the false-rejection run
// counts such a program Skipped, as it does a capturing call. The same
// views reached through an accepted, non-generic wrapper run, and the
// tagged mode sees their loans (split-, pair- and chunks-wrapped).
var viewsRejected = []struct {
	name, code string
	kinds      []string
	entry      string
}{
	{"arm-write", "E0502", []string{"VRemovedTag"}, `Run(var k: i64): i64 {
    var h: i64 = 0
    var v1: i64 = 5
    var v2: i64 = 6
    k = k *% 6364136223846793005 +% 1442695040888963407
    var r3 := if ((k >> 33) & 1) == 1 { &v1 } else { &v2 }
    v1 = 7
    h = h *% 31 +% *r3
    h = h *% 31 +% v2
    h = h *% 31 +% v1
    h = h *% 31 +% k
    return h
}`},
	{"iter-write", "E0502", []string{"VRemovedTag"}, `Run(var k: i64): i64 {
    var h: i64 = 0
    var xs1: [4]i64 = [1, 2, 3, 4]
    for (i, x) in It(xs: xs1[..], i: 0) {
        xs1[0] = i
        h = h *% 31 +% *x
    }
    h = h *% 31 +% xs1[0] +% xs1[1] +% xs1[2] +% xs1[3]
    h = h *% 31 +% k
    return h
}`},
	{"index-receiver", "E0501", []string{"VRemovedTag"}, `Run(var k: i64): i64 {
    var h: i64 = 0
    var cs1: [2]Ct = [Ct(n: 1), Ct(n: 2)]
    var r2 := &cs1[0].n
    cs1[0].Add(k)
    h = h *% 31 +% *r2
    h = h *% 31 +% cs1[0].n +% cs1[1].n
    h = h *% 31 +% k
    return h
}`},
	{"swap", "E0501", []string{"VRemovedTag"}, `Run(var k: i64): i64 {
    var h: i64 = 0
    var v1: i64 = 5
    var v2: i64 = 6
    var r3 := &v1
    Swap(mut v1, mut v2)
    h = h *% 31 +% *r3
    h = h *% 31 +% v2
    h = h *% 31 +% v1
    h = h *% 31 +% k
    return h
}`},
	{"replace", "E0502", []string{"VRemovedTag"}, `Run(var k: i64): i64 {
    var h: i64 = 0
    var v1: i64 = 5
    var v2: i64 = 6
    var r3 := &v2
    _ = Replace(mut r3, &v1)
    v1 = 8
    h = h *% 31 +% *r3
    h = h *% 31 +% v2
    h = h *% 31 +% v1
    h = h *% 31 +% k
    return h
}`},
	{"split", "E0502", nil, `Run(var k: i64): i64 {
    var h: i64 = 0
    var xs1: [4]i64 = [1, 2, 3, 4]
    (left: lf2, right: rt2) := xs1.SplitAt(2)
    xs1[0] = 9
    lf2[0] = 5
    h = h *% 31 +% lf2[1] +% rt2[0]
    h = h *% 31 +% xs1[0] +% xs1[1] +% xs1[2] +% xs1[3]
    h = h *% 31 +% k
    return h
}`},
	{"pair", "E0502", nil, `Run(var k: i64): i64 {
    var h: i64 = 0
    var xs1: [4]i64 = [1, 2, 3, 4]
    pq2 := xs1.GetPair(1, 2) else { return h }
    xs1[1] = 9
    *pq2.a = *pq2.b +% k
    h = h *% 31 +% xs1[0] +% xs1[1] +% xs1[2] +% xs1[3]
    h = h *% 31 +% k
    return h
}`},
	{"chunks-scope", "E0502", nil, `Run(var k: i64, io: Io): i64 {
    var h: i64 = 0
    var xs1: [4]i64 = [1, 2, 3, 4]
    scope s2 := io.Scope() {
        for c3 in xs1.ChunksMut(1) {
            s2.go Fill(c3, k)
        }
        xs1[0] = 9
    }
    h = h *% 31 +% xs1[0] +% xs1[1] +% xs1[2] +% xs1[3]
    h = h *% 31 +% k
    return h
}`},
	{"split-wrapped", "E0502", []string{"VRemovedTag"}, `Run(var k: i64): i64 {
    var h: i64 = 0
    var xs1: [4]i64 = [1, 2, 3, 4]
    (left: lf2, right: rt2) := Halves(mut xs1[..], 2)
    xs1[0] = 9
    lf2[0] = 5
    h = h *% 31 +% lf2[1] +% rt2[0]
    h = h *% 31 +% xs1[0] +% xs1[1] +% xs1[2] +% xs1[3]
    h = h *% 31 +% k
    return h
}`},
	{"pair-wrapped", "E0502", []string{"VRemovedTag"}, `Run(var k: i64): i64 {
    var h: i64 = 0
    var xs1: [4]i64 = [1, 2, 3, 4]
    pq2 := Pair(mut xs1[..], 1, 2) else { return h }
    xs1[1] = 9
    *pq2.a = *pq2.b +% k
    h = h *% 31 +% xs1[0] +% xs1[1] +% xs1[2] +% xs1[3]
    h = h *% 31 +% k
    return h
}`},
	{"chunks-wrapped", "E0501", []string{"VRemovedTag"}, `Par(io: Io, xs: mut []i64, v: i64) {
    scope s := io.Scope() {
        for c in xs.ChunksMut(1) {
            s.go Fill(c, v)
        }
    }
}

Run(var k: i64, io: Io): i64 {
    var h: i64 = 0
    var xs1: [4]i64 = [1, 2, 3, 4]
    var r2 := &xs1[2]
    Par(io, mut xs1[..], k)
    h = h *% 31 +% *r2
    h = h *% 31 +% xs1[0] +% xs1[1] +% xs1[2] +% xs1[3]
    h = h *% 31 +% k
    return h
}`},
	{"pool-pair", "E0501", []string{"VRemovedTag", "VDead"}, `import std.mem

Run(var k: i64, a: &Arena): i64 {
    var h: i64 = 0
    var p1 := mem.Pool[i64].New(a)
    g2 := p1.Add(5) else { return h }
    g3 := p1.Add(6) else { return h }
    pr4 := p1.GetPair(g2, g3) else { return h }
    _ = p1.Free(g2)
    *pr4.a = *pr4.b +% k
    for (handle, value) in p1.Iter() {
        _ = handle
        h = h *% 31 +% *value
    }
    h = h *% 31 +% k
    return h
}`},
}

// TestSoundRejected: each misuse is rejected by its code alone, and a
// Rejected run on some seed of 0 to 15 reports one of its kinds, so the
// tagged mode sees the loans the new forms make.
func TestSoundRejected(t *testing.T) {
	s := soundSess(t)
	for _, c := range viewsRejected {
		t.Run(c.name, func(t *testing.T) {
			name := fmt.Sprintf("sound/m7rej%d", soundUnitN.Add(1))
			out := s.CheckUnit(driver.UnitInput{Name: name, Files: []driver.UnitFile{{Path: "sound.mk", Text: viewsText(c.entry)}}})
			if got := unitCodes(out); !slices.Equal(got, []string{c.code}) {
				t.Fatalf("codes %v, want exactly [%s]:\n%s", got, c.code, out.Report.Text(out.Sources))
			}
			var kinds []string
			for k := uint64(0); k < 16; k++ {
				r := SoundRunOn(s, SoundRunSpec{Unit: name, Fn: "Run", Args: []uint64{k}, Tag: &SoundTag{Seed: k, Rejected: true}})
				if c.kinds == nil {
					if r.Skipped != "poisoned" {
						t.Fatalf("k %d: skipped %q, err %s %s, violations %v; want Skipped poisoned", k, r.Skipped, r.ErrKind, r.ErrText, r.Violations)
					}
					continue
				}
				if r.ErrKind != "" || r.Skipped != "" {
					t.Fatalf("k %d: err %s %s, skipped %q", k, r.ErrKind, r.ErrText, r.Skipped)
				}
				for _, v := range r.Violations {
					if !slices.Contains(kinds, v.Kind) {
						kinds = append(kinds, v.Kind)
					}
					if !slices.Contains(c.kinds, v.Kind) {
						t.Errorf("k %d: %s %s, want one of %v", k, v.Kind, v.Text, c.kinds)
					}
				}
			}
			if len(kinds) == 0 && c.kinds != nil {
				t.Errorf("no seed of 0 to 15 reports a violation; want one of %v", c.kinds)
			}
			t.Logf("%s %s: %v", c.name, c.code, kinds)
		})
	}
}

// TestSoundNoDefects: the first MK_SOUND_M7_N (default 400) programs
// of profiles m7, reject7 and oracle (base 7) check with no generator
// defect (a diagnostic outside the flow group, or an E0402 the program did
// not mean); each defect is logged with its program and diagnostics.
func TestSoundNoDefects(t *testing.T) {
	s := soundSess(t)
	n := soundInt(t, "MK_SOUND_M7_N", 400)
	for _, p := range []Profile{ProfileViews, ProfileReject7, ProfileOracle} {
		t.Run(p.String(), func(t *testing.T) {
			var mu sync.Mutex
			var wg sync.WaitGroup
			sem := make(chan struct{}, 4)
			acc, shown := 0, 0
			ret, retAcc := 0, 0 // programs with a value arm as return's operand
			codes := map[string]int{}
			for i := range n {
				wg.Add(1)
				sem <- struct{}{}
				go func() {
					defer wg.Done()
					defer func() { <-sem }()
					prog := GenerateSound(p, ProgramSeed(7, i))
					out := soundCheck(s, prog.Text)
					class, cs := classifySoundProg(prog, out.Report)
					mu.Lock()
					defer mu.Unlock()
					for _, c := range cs {
						codes[c]++
					}
					r := returnArm(prog.Text)
					if r {
						ret++
					}
					switch class {
					case "accepted":
						acc++
						if r {
							retAcc++
						}
					case "defect", "ice":
						if shown < 3 {
							shown++
							t.Errorf("program %d (seed %#x) is a %s:\n%s\n%s", i, prog.Seed, class, numbered(prog.Text), out.Report.Text(out.Sources))
						} else {
							t.Errorf("program %d (seed %#x) is a %s: %v", i, prog.Seed, class, cs)
						}
					}
				}()
			}
			wg.Wait()
			t.Logf("%s: %d of %d accepted; codes %s; a value arm as return's operand in %d programs, %d of them accepted", p, acc, n, codeTable(codes), ret, retAcc)
			if p == ProfileViews && n >= 400 && retAcc == 0 {
				t.Errorf("no accepted program returns a value arm")
			}
		})
	}
}

// TestSoundSmoke: 400 generated programs of m7 (base DefaultSoundBase),
// 4 run seeds (the fourth with FailAlloc 3): no defect, no crash, no
// evaluator error or Skipped run, no violation, at least 20 percent
// accepted, and every m7 shape (S55 to S69) among the accepted programs.
func TestSoundSmoke(t *testing.T) {
	s := soundSess(t)
	n := soundInt(t, "MK_SOUND_M7_N", 400)
	cfg := SoundConfig{Profile: ProfileViews, Base: DefaultSoundBase, Generated: n, Seeds: 4, Label: fmt.Sprintf("smoke-m7-%d", soundUnitN.Add(1))}
	rep, err := RunSound(context.Background(), cfg, NewSessionCompiler(driver.Config{}, s))
	if err != nil {
		t.Fatal(err)
	}
	t.Log(rep.Summary())
	t.Logf("rejected by code: %s", codeTable(rep.RejectedByCode))
	if n := rep.DefectTotal() + rep.ICE; n > 0 {
		t.Errorf("%d defects or crashes: defects %+v, fuel %d, panics %d, skipped %v", n, rep.Defects, rep.Fuel, rep.Panics, rep.Skipped)
	}
	for _, v := range rep.Violations {
		t.Errorf("violation: program %d (seed %s, run %s): %s", v.Program, v.Seed, v.Run, v.Text)
	}
	if rep.Accepted*5 < rep.Generated {
		t.Errorf("accepted %d of %d: under 20 percent, a generator defect (4.2.10)", rep.Accepted, rep.Generated)
	}
	var missing []string
	for id := sArmDiverge; id < sFpNew; id++ {
		if rep.Shapes[fmt.Sprintf("S%02d", id)][0] == 0 {
			missing = append(missing, fmt.Sprintf("S%02d", id))
		}
	}
	if len(missing) > 0 {
		t.Errorf("view shapes never among the accepted programs: %v", missing)
	}
	for id := sArmDiverge; id < sFpNew; id++ {
		k := fmt.Sprintf("S%02d", id)
		t.Logf("%s: %v", k, rep.Shapes[k])
	}
}

// viewsDefectSeeds are generator defects found by long runs of profile m7,
// each fixed in the generator; each program is accepted or rejected in
// the flow group only.
var viewsDefectSeeds = []uint64{
	0x9d86048b23648751, // a 600 s run at GenVersion 8
	0x77a32bde516f1ab8, // a 600 s run at GenVersion 10
}

// TestSoundDefectSeeds: the recorded defect seeds are no defect.
func TestSoundDefectSeeds(t *testing.T) {
	s := soundSess(t)
	for _, seed := range viewsDefectSeeds {
		p := GenerateSound(ProfileViews, seed)
		out := soundCheck(s, p.Text)
		if c, codes := classifySoundProg(p, out.Report); c == "defect" || c == "ice" {
			t.Errorf("seed %#x: a generator %s %v:\n%s\n%s", seed, c, codes, out.Report.Text(out.Sources), numbered(p.Text))
		}
	}
}

// TestSoundRejectBreakdown (MK_SOUND=1) splits the false-rejection sample of
// profile reject7 (MK_SOUND_REJECT programs, default 5,000, base
// DefaultSoundBase) by shape: the raw and the adjusted clean rate of the
// sampled programs with no m7 shape, with any, and with each m7 shape, so a
// rise of the rate over reject's is placed on the m7 forms that carry it.
// It fills and cuts the sample as RunReject does (rejectFill, rejectCut: index
// order, windows that are forgotten and recycled), so it counts the same
// programs as TestSound_Reject's record of the same size.
func TestSoundRejectBreakdown(t *testing.T) {
	soundLong(t)
	s := soundSess(t)
	n := soundInt(t, "MK_SOUND_REJECT", 5000)
	cfg := rejectConfig(SoundConfig{Profile: ProfileReject7, Base: DefaultSoundBase, Reject: n, Recycle: soundInt(t, "MK_SOUND_RECYCLE", 0),
		Label: fmt.Sprintf("t7m7-%d", soundUnitN.Add(1))})
	c := NewSessionCompiler(driver.Config{}, s)
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	slots, recycles, err := rejectFill(context.Background(), cfg, c)
	if err != nil {
		t.Fatal(err)
	}
	groups, idx := rejectByShape(cfg, slots)
	t.Logf("%d sampled programs of %d generated, %d recycles", len(idx), len(rejectCut(cfg, slots)), recycles)
	for _, k := range sortedKeys(groups) {
		g := groups[k]
		r, a := rateOf(g.RawClean, g.Raw), rateOf(g.AdjClean, g.Adj)
		t.Logf("%-14s raw clean %5d of %5d = %5.2f%% [%5.2f, %5.2f]; adjusted %5d of %5d = %5.2f%% [%5.2f, %5.2f]", k,
			g.RawClean, g.Raw, 100*r.Rate, 100*r.Low, 100*r.High, g.AdjClean, g.Adj, 100*a.Rate, 100*a.Low, 100*a.High)
	}
}

// returnArm reports whether text has a return whose operand is a value
// arm: if, match, or an else block on Find or Get (a refRes helper's
// result drawn by ref).
func returnArm(text string) bool {
	for _, l := range strings.Split(text, "\n") {
		l = strings.TrimSpace(l)
		if strings.HasPrefix(l, "return if ") || strings.HasPrefix(l, "return match ") ||
			(strings.HasPrefix(l, "return Find(") || strings.HasPrefix(l, "return Get(")) && strings.Contains(l, ") else {") {
			return true
		}
	}
	return false
}
