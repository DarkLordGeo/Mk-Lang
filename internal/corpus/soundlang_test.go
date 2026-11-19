// Why: the lang and vec generator profiles cover closures, captures and
// SIMD vectors, and the soundness test must see their violations.
// What: TestSoundLang_Smoke, TestSoundLang_Rejected, TestSoundVec_TaggedAccess,
// TestSoundGen_E0411Lever and TestSoundGen_CapMisuseNoDefect.
// How: runs generated and hand-written programs in the tagged mode and
// checks the codes and violation kinds each reports.
//
// The profiles lang and vec (GenVersion 7) run clean in the tagged mode
// with no generator defect; the capture result restriction has a program
// whose Rejected run shows the violation its removal would let through
// (the evidence M-E0609-flow needs); the tagged mode sees simd.Vec's loads
// and stores (FromSlice and CopyTo go through its access rule, so the vec
// profile is checked, not only run); and M-E0411's lever fails on every
// run.

package corpus

import (
	"context"
	"fmt"
	"testing"

	"mk/internal/driver"
)

// TestSoundLang_Smoke: 400 generated programs each of lang and vec (base
// DefaultSoundBase), 4 run seeds (the fourth with FailAlloc 3): no defect,
// no crash, no evaluator error or Skipped run, no violation, the acceptance
// rate logged with the codes, and the new shapes among the accepted.
func TestSoundLang_Smoke(t *testing.T) {
	s := soundSess(t)
	for _, p := range []Profile{ProfileLang, ProfileVec} {
		t.Run(p.String(), func(t *testing.T) {
			cfg := SoundConfig{Profile: p, Base: DefaultSoundBase, Generated: 400, Seeds: 4, Label: fmt.Sprintf("smoke-%s-%d", p, soundUnitN.Add(1))}
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
			last := sFmtShort
			if p == ProfileVec {
				last = sVec32
			}
			for id := sCapRead; id <= last; id++ {
				if id == sCapMisuse {
					continue // rejected in the types group by construction
				}
				if rep.Shapes[fmt.Sprintf("S%02d", id)][0] == 0 {
					missing = append(missing, fmt.Sprintf("S%02d", id))
				}
			}
			if len(missing) > 0 {
				t.Errorf("new shapes never among the accepted programs: %v", missing)
			}
		})
	}
}

// langRejected are the forms of the capture flow restrictions and of the
// capture rule at a capturing call, in the generator's spelling (after the
// header), each rejected by its code alone, with the kinds a Rejected run
// reports: the result restriction when the captured local dies first
// (VDeadScope) and when it is written while the result lives
// (VRemovedTag), and a captured name against a mut receiver.
var langRejected = []struct {
	name, code string
	kinds      []string
	entry      string
}{
	{"result-dead", "E0609", []string{"VDeadScope"}, `Run(var k: i64): i64 {
    var h: i64 = 0
    var v1: i64 = 5
    var v2: i64 = 6
    var r9 := &v2
    k = k *% 6364136223846793005 +% 1442695040888963407
    if ((k >> 33) & 1) == 1 {
        var v3: i64 = 7
        r9 = Pass(&v1, fn(x: &i64): &i64 { return &v3 })
        h = h *% 31 +% v3
    }
    h = h *% 31 +% *r9
    h = h *% 31 +% v1
    h = h *% 31 +% k
    return h
}`},
	{"result-written", "E0609", []string{"VRemovedTag"}, `Run(k: i64): i64 {
    var h: i64 = 0
    var v1: i64 = 5
    var v3: i64 = 7
    var r9 := Pass(&v1, fn(x: &i64): &i64 { return &v3 })
    v3 = 8
    h = h *% 31 +% *r9
    h = h *% 31 +% v3
    h = h *% 31 +% v1
    h = h *% 31 +% k
    return h
}`},
}

// TestSoundLang_Rejected: each form is rejected by its code alone, and a
// Rejected run of it ends Skipped "poisoned", never in an ICE: the call
// passes an environment, which only mono's instance takes, and mono
// serves no rejected unit. The violation the restriction prevents is
// therefore shown with the restriction cut, by M-E0609-flow's compiler
// (TestSound_Mutants), whose accepted units mono serves.
func TestSoundLang_Rejected(t *testing.T) {
	s := soundSess(t)
	for _, c := range langRejected {
		t.Run(c.name, func(t *testing.T) {
			name := fmt.Sprintf("sound/langrej%d", soundUnitN.Add(1))
			out := s.CheckUnit(driver.UnitInput{Name: name, Files: []driver.UnitFile{{Path: "sound.mk", Text: soundHeader + "\n" + c.entry + "\n"}}})
			if got := unitCodes(out); len(got) != 1 || got[0] != c.code {
				t.Fatalf("codes %v, want [%s]:\n%s", got, c.code, out.Report.Text(out.Sources))
			}
			poisoned := 0
			for k := uint64(0); k < 8; k++ {
				r := SoundRunOn(s, SoundRunSpec{Unit: name, Fn: "Run", Args: []uint64{k}, Tag: &SoundTag{Seed: k, Rejected: true}})
				if r.Skipped == "poisoned" {
					poisoned++
				}
				// A seed whose branch skips the call runs clean.
				if r.ErrKind != "" || len(r.Violations) > 0 || r.Skipped != "" && r.Skipped != "poisoned" {
					t.Errorf("k %d: skipped %q, err %s %s, violations %v; want Skipped poisoned, or clean", k, r.Skipped, r.ErrKind, r.ErrText, r.Violations)
				}
			}
			if poisoned == 0 {
				t.Errorf("no seed of 0 to 7 reached the call")
			}
		})
	}
}

// vecRejected are vector loads and stores through a loan the rules end
// first: the tagged mode must see FromSlice's read and CopyTo's write.
var vecRejected = []struct {
	name, code string
	kinds      []string
	entry      string
}{
	{"fromslice-after-write", "E0502", []string{"VRemovedTag"}, `Run(k: i64): i64 {
    var h: i64 = 0
    var xs1: [4]i64 = [1, 2, 3, 4]
    var z2 := xs1[0..2]
    xs1[0] = 9
    var vc3 := simd.Vec[i64, 2].FromSlice(z2, 0)
    h = h *% 31 +% vc3.ReduceSumWrap()
    h = h *% 31 +% xs1[0]
    h = h *% 31 +% k
    return h
}`},
	{"copyto-after-read", "E0504", []string{"VRemovedTag"}, `Run(k: i64): i64 {
    var h: i64 = 0
    var xs1: [4]i64 = [1, 2, 3, 4]
    var z2 := mut xs1[0..2]
    h = h *% 31 +% xs1[1]
    var vc3 := simd.Vec[i64, 2].Splat(k)
    vc3.CopyTo(z2, 0)
    h = h *% 31 +% xs1[0]
    h = h *% 31 +% k
    return h
}`},
}

// TestSoundVec_TaggedAccess: each vector form is rejected by its code alone
// and its Rejected run reports VRemovedTag at the vector access.
func TestSoundVec_TaggedAccess(t *testing.T) {
	s := soundSess(t)
	for _, c := range vecRejected {
		t.Run(c.name, func(t *testing.T) {
			kinds := calibRejected(t, s, c.code, c.kinds, "import std.simd\n\n"+c.entry, 0)
			t.Logf("%s: %v", c.code, kinds)
		})
	}
}

// TestSoundGen_E0411Lever: a badFree helper of GenVersion 7 is E0411 alone,
// and its Rejected run frees the caller's arena twice on every seed (the
// always-failing failure point after bp.Free()), with FailAlloc 0.
func TestSoundGen_E0411Lever(t *testing.T) {
	s := soundSess(t)
	entry := `H1(a: &Arena, bp: Arena): OutOfMemory!i64 {
    var h: i64 = 0
    bp.Free()
    zq := try a.Slice[i64](2305843009213693952)
    h = h *% 31 +% i64(zq.Len())
    b := try a.Sub()
    onerr b.Free()
    t := try b.Sub()
    t.Free()
    b.Free()
    return h
}

Run(k: i64, a: &Arena): i64 {
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
}`
	name := fmt.Sprintf("sound/lever%d", soundUnitN.Add(1))
	out := s.CheckUnit(driver.UnitInput{Name: name, Files: []driver.UnitFile{{Path: "sound.mk", Text: soundHeader + "\n" + entry + "\n"}}})
	if got := unitCodes(out); len(got) != 1 || got[0] != "E0411" {
		t.Fatalf("codes %v, want [E0411]:\n%s", got, out.Report.Text(out.Sources))
	}
	for k := uint64(0); k < 8; k++ {
		r := SoundRunOn(s, SoundRunSpec{Unit: name, Fn: "Run", Args: []uint64{k}, Tag: &SoundTag{Seed: k, Rejected: true}})
		if r.ErrKind != "" || r.Skipped != "" {
			t.Fatalf("k %d: err %s %s, skipped %q", k, r.ErrKind, r.ErrText, r.Skipped)
		}
		if len(r.Violations) == 0 || r.Violations[0].Kind != "VDead" {
			t.Errorf("k %d: violations %v, want VDead (the second Free)", k, r.Violations)
		}
	}
}

// TestSoundGen_CapMisuseNoDefect: the eight vec seeds that were generator
// defects in a version-7 throughput run (S47's bound literal named f32,
// E0102; a slice local marked read by a form that reads none, E0208) are
// rejected by E0609 alone after the fix, which changed no other program's text
// (the goldens are unchanged).
func TestSoundGen_CapMisuseNoDefect(t *testing.T) {
	s := soundSess(t)
	for _, seed := range []uint64{0x7306634e90b1c943, 0xec92c724c2bf33b6, 0x8f6aae6611b2e41c, 0x99cda8910f0ef5a5,
		0x6858acae889b25cc, 0xd7afda43b3c115a9, 0x2017e3f07b3fd059, 0xa1c6a9418e6495e2} {
		p := GenerateSound(ProfileVec, seed)
		out := soundCheck(s, p.Text)
		if c, codes := classifySoundProg(p, out.Report); c != "rejected" || len(codes) != 1 || codes[0] != "E0609" {
			t.Errorf("%#x: %s %v, want rejected [E0609]:\n%s", seed, c, codes, out.Report.Text(out.Sources))
		}
	}
}
