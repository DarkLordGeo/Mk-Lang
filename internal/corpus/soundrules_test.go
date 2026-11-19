// Why: a mutant of a permissive rule is shown needed only by programs the
// real compiler accepts, so the generator needs shapes aimed at those rules.
// What: TestSoundRules_Accepted, TestSoundRules_Rejected and
// TestSoundRules_Generated, with calibRejected.
// How: each shape's form is checked and run in the tagged mode, and a
// generated sample must contain accepted programs with each shape.
//
// The shapes: GenVersion 4's S41, S42 and S20's keeping cell, and the arena
// liveness test; GenVersion 5's S34 consuming helper with a late operand, for
// the argument slot. A class P mutant is shown needed only when the unmutated
// compiler accepts some program the mutant rejects, so the shape's form must
// be accepted and run clean (TestSoundRules_Accepted); a class R mutant needs
// a program its code alone rejects whose Rejected run reports the violation
// (TestSoundRules_Rejected); and the generator must emit accepted programs
// with each shape (TestSoundRules_Generated). Whether a mutant flips them is
// TestSound_Mutants' question (MK_SOUND=1).

package corpus

import (
	"fmt"
	"slices"
	"sync"
	"testing"

	"mk/internal/driver"
)

// rulesAccepted are the permissive rules' forms in the generator's
// spelling: each is accepted, and its mutant (named) rejects it, which
// was checked with mk built through go build -overlay.
var rulesAccepted = []struct {
	mutant string
	entry  string
}{
	{"M-R15-kill", `Run(var k: i64): i64 {
    var h: i64 = 0
    var v1: i64 = 1
    var v2: i64 = 2
    var m3 := mut v1
    y4 := mut *m3
    *y4 = 3
    m3 = mut v2
    *m3 = 4
    h = h *% 31 +% *y4
    h = h *% 31 +% *m3
    h = h *% 31 +% v2
    h = h *% 31 +% v1
    h = h *% 31 +% k
    return h
}`},
	{"M-R20-disjoint", `Run(var k: i64): i64 {
    var h: i64 = 0
    var v1: i64 = 5
    var xs2: [4]i64 = [1, 2, 3, 4]
    c3 := Cell.New(&v1)
    for x4 in mut xs2 {
        *x4 = *x4 +% 1
        c3.Store(&*x4)
    }
    h = h *% 31 +% *c3.Load()
    h = h *% 31 +% xs2[0] +% xs2[1] +% xs2[2] +% xs2[3]
    h = h *% 31 +% v1
    h = h *% 31 +% k
    return h
}`},
	{"M-R13-order", `H1(a: &Arena, bp: Arena, n: i64): OutOfMemory!i64 {
    var h: i64 = 0
    h = h *% 31 +% n
    b := try a.Sub()
    onerr b.Free()
    h = h *% 31 +% 3
    t := try b.Sub()
    t.Free()
    b.Free()
    bp.Free()
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
}`},
	{"M-R13-order (S43)", `Run(var k: i64, a: &Arena): i64 {
    var h: i64 = 0
    var b1 := a.Sub() else {
        return h
    }
    var t2 := Drop(a, b1, k ^ 1) else {
        b1.Free()
        return h
    }
    h = h *% 31 +% t2
    h = h *% 31 +% k
    return h
}`},
	{"M-R18-exempt, M-R18-ends", `Run(var k: i64, a: &Arena): i64 {
    var h: i64 = 0
    var b1 := a.Sub() else {
        return h
    }
    var w2 := b1.Put(i64(7)) else {
        b1.Free()
        return h
    }
    var d3 := b1.Put(Cell.New(&*w2)) else {
        b1.Free()
        return h
    }
    h = h *% 31 +% *d3.Load()
    b1.Reset()
    b1.Free()
    h = h *% 31 +% k
    return h
}`},
}

// rulesRejected is the arena liveness test (M-R18-live): a cell carved from an
// arena still read after the arena's Free. E0503 alone; a Rejected run
// reads freed memory.
var rulesRejected = []struct {
	code  string
	kinds []string
	entry string
}{
	{"E0503", []string{"VDead"}, `Run(var k: i64, a: &Arena): i64 {
    var h: i64 = 0
    var v1: i64 = 5
    var b2 := a.Sub() else {
        return h
    }
    var d3 := b2.Put(Cell.New(&v1)) else {
        b2.Free()
        return h
    }
    b2.Free()
    h = h *% 31 +% *d3.Load()
    h = h *% 31 +% v1
    h = h *% 31 +% k
    return h
}`},
}

// TestSoundRules_Accepted: each form checks clean and runs clean in the
// tagged mode on seeds 0 to 15, with FailAlloc 0 and 3.
func TestSoundRules_Accepted(t *testing.T) {
	s := soundSess(t)
	for _, c := range rulesAccepted {
		t.Run(c.mutant, func(t *testing.T) {
			name := fmt.Sprintf("sound/rules%d", soundUnitN.Add(1))
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

// TestSoundRules_Rejected: each program is rejected by its code alone, and
// a Rejected tagged run on some seed of 0 to 15 reports one of its kinds.
func TestSoundRules_Rejected(t *testing.T) {
	s := soundSess(t)
	for _, c := range rulesRejected {
		t.Run(c.code, func(t *testing.T) {
			kinds := calibRejected(t, s, c.code, c.kinds, c.entry, 0)
			t.Logf("%s: %v", c.code, kinds)
		})
	}
}

// calibRejected checks entry (after the header) as a unit: exactly code,
// and every violation of a Rejected run on seeds 0 to 15 one of kinds,
// with at least one; it returns the kinds seen.
func calibRejected(t *testing.T, s *driver.CorpusSession, code string, want []string, entry string, fail uint32) []string {
	t.Helper()
	name := fmt.Sprintf("sound/calib%d", soundUnitN.Add(1))
	out := s.CheckUnit(driver.UnitInput{Name: name, Files: []driver.UnitFile{{Path: "sound.mk", Text: soundHeader + "\n" + entry + "\n"}}})
	if got := unitCodes(out); !slices.Equal(got, []string{code}) {
		t.Fatalf("codes %v, want exactly [%s]:\n%s", got, code, out.Report.Text(out.Sources))
	}
	var kinds []string
	for k := uint64(0); k < 16; k++ {
		r := SoundRunOn(s, SoundRunSpec{Unit: name, Fn: "Run", Args: []uint64{k}, Tag: &SoundTag{Seed: k, Rejected: true, FailAlloc: fail}})
		if r.ErrKind != "" || r.Skipped != "" {
			t.Fatalf("k %d: err %s %s, skipped %q", k, r.ErrKind, r.ErrText, r.Skipped)
		}
		for _, v := range r.Violations {
			if !slices.Contains(kinds, v.Kind) {
				kinds = append(kinds, v.Kind)
			}
			if !slices.Contains(want, v.Kind) {
				t.Errorf("k %d: %s %s, want one of %v", k, v.Kind, v.Text, want)
			}
		}
	}
	if len(kinds) == 0 {
		t.Errorf("no seed of 0 to 15 reports a violation; want one of %v (the mutant of %s could never be shown needed)", want, code)
	}
	return kinds
}

// TestSoundRules_Generated: among the first MK_SOUND_MOVES programs of
// profile full (default 3,000, base 13), some accepted program has S41 and
// some has S42, so their mutants have programs to drop, and some program
// is rejected by E0503 with S42 among its shapes (M-R18-live's programs).
// It logs the acceptance of the programs with each shape and their codes.
func TestSoundRules_Generated(t *testing.T) {
	s := soundSess(t)
	n := soundInt(t, "MK_SOUND_MOVES", 3000)
	type tally struct {
		with, acc int
		codes     map[string]int
	}
	shapes := []int{sArray, sReborrowKill, sArenaTie, sDropSlot}
	tal := map[int]*tally{}
	for _, id := range shapes {
		tal[id] = &tally{codes: map[string]int{}}
	}
	var mu sync.Mutex
	var wg sync.WaitGroup
	// MK_SOUND_RULES_DEBUG=<shape id> logs the first three rejected
	// programs with that shape, and their diagnostics.
	dbg, dumped := soundInt(t, "MK_SOUND_RULES_DEBUG", 0), 0
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
			if dbg > 0 && class != "accepted" && slices.Contains(p.Shapes, uint16(dbg)) && dumped < 3 {
				dumped++
				t.Logf("program %d:\n%s\n%s", i, p.Text, out.Report.Text(out.Sources))
			}
			for _, id := range shapes {
				if !slices.Contains(p.Shapes, uint16(id)) {
					continue
				}
				x := tal[id]
				x.with++
				if class == "accepted" {
					x.acc++
				}
				for _, c := range codes {
					x.codes[c]++
				}
			}
		}()
	}
	wg.Wait()
	for _, id := range shapes {
		x := tal[id]
		t.Logf("S%02d: in %d of %d programs, %d accepted (%.1f%%); codes of those rejected: %s", id, x.with, n, x.acc,
			100*float64(x.acc)/float64(max(x.with, 1)), codeTable(x.codes))
		if x.acc == 0 {
			t.Errorf("S%02d: no accepted program of %d: its mutant has nothing to drop", id, n)
		}
	}
	if tal[sArenaTie].codes["E0503"] == 0 {
		t.Errorf("no program with S42 of %d is rejected by E0503: M-R18-live has nothing to accept", n)
	}
}
