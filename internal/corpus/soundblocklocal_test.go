// Why: the soundness test sees only the loan forms the generator has a shape
// for, and five forms (match scrutinees, kept loans, owning iterators, arm
// arguments, reference views) need one each.
// What: TestFixAccepted, TestFixRejected and TestGeneratedRejections,
// over the forms named S7-1 to S7-5 and GenVersion 10's shapes S65 to S69.
// How: the forms are checked and run in the tagged mode, and generated
// programs must carry each rejection on the shape's own line.

package corpus

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"

	"mk/internal/driver"
)

// blockLocalAccepted are the five forms in the generator's spelling, after the
// header.
var blockLocalAccepted = []struct {
	name  string
	entry string
}{
	{"S7-1-scrutinee", `Run(var k: i64): i64 {
    var h: i64 = 0
    var v1: i64 = 5
    var v2: i64 = 6
    var r3 := match Find(k ^ 1, &v1) {
        some(x4) { x4 }
        none { &v2 }
    }
    var v5: i64 = match Find(k ^ 2, r3) {
        some(x6) { *x6 }
        none { 0 }
    }
    var r7 := match &Lx(src: r3, pos: 1) {
        x8 { x8.src }
    }
    var v9: i64 = match &Lx(src: &v2, pos: 2) {
        x10 { x10.pos +% *x10.src }
    }
    var r11 := match &Find(k ^ 3, &v1) {
        some(x12) { *x12 }
        none { &v2 }
    }
    var v13: i64 = if x14 := Find(k ^ 4, &v2) { *x14 } else { 3 }
    var r15 := match Get(k ^ 5, r7) {
        ok(x16) { x16 }
        err(er17) {
            _ = er17
            &v1
        }
    }
    var r18 := match Find(k ^ 6, &v1) {
        some(x19) { x19 }
        none {
            h = h *% 31 +% 3
            &v2
        }
    }
    h = h *% 31 +% *r18
    h = h *% 31 +% *r15
    h = h *% 31 +% v13
    h = h *% 31 +% *r11
    h = h *% 31 +% v9
    h = h *% 31 +% *r7
    h = h *% 31 +% v5
    h = h *% 31 +% *r3
    h = h *% 31 +% v2
    h = h *% 31 +% v1
    h = h *% 31 +% k
    return h
}`},
	{"S7-2-keep", `H1(cs: mut []Cr, r: &i64 into cs) {
    cs[0].Keep(r)
}

Run(var k: i64, a: &Arena): i64 {
    var h: i64 = 0
    var v1: i64 = 5
    var v2: i64 = 6
    var crs3: [2]Cr = [Cr(r: &v1), Cr(r: &v2)]
    crs3[1].Keep(&v1)
    for ix4 in 0..2 {
        crs3[ix4].Keep(&v2)
    }
    var gs5: [2]Gr = [Gr(rows: [Cr(r: &v1), Cr(r: &v2)]), Gr(rows: [Cr(r: &v2), Cr(r: &v1)])]
    gs5[1].rows[0].Keep(&v2)
    H1(mut crs3[..], &v1)
    var zr6 := mut gs5[0].rows[..]
    zr6[1].Keep(&v2)
    var sa7 := a.Sub() else { return h }
    defer sa7.Free()
    var l7 := List[Cr].New(&sa7)
    l7.Push(Cr(r: &v1)) else { return h }
    l7.Push(Cr(r: &v2)) else { return h }
    l7[1].Keep(&v1)
    h = h *% 31 +% *l7[0].r +% *l7[1].r
    h = h *% 31 +% *gs5[0].rows[0].r +% *gs5[0].rows[1].r +% *gs5[1].rows[0].r +% *gs5[1].rows[1].r
    h = h *% 31 +% *crs3[0].r +% *crs3[1].r
    h = h *% 31 +% v2
    h = h *% 31 +% v1
    h = h *% 31 +% k
    return h
}`},
	{"S7-3-own-iter", `Run(var k: i64): i64 {
    var h: i64 = 0
    var v1: i64 = 5
    for p2 in Bf(buf: [1, v1, 3], i: 0) {
        h = h *% 31 +% *p2
    }
    var xs3: [4]i64 = [1, 2, 3, 4]
    var it4 := It(xs: xs3[..], i: 1)
    for (i: ip5, x: xp6) in it4 {
        h = h *% 31 +% ip5 +% *xp6
    }
    h = h *% 31 +% i64(it4.i)
    h = h *% 31 +% xs3[0] +% xs3[1] +% xs3[2] +% xs3[3]
    h = h *% 31 +% v1
    h = h *% 31 +% k
    return h
}`},
	{"S7-4-arm-args", `Run(var k: i64, a: &Arena): i64 {
    var h: i64 = 0
    var v1: i64 = 5
    var b2 := a.Sub() else { return h }
    v1 +%= if ((k >> 3) & 1) == 1 { 4 } else { 9 }
    v1 *%= match (k >> 5) & 3 {
        0 { 2 }
        1 {
            b2.Free()
            return h
        }
        _ { 3 }
    }
    var cs3: [2]Ct = [Ct(n: if ((k >> 7) & 1) == 1 { 1 } else {
        b2.Free()
        return h
    }), Ct(n: 2)]
    cs3[0].Add(if ((k >> 9) & 1) == 1 { v1 } else {
        b2.Free()
        return h
    })
    var xs4: [4]i64 = [1, match (k >> 11) & 3 {
        0 { 2 }
        1 {
            b2.Free()
            return h
        }
        _ { v1 }
    }, 3, 4]
    var v5 := Replace(mut v1, if ((k >> 13) & 1) == 1 { 6 } else {
        b2.Free()
        return h
    })
    b2.Free()
    h = h *% 31 +% v5
    h = h *% 31 +% xs4[0] +% xs4[1] +% xs4[2] +% xs4[3]
    h = h *% 31 +% cs3[0].n +% cs3[1].n
    h = h *% 31 +% v1
    h = h *% 31 +% k
    return h
}`},
	{"S7-5-ref-views", `import std.mem

Run(var k: i64, a: &Arena): i64 {
    var h: i64 = 0
    var v1: i64 = 5
    var v2: i64 = 6
    var sa3 := a.Sub() else { return h }
    defer sa3.Free()
    var pl3 := mem.Pool[&i64].New(&sa3)
    g4 := pl3.Add(&v1) else { return h }
    g5 := pl3.Add(&v2) else { return h }
    if pr6 := pl3.GetPair(g4, g5) {
        *pr6.a = &v2
        Swap(mut *pr6.a, mut *pr6.b)
    }
    var rs8: [4]&i64 = [&v1, &v2, &v1, &v2]
    if pr9 := rs8.GetPair(0, 2) {
        *pr9.a = &v2
        *pr9.b = *pr9.a
    }
    (left: lf10, right: rt10) := rs8.SplitAt(1)
    lf10[0] = &v2
    rt10[2] = &v1
    for c11 in rs8.ChunksMut(3) {
        c11[0] = &v1
    }
    for c12 in PiecesMutR(mut rs8[..], 2) {
        c12[0] = &v2
    }
    if pr13 := PairR(mut rs8[..], 1, 3) {
        *pr13.a = &v1
    }
    (left: lf14, right: rt14) := HalvesR(mut rs8[..], 2)
    lf14[1] = rt14[0]
    h = h *% 31 +% *rs8[0] +% *rs8[1] +% *rs8[2] +% *rs8[3]
    for (handle: hd15, value: vl15) in pl3.Iter() {
        _ = hd15
        h = h *% 31 +% **vl15
    }
    h = h *% 31 +% v2
    h = h *% 31 +% v1
    h = h *% 31 +% k
    return h
}`},
}

// TestFixAccepted: each form checks clean and runs clean
// in the tagged mode on seeds 0 to 15, with FailAlloc 0 and 3. S7-2 and S7-5
// keep loans of locals in a List and a Pool made in a sub-arena declared
// after those locals: in the parameter arena itself, a store through an
// allocator lands in `a`, which outlives them (E0505), though the runs are
// clean.
func TestFixAccepted(t *testing.T) {
	s := soundSess(t)
	for _, c := range blockLocalAccepted {
		t.Run(c.name, func(t *testing.T) {
			name := fmt.Sprintf("sound/m7fixacc%d", soundUnitN.Add(1))
			out := s.CheckUnit(driver.UnitInput{Name: name, Files: []driver.UnitFile{{Path: "sound.mk", Text: viewsText(c.entry)}}})
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

// blockLocalRejected are the five forms misused, each rejected by one
// code, with the kinds a Rejected run may report.
var blockLocalRejected = []struct {
	name, code string
	kinds      []string
	entry      string
}{
	{"S7-1-temp-scrutinee", "E0506", []string{"VDeadScope"}, `Run(var k: i64): i64 {
    var h: i64 = 0
    var v1: i64 = 5
    var r3 := match &Lx(src: &v1, pos: 1) {
        x4 { &x4.pos }
    }
    h = h *% 31 +% *r3
    h = h *% 31 +% v1
    h = h *% 31 +% k
    return h
}`},
	{"S7-1-arm-write", "E0502", []string{"VRemovedTag"}, `Run(var k: i64): i64 {
    var h: i64 = 0
    var v1: i64 = 5
    var v2: i64 = 6
    var r3 := match Find(k ^ 1, &v1) {
        some(x4) {
            v1 = 7
            x4
        }
        none { &v2 }
    }
    h = h *% 31 +% *r3
    h = h *% 31 +% v2
    h = h *% 31 +% v1
    h = h *% 31 +% k
    return h
}`},
	{"S7-2-keep-dead", "E0505", []string{"VDeadScope"}, `Run(var k: i64): i64 {
    var h: i64 = 0
    var v1: i64 = 5
    var crs2: [2]Cr = [Cr(r: &v1), Cr(r: &v1)]
    k = k *% 6364136223846793005 +% 1442695040888963407
    if ((k >> 33) & 1) == 1 {
        var v3: i64 = 7
        crs2[0].Keep(&v3)
        h = h *% 31 +% v3
    }
    h = h *% 31 +% *crs2[0].r +% *crs2[1].r
    h = h *% 31 +% v1
    h = h *% 31 +% k
    return h
}`},
	{"S7-2-list-dead", "E0505", []string{"VDeadScope"}, `Run(var k: i64, a: &Arena): i64 {
    var h: i64 = 0
    var v1: i64 = 5
    var l2 := List[Cr].New(a)
    l2.Push(Cr(r: &v1)) else { return h }
    l2.Push(Cr(r: &v1)) else { return h }
    k = k *% 6364136223846793005 +% 1442695040888963407
    if ((k >> 33) & 1) == 1 {
        var v3: i64 = 7
        l2[1].Keep(&v3)
        h = h *% 31 +% v3
    }
    h = h *% 31 +% *l2[0].r +% *l2[1].r
    h = h *% 31 +% v1
    h = h *% 31 +% k
    return h
}`},
	{"S7-3-payload-escapes", "E0506", []string{"VDeadScope"}, `Run(var k: i64): i64 {
    var h: i64 = 0
    var v1: i64 = 5
    var r2 := &v1
    for p3 in Bf(buf: [1, 2, 3], i: 0) {
        r2 = p3
        break
    }
    h = h *% 31 +% *r2
    h = h *% 31 +% v1
    h = h *% 31 +% k
    return h
}`},
	{"S7-4-drop-arm", "E0402", []string{"VLinear"}, `Run(var k: i64, a: &Arena): i64 {
    var h: i64 = 0
    var b2 := a.Sub() else { return h }
    var t3 := Drop(a, b2, if ((k >> 3) & 1) == 1 { 4 } else { return h }) else {
        b2.Free()
        return h
    }
    h = h *% 31 +% t3
    h = h *% 31 +% k
    return h
}`},
	{"S7-5-pool-dead", "E0505", []string{"VDeadScope"}, `import std.mem

Run(var k: i64, a: &Arena): i64 {
    var h: i64 = 0
    var v1: i64 = 5
    var pl2 := mem.Pool[&i64].New(a)
    g3 := pl2.Add(&v1) else { return h }
    _ = g3
    k = k *% 6364136223846793005 +% 1442695040888963407
    if ((k >> 33) & 1) == 1 {
        var v4: i64 = 7
        g5 := pl2.Add(&v4) else { return h }
        _ = g5
    }
    for (handle: hd6, value: vl6) in pl2.Iter() {
        _ = hd6
        h = h *% 31 +% **vl6
    }
    h = h *% 31 +% v1
    h = h *% 31 +% k
    return h
}`},
	{"S7-5-pair-dead", "E0505", []string{"VDeadScope"}, `Run(var k: i64): i64 {
    var h: i64 = 0
    var v1: i64 = 5
    var rs2: [4]&i64 = [&v1, &v1, &v1, &v1]
    k = k *% 6364136223846793005 +% 1442695040888963407
    if ((k >> 33) & 1) == 1 {
        var v3: i64 = 7
        if pr4 := PairR(mut rs2[..], 0, 2) {
            *pr4.b = &v3
        }
        h = h *% 31 +% v3
    }
    h = h *% 31 +% *rs2[0] +% *rs2[1] +% *rs2[2] +% *rs2[3]
    h = h *% 31 +% v1
    h = h *% 31 +% k
    return h
}`},
	{"S7-5-split-dead", "E0505", []string{"VDeadScope"}, `Run(var k: i64): i64 {
    var h: i64 = 0
    var v1: i64 = 5
    var rs2: [4]&i64 = [&v1, &v1, &v1, &v1]
    k = k *% 6364136223846793005 +% 1442695040888963407
    if ((k >> 33) & 1) == 1 {
        var v3: i64 = 7
        (left: lf4, right: rt4) := HalvesR(mut rs2[..], 1)
        rt4[0] = &v3
        lf4[0] = rt4[1]
        h = h *% 31 +% v3
    }
    h = h *% 31 +% *rs2[0] +% *rs2[1] +% *rs2[2] +% *rs2[3]
    h = h *% 31 +% v1
    h = h *% 31 +% k
    return h
}`},
	{"S7-5-chunks-dead", "E0505", []string{"VDeadScope"}, `Run(var k: i64): i64 {
    var h: i64 = 0
    var v1: i64 = 5
    var rs2: [4]&i64 = [&v1, &v1, &v1, &v1]
    k = k *% 6364136223846793005 +% 1442695040888963407
    if ((k >> 33) & 1) == 1 {
        var v3: i64 = 7
        for c4 in PiecesMutR(mut rs2[..], 2) {
            c4[1] = &v3
        }
        h = h *% 31 +% v3
    }
    h = h *% 31 +% *rs2[0] +% *rs2[1] +% *rs2[2] +% *rs2[3]
    h = h *% 31 +% v1
    h = h *% 31 +% k
    return h
}`},
}

// TestFixRejected: each misuse is rejected by its code
// alone, and a Rejected run on some seed of 0 to 15 reports one of its
// kinds and no other.
func TestFixRejected(t *testing.T) {
	s := soundSess(t)
	for _, c := range blockLocalRejected {
		t.Run(c.name, func(t *testing.T) {
			name := fmt.Sprintf("sound/m7fixrej%d", soundUnitN.Add(1))
			out := s.CheckUnit(driver.UnitInput{Name: name, Files: []driver.UnitFile{{Path: "sound.mk", Text: viewsText(c.entry)}}})
			if got := unitCodes(out); !slices.Equal(got, []string{c.code}) {
				t.Fatalf("codes %v, want exactly [%s]:\n%s", got, c.code, out.Report.Text(out.Sources))
			}
			var kinds []string
			for k := uint64(0); k < 16; k++ {
				r := SoundRunOn(s, SoundRunSpec{Unit: name, Fn: "Run", Args: []uint64{k}, Tag: &SoundTag{Seed: k, Rejected: true}})
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
			if len(kinds) == 0 {
				t.Errorf("no seed of 0 to 15 reports a violation; want one of %v", c.kinds)
			}
		})
	}
}

// blockLocalTargets are the rejections each new shape is built to produce: the
// shape, the code, the violation the rule prevents, and the shape's own
// line the code must be reported on (the line of the diagnostic's start,
// and for a write in an arm, the line before it), so the rejection is the
// shape's and not another shape's in the same program.
var blockLocalTargets = []struct {
	shape      int
	code, kind string
	line, prev *regexp.Regexp
}{
	{sArmScrut, "E0506", "VDeadScope", regexp.MustCompile(`\{ &x\d+\.pos \}`), nil},                                    // S7-1: a reference into the scrutinee temporary
	{sArmScrut, "E0502", "VRemovedTag", regexp.MustCompile(`^\s*v\d+ = \d+$`), regexp.MustCompile(`some\(x\d+\) \{$`)}, // S7-1: an arm writes what the scrutinee borrows
	{sIdxKeep, "E0505", "VDeadScope", regexp.MustCompile(`\.Keep\(&v`), nil},                                           // S7-2: Keep of a block-local int's reference through an index
	{sOwnIter, "E0506", "VDeadScope", regexp.MustCompile(` in Bf\(`), nil},                                             // S7-3: the payload outlives the hidden iterator var
	{sArmArg, "E0402", "VLinear", regexp.MustCompile(`:= Drop\(a, `), nil},                                             // S7-4: an arm leaves while an arena is in Drop's slot
	{sRefViews, "E0505", "VDeadScope", regexp.MustCompile(`(\*pr\d+\.a|(lf|rt|c)\d+\[\d+\]) = &v|\.Add\(&v`), nil},     // S7-5: a block-local int's reference stored through a view or into a pool
}

// codeLines are the source lines (1-based) of out's diagnostics of code c.
func codeLines(out driver.UnitOutcome, c string) []int {
	var ls []int
	for _, e := range out.Report.Diags {
		if e.Diag.Code.String() == c {
			ls = append(ls, int(out.Sources.Position(e.Diag.At.StartPos()).Line))
		}
	}
	return ls
}

// onLine reports whether a diagnostic of code c is on a line of text that
// matches line (and whose previous line matches prev, when set).
func onLine(out driver.UnitOutcome, text, c string, line, prev *regexp.Regexp) bool {
	ls := strings.Split(text, "\n")
	for _, n := range codeLines(out, c) {
		if n < 2 || n > len(ls) || !line.MatchString(ls[n-1]) {
			continue
		}
		if prev == nil || prev.MatchString(ls[n-2]) {
			return true
		}
	}
	return false
}

// TestGeneratedRejections: among MK_SOUND_M7_N (default
// 3,000) generated programs of reject7 (base 7), each new shape is in an
// accepted program, and each target is met by a program that carries the
// shape, has the target code on the shape's own line, and whose Rejected
// run reports the target kind. And S66's helper form: a program with E0604
// on a helper's Keep line whose clause-restored twin has none there (the
// clause went through the clause draws, so the twin restores it). The
// generator produces the forms that were missing, and the tagged mode sees
// in them what each rule prevents: the in-tree stand-in for the mutation
// test, which runs separately.
func TestGeneratedRejections(t *testing.T) {
	s := soundSess(t)
	n := soundInt(t, "MK_SOUND_M7_N", 3000)
	hit := make([]int, len(blockLocalTargets))
	on := make([]int, len(blockLocalTargets)) // the code on the shape's line, whatever the run reports
	acc := map[int]int{}
	keep604, keepTwin := 0, 0
	keepLine := regexp.MustCompile(`^\s*cs\d+\[\d\]\.Keep\(`)
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 4)
	for i := range n {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			p := GenerateSound(ProfileReject7, ProgramSeed(7, i))
			name := fmt.Sprintf("sound/m7fixgen%d", soundUnitN.Add(1))
			defer s.Forget([]string{name})
			out := s.CheckUnit(driver.UnitInput{Name: name, Files: []driver.UnitFile{{Path: "sound.mk", Text: p.Text}}})
			class, codes := classifySoundProg(p, out.Report)
			if class == "accepted" {
				mu.Lock()
				for _, id := range p.Shapes {
					acc[int(id)]++
				}
				mu.Unlock()
				return
			}
			if class != "rejected" {
				return
			}
			if slices.Contains(codes, "E0604") && onLine(out, p.Text, "E0604", keepLine, nil) {
				tw := GenerateSoundTwin(ProfileReject7, p.Seed)
				tn := name + "-twin"
				tout := s.CheckUnit(driver.UnitInput{Name: tn, Files: []driver.UnitFile{{Path: "sound.mk", Text: tw.Text}}})
				s.Forget([]string{tn})
				mu.Lock()
				keep604++
				if !onLine(tout, tw.Text, "E0604", keepLine, nil) {
					keepTwin++
				}
				mu.Unlock()
			}
			var want []int
			for j, tg := range blockLocalTargets {
				if slices.Contains(codes, tg.code) && slices.Contains(p.Shapes, uint16(tg.shape)) && onLine(out, p.Text, tg.code, tg.line, tg.prev) {
					want = append(want, j)
				}
			}
			mu.Lock()
			for _, j := range want {
				on[j]++
			}
			mu.Unlock()
			if len(want) == 0 {
				return
			}
			kinds := map[string]bool{}
			for _, k := range RunSeeds(p.Seed, RejectSeeds) {
				r := SoundRunOn(s, SoundRunSpec{Unit: name, Fn: "Run", Args: []uint64{k}, Tag: &SoundTag{Seed: k, Rejected: true}})
				for _, v := range r.Violations {
					kinds[v.Kind] = true
				}
			}
			mu.Lock()
			for _, j := range want {
				if kinds[blockLocalTargets[j].kind] {
					hit[j]++
				}
			}
			mu.Unlock()
		}()
	}
	wg.Wait()
	for id := sArmScrut; id < sFpNew; id++ {
		if acc[id] == 0 {
			t.Errorf("S%02d is in no accepted program of %d", id, n)
		}
	}
	for j, tg := range blockLocalTargets {
		t.Logf("S%02d %s on its line in %d programs, %s in %d of them (accepted programs with the shape: %d)", tg.shape, tg.code, on[j], tg.kind, hit[j], acc[tg.shape])
		if hit[j] == 0 {
			t.Errorf("S%02d: no generated program with %s on the shape's line reports %s", tg.shape, tg.code, tg.kind)
		}
	}
	t.Logf("S66 helper: E0604 on a Keep line in %d programs, restored by the twin in %d", keep604, keepTwin)
	if keepTwin == 0 {
		t.Errorf("S66: no program has E0604 on a helper's Keep line that its twin restores")
	}
}
