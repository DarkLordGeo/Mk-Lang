// Why: a mutant of a move or init code is shown needed only if the
// generator emits programs that code alone rejects, and the tagged mode
// flags them when the code is muted.
// What: TestSoundMoves_Calibration, TestSoundMoves_Generated,
// TestSound_GlobalsEnd and TestSound_FailAlloc, over the shapes S35 to S38.
// How: one minimal program per code in the generator's spelling, plus a
// count of single-code programs in a generated sample.
//
// The codes are E0401, E0402, E0403, E0405, E0406 and E0409 (GenVersion 2).
// TestSoundMoves_Calibration checks the second half on one minimal program
// per code in the generator's own spelling: the real compiler gives
// exactly that code, and a Rejected tagged run on some seed reports the
// violation the rule prevents (as doc/memory.md's guarantees state it).
// TestSoundMoves_Generated checks the first half.

package corpus

import (
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"

	"mk/internal/driver"
)

// moveCalib is one minimal program per move or init code, written as S35
// to S38 and S34's consuming helper write them, with the violation
// kinds a muted code allows. E0411's helper frees b by defer, not onerr:
// a Rejected run stops at an onerr guard of a rejected body (no borrow
// decision), so the generator's form is calibrated by the mutant itself.
// Its failure point is the second Sub: the first reuses the chunk bp's
// Free gave back to the heap, so no page allocation can fail there.
var moveCalib = []struct {
	code  string
	kinds []string
	entry string
	fail  uint32 // FailAlloc of the runs
}{
	{"E0401", []string{"VUninit"}, `Run(var k: i64): i64 {
    var h: i64 = 0
    var v1: i64 = 5
    var s1 := Sl(m: mut v1)
    var s2 := s1
    h = h *% 31 +% *s2.m
    h = h *% 31 +% *s1.m
    return h
}`, 0},
	{"E0403", []string{"VUninit"}, `Run(var k: i64): i64 {
    var h: i64 = 0
    u1: i64
    k = k *% 6364136223846793005 +% 1442695040888963407
    if ((k >> 33) & 1) == 1 {
        u1 = 5
    }
    h = h *% 31 +% u1
    return h
}`, 0},
	{"E0405", []string{"VUninit"}, `Run(var k: i64): i64 {
    var h: i64 = 0
    var v1: i64 = 5
    var s1 := Sl(m: mut v1)
    for ix in 0..3 {
        h = h *% 31 +% i64(ix)
        var s2 := s1
        h = h *% 31 +% *s2.m
    }
    return h
}`, 0},
	{"E0409", []string{"VUninit"}, `Run(var k: i64): i64 {
    var h: i64 = 0
    var v1: i64 = 5
    var s1 := Sl(m: mut v1)
    var t1 := s1.m
    h = h *% 31 +% *t1
    h = h *% 31 +% *s1.m
    return h
}`, 0},
	{"E0402", []string{"VLinear"}, `Run(var k: i64, a: &Arena): i64 {
    var h: i64 = 0
    var b1 := a.Sub() else {
        return h
    }
    k = k *% 6364136223846793005 +% 1442695040888963407
    if ((k >> 33) & 1) == 1 {
        b1.Free()
    }
    h = h *% 31 +% k
    return h
}`, 0},
	{"E0406", []string{"VUninit", "VDead"}, `Run(var k: i64, a: &Arena): i64 {
    var h: i64 = 0
    var b1 := a.Sub() else {
        return h
    }
    defer b1.Free()
    b1.Free()
    h = h *% 31 +% k
    return h
}`, 0},
	{"E0411", []string{"VUninit", "VDead"}, `H1(a: &Arena, bp: Arena): OutOfMemory!i64 {
    var h: i64 = 0
    bp.Free()
    b := try a.Sub()
    defer b.Free()
    h = h *% 31 +% 3
    t := try b.Sub()
    t.Free()
    return h
}

Run(var k: i64, a: &Arena): i64 {
    var h: i64 = 0
    var b1 := a.Sub() else {
        return h
    }
    var t2 := H1(a, b1) else {
        b1.Free()
        return h
    }
    h = h *% 31 +% t2
    h = h *% 31 +% k
    return h
}`, 2},
	// E0411 with GenVersion 5's late operand: bp goes through an argument
	// slot, which the tagged mode restores on the error edge as borrow does.
	{"E0411", []string{"VUninit", "VDead"}, `H1(a: &Arena, bp: Arena, n: i64): OutOfMemory!i64 {
    var h: i64 = 0
    bp.Free()
    h = h *% 31 +% n
    b := try a.Sub()
    defer b.Free()
    h = h *% 31 +% 3
    t := try b.Sub()
    t.Free()
    return h
}

Run(var k: i64, a: &Arena): i64 {
    var h: i64 = 0
    var b1 := a.Sub() else {
        return h
    }
    var t2 := H1(a, b1, k ^ 1) else {
        b1.Free()
        return h
    }
    h = h *% 31 +% t2
    h = h *% 31 +% k
    return h
}`, 2},
	{"E0506", []string{"VDeadScope"}, `Run(var k: i64): i64 {
    var h: i64 = 0
    var v1: i64 = 5
    var r2 := &Lx(src: &v1, pos: 3).pos
    h = h *% 31 +% *r2
    return h
}`, 0},
	{"E0507", []string{"VRemovedTag"}, `Run(var k: i64): i64 {
    var h: i64 = 0
    var v1: i64 = 5
    var s1 := Sl(m: mut v1)
    p2 := &s1
    var r3 := PeekS(p2)
    *p2.m = 7
    h = h *% 31 +% *r3
    h = h *% 31 +% *s1.m
    return h
}`, 0},
	{"E0603", []string{"VDead", "VDeadScope"}, `H1(x1: &i64): &i64 from x1 {
    var h: i64 = 0
    h = h *% 31 +% *x1
    return &h
}

Run(var k: i64): i64 {
    var h: i64 = 0
    var v1: i64 = 5
    h = h *% 31 +% *H1(&v1)
    return h
}`, 0},
}

// unitCodes is the sorted set of codes of a checked unit.
func unitCodes(out driver.UnitOutcome) []string {
	var cs []string
	for _, e := range out.Report.Diags {
		if c := e.Diag.Code.String(); !slices.Contains(cs, c) {
			cs = append(cs, c)
		}
	}
	slices.Sort(cs)
	return cs
}

// TestSoundMoves_Calibration: each program is rejected by its code alone,
// and a Rejected tagged run (seeds 0 to 15, which are also its k) reports
// one of the code's violation kinds.
func TestSoundMoves_Calibration(t *testing.T) {
	s := soundSess(t)
	for _, c := range moveCalib {
		t.Run(c.code, func(t *testing.T) {
			name := fmt.Sprintf("sound/moves%d", soundUnitN.Add(1))
			out := s.CheckUnit(driver.UnitInput{Name: name, Files: []driver.UnitFile{{Path: "sound.mk", Text: soundHeader + "\n" + c.entry + "\n"}}})
			if got := unitCodes(out); !slices.Equal(got, []string{c.code}) {
				t.Fatalf("codes %v, want exactly [%s]:\n%s", got, c.code, out.Report.Text(out.Sources))
			}
			var kinds []string
			for k := uint64(0); k < 16; k++ {
				r := SoundRunOn(s, SoundRunSpec{Unit: name, Fn: "Run", Args: []uint64{k}, Tag: &SoundTag{Seed: k, Rejected: true, FailAlloc: c.fail}})
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
				t.Errorf("no seed of 0 to 15 reports a violation; want one of %v (the mutant of %s could never be shown needed)", c.kinds, c.code)
			}
			t.Logf("%s: %v", c.code, kinds)
		})
	}
}

// TestSoundMoves_Generated: among the first MK_SOUND_MOVES programs of
// profile full (default 3,000, base 13), each move or init code is the
// only code of some program, so its mutant has programs to accept,
// and the acceptance stays at or above 30 percent. The rarer
// codes (E0401, E0405, E0406, E0409, E0507: 2 to 7 alone in 10,000) must
// only appear in the default sample.
func TestSoundMoves_Generated(t *testing.T) {
	s := soundSess(t)
	n := soundInt(t, "MK_SOUND_MOVES", 3000)
	only, any := map[string]int{}, map[string]int{}
	acc, def := 0, 0
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 4)
	for i := range n {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			p := GenerateSound(ProfileFull, ProgramSeed(13, i))
			out := soundCheck(s, p.Text)
			class, codes := classifySoundProg(p, out.Report)
			mu.Lock()
			defer mu.Unlock()
			switch class {
			case "accepted":
				acc++
			case "defect", "ice":
				def++
				if def <= 2 {
					t.Errorf("program %d (seed %#x) is a generator %s:\n%s", i, p.Seed, class, out.Report.Text(out.Sources))
				}
			}
			if len(codes) == 1 && (strings.HasPrefix(codes[0], "E04") || codes[0] == "E0506" || codes[0] == "E0507" || codes[0] == "E0603") {
				only[codes[0]]++
			}
			for _, c := range codes {
				if strings.HasPrefix(c, "E04") || c == "E0506" || c == "E0507" || c == "E0603" {
					any[c]++
				}
			}
		}()
	}
	wg.Wait()
	t.Logf("%d of %d accepted (%.1f%%), %d defects; programs rejected by one move or init code alone: %s; with it among others: %s",
		acc, n, 100*float64(acc)/float64(n), def, codeTable(only), codeTable(any))
	// The rarer codes (a few alone per 10,000 programs at version 3) must
	// appear; the mutant runs judge hundreds of thousands.
	for _, c := range []string{"E0401", "E0405", "E0406", "E0409", "E0507"} {
		if any[c] == 0 {
			t.Errorf("no program of %d has %s", n, c)
		}
	}
	for _, c := range []string{"E0402", "E0403", "E0411", "E0506", "E0603"} {
		if only[c] == 0 {
			t.Errorf("no program of %d is rejected by %s alone: its mutant has nothing to accept", n, c)
		}
	}
	if 10*acc < 3*n {
		t.Errorf("acceptance %.1f%% is under 4.2.10's 30 percent", 100*float64(acc)/float64(n))
	}
}

// TestSound_GlobalsEnd: the host reads every reference a unit global
// holds once more after the entry returns (eval's tgGlobalsEnd, the
// ending past the return). A frame's loan stored in the global mutex
// (E0605) is then a VDead on every seed; a static loan stays clean.
func TestSound_GlobalsEnd(t *testing.T) {
	s := soundSess(t)
	prog := func(ref string) string {
		return `const K0: i64 = 7
var GM: Mutex[&i64] = Mutex.New(&K0)

Run(var k: i64, io: Io): i64 {
    var h: i64 = 0
    var v1: i64 = 5
    var g2 := GM.Lock(io) else {
        return h
    }
    g2.Store(` + ref + `)
    g2.Unlock()
    h = h *% 31 +% v1
    h = h *% 31 +% k
    return h
}
`
	}
	for _, c := range []struct {
		ref, code, kind string
	}{{"&v1", "E0605", "VDead"}, {"&K0", "", ""}} {
		name := fmt.Sprintf("sound/globals%d", soundUnitN.Add(1))
		out := s.CheckUnit(driver.UnitInput{Name: name, Files: []driver.UnitFile{{Path: "sound.mk", Text: prog(c.ref)}}})
		got, want := unitCodes(out), []string{}
		if c.code != "" {
			want = []string{c.code}
		}
		if !slices.Equal(got, want) {
			t.Fatalf("%s: codes %v, want %v:\n%s", c.ref, got, want, out.Report.Text(out.Sources))
		}
		for k := uint64(0); k < 4; k++ {
			r := SoundRunOn(s, SoundRunSpec{Unit: name, Fn: "Run", Args: []uint64{k}, Tag: &SoundTag{Seed: k, Rejected: c.code != ""}})
			if r.ErrKind != "" || r.Skipped != "" {
				t.Fatalf("%s k %d: err %s %s, skipped %q", c.ref, k, r.ErrKind, r.ErrText, r.Skipped)
			}
			switch {
			case c.kind == "" && len(r.Violations) > 0:
				t.Errorf("%s k %d: %v, want a clean run", c.ref, k, r.Violations)
			case c.kind != "" && (len(r.Violations) == 0 || r.Violations[0].Kind != c.kind):
				t.Errorf("%s k %d: %v, want %s at the end of the run", c.ref, k, r.Violations, c.kind)
			}
		}
	}
}

// TestSound_FailAlloc: with TagOptions.FailAlloc 1 every page allocation
// after the entry started fails, so a.Sub() takes its else path; S34's
// helper with a failure point after its arena is consumed runs clean on
// every seed and FailAlloc (the onerr of a moved value is skipped), and so
// does the host's own &Arena setup, which never fails.
func TestSound_FailAlloc(t *testing.T) {
	s := soundSess(t)
	text := `H1(a: &Arena): OutOfMemory!i64 {
    var h: i64 = 0
    b := try a.Sub()
    onerr b.Free()
    h = h *% 31 +% 3
    t := try b.Sub()
    t.Free()
    b.Free()
    u := try a.Sub()
    u.Free()
    return h
}

Run(var k: i64, a: &Arena): i64 {
    var h: i64 = 0
    var b1 := a.Sub() else {
        h = h *% 31 +% 1
        return h
    }
    b1.Free()
    var t2 := H1(a) else {
        h = h *% 31 +% 2
        return h
    }
    h = h *% 31 +% t2
    return h
}
`
	name := fmt.Sprintf("sound/failalloc%d", soundUnitN.Add(1))
	out := s.CheckUnit(driver.UnitInput{Name: name, Files: []driver.UnitFile{{Path: "sound.mk", Text: text}}})
	if got := unitCodes(out); len(got) > 0 {
		t.Fatalf("codes %v, want none:\n%s", got, out.Report.Text(out.Sources))
	}
	values := map[uint64]bool{}
	for _, fa := range []uint32{0, 1, 2, 3} {
		for k := uint64(0); k < 16; k++ {
			r := SoundRunOn(s, SoundRunSpec{Unit: name, Fn: "Run", Args: []uint64{k}, Tag: &SoundTag{Seed: k, FailAlloc: fa}})
			if r.ErrKind != "" || r.Skipped != "" || len(r.Violations) > 0 {
				t.Fatalf("FailAlloc %d k %d: err %s %s, skipped %q, violations %v; want a clean run", fa, k, r.ErrKind, r.ErrText, r.Skipped, r.Violations)
			}
			if fa == 1 && r.Value != 1 {
				t.Errorf("FailAlloc 1 k %d: value %d, want 1 (the first Sub fails)", k, r.Value)
			}
			values[r.Value] = true
		}
	}
	if len(values) < 3 {
		t.Errorf("values %v: want the first else, the helper's error edge and success all taken", values)
	}
}
