// Why: the soundness runner's reports and its found and miscompile records
// are what a finding is filed from, so their format and reproduction must hold.
// What: TestSound_RunReportDeterministic, TestSound_RunRecordSchema,
// TestSound_Smoke and the record tests (TestSound_RecordFormat,
// TestSound_FoundOpen, TestSound_FoundClosed, TestOracle_Records).
// How: a deterministic fake compiler (fakeSound) for the runner, and the
// real session for every record under tests/soundness.

package corpus

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"mk/internal/driver"
)

// fakeSound is a SoundCompiler over the shared session's CheckUnit whose
// runs are deterministic: a run reports a VDead violation when the text
// holds plantViolation, else the value is a hash of the unit and k.
type fakeSound struct {
	s        *driver.CorpusSession
	recycles atomic.Int64
	texts    map[string]string
	mu       chan struct{}
}

const plantViolation = "h = h *% 31 +% 42424242"

func newFakeSound(s *driver.CorpusSession) *fakeSound {
	f := &fakeSound{s: s, texts: map[string]string{}, mu: make(chan struct{}, 1)}
	return f
}

func (f *fakeSound) Start() error { return nil }
func (f *fakeSound) Check(unit, text string) driver.UnitOutcome {
	f.mu <- struct{}{}
	f.texts[unit] = text
	<-f.mu
	return f.s.CheckUnit(driver.UnitInput{Name: unit, Files: []driver.UnitFile{{Path: "sound.mk", Text: text}}})
}
func (f *fakeSound) RunUnit(spec SoundRunSpec) SoundRunResult {
	f.mu <- struct{}{}
	text := f.texts[spec.Unit]
	<-f.mu
	if strings.Contains(text, plantViolation) {
		return SoundRunResult{Violations: []SoundViolation{{Kind: "VDead", Text: "VDead at 1:1 (planted)"}}}
	}
	return SoundRunResult{Value: splitmix64(spec.Args[0] ^ uint64(len(text)))}
}
func (f *fakeSound) Forget([]string) {}
func (f *fakeSound) Recycle() error  { f.recycles.Add(1); return nil }

// timingFree clears a record's timing keys and its label (two runs are
// equal except the timing keys).
func timingFree(r *SoundReport) SoundReport {
	c := *r
	c.Label, c.WallSeconds, c.Throughput, c.Workers, c.Recycles = "", 0, Throughput{}, 0, 0
	return c
}

func TestSound_RunReportDeterministic(t *testing.T) {
	s := soundSess(t)
	var reps []*SoundReport
	for _, w := range []int{1, 8} {
		cfg := SoundConfig{Profile: ProfileCore, Base: 77, Generated: 200, Seeds: 2, Workers: w, Recycle: 64, Label: fmt.Sprintf("det-%d-%d", w, soundUnitN.Add(1))}
		rep, err := RunSound(context.Background(), cfg, newFakeSound(s))
		if err != nil {
			t.Fatal(err)
		}
		if rep.Generated != 200 || rep.Recycles < 3 {
			t.Fatalf("workers %d: generated %d, recycles %d (want 200 and at least 3 at 64 a session)", w, rep.Generated, rep.Recycles)
		}
		reps = append(reps, rep)
	}
	a, b := timingFree(reps[0]), timingFree(reps[1])
	if !reflect.DeepEqual(a, b) {
		ja, _ := json.Marshal(a)
		jb, _ := json.Marshal(b)
		t.Fatalf("records differ between 1 and 8 workers:\n%s\n%s", ja, jb)
	}
	t.Log(reps[1].Summary())
	// Accepted bounds the run at the same program whatever the workers.
	var cut []int
	for _, w := range []int{1, 8} {
		cfg := SoundConfig{Profile: ProfileCore, Base: 78, Accepted: 25, Seeds: 1, Workers: w, Label: fmt.Sprintf("cut-%d-%d", w, soundUnitN.Add(1))}
		rep, err := RunSound(context.Background(), cfg, newFakeSound(s))
		if err != nil {
			t.Fatal(err)
		}
		if rep.Accepted != 25 {
			t.Fatalf("workers %d: %d accepted, want 25", w, rep.Accepted)
		}
		cut = append(cut, rep.Generated)
	}
	if cut[0] != cut[1] {
		t.Fatalf("Accepted 25 cuts at %d programs with 1 worker and %d with 8", cut[0], cut[1])
	}
}

func TestSound_RunRecordSchema(t *testing.T) {
	rep := soundReport(SoundConfig{Profile: ProfileFull, Base: DefaultSoundBase, Seeds: 4, Label: "schema"}, nil, "accepted", 0, 0)
	b, err := json.Marshal(rep)
	if err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	if _, err := dec.Token(); err != nil {
		t.Fatal(err)
	}
	var keys []string
	for dec.More() {
		k, err := dec.Token()
		if err != nil {
			t.Fatal(err)
		}
		keys = append(keys, k.(string))
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			t.Fatal(err)
		}
	}
	if !reflect.DeepEqual(keys, SoundRecordKeys) {
		t.Fatalf("record keys %v, want %v", keys, SoundRecordKeys)
	}
	if !strings.HasPrefix(rep.Summary(), "sound: full base 0x4d6b536f756e6431 first 0: generated 0 accepted 0") {
		t.Fatalf("summary line %q", rep.Summary())
	}
}

// TestSound_Smoke: profile full, the default base, 300 programs,
// 2 seeds, minimiser off: zero defects, at least one accepted and one
// rejected program, and at least 25 of the 34 shapes among the accepted
// ones.
func TestSound_Smoke(t *testing.T) {
	s := soundSess(t)
	t.Run("check", func(t *testing.T) {
		cfg := SoundConfig{Profile: ProfileFull, Base: DefaultSoundBase, Generated: 300, Seeds: 2, CheckOnly: true, Label: fmt.Sprintf("smoke-%d", soundUnitN.Add(1))}
		rep, err := RunSound(context.Background(), cfg, newFakeSound(s))
		if err != nil {
			t.Fatal(err)
		}
		t.Log(rep.Summary())
		t.Logf("rejected by code: %s", codeTable(rep.RejectedByCode))
		if n := rep.DefectTotal() + rep.ICE; n > 0 {
			t.Errorf("%d defects or crashes: %+v", n, rep.Defects)
		}
		if rep.Accepted == 0 || rep.RejectedFlow == 0 {
			t.Errorf("accepted %d, rejected %d: want at least one of each", rep.Accepted, rep.RejectedFlow)
		}
		shapes := 0
		var missing []string
		for id := 1; id <= nShapes; id++ {
			if rep.Shapes[fmt.Sprintf("S%02d", id)][0] > 0 {
				shapes++
			} else {
				missing = append(missing, fmt.Sprintf("S%02d", id))
			}
		}
		if shapes < 25 {
			t.Errorf("%d of %d shapes among the accepted programs, want at least 25; missing %v", shapes, nShapes, missing)
		}
		t.Logf("%d of %d shapes among the accepted programs; missing %v", shapes, nShapes, missing)
	})
	t.Run("run", func(t *testing.T) {
		cfg := SoundConfig{Profile: ProfileFull, Base: DefaultSoundBase, Generated: 300, Seeds: 2, Label: fmt.Sprintf("smoke-%d", soundUnitN.Add(1))}
		rep, err := RunSound(context.Background(), cfg, NewSessionCompiler(driver.Config{}, s))
		if err != nil {
			t.Fatal(err)
		}
		t.Log(rep.Summary())
		if n := rep.DefectTotal() + rep.ICE; n > 0 {
			t.Errorf("%d defects or crashes: defects %+v, fuel %d, panics %d, skipped %v", n, rep.Defects, rep.Fuel, rep.Panics, rep.Skipped)
		}
		if len(rep.Violations) > 0 {
			for _, v := range rep.Violations {
				t.Errorf("violation: program %d (seed %s, run %s): %s", v.Program, v.Seed, v.Run, v.Text)
			}
		}
		if rep.Runs != 2*rep.Accepted {
			t.Errorf("%d runs for %d accepted programs, want 2 each", rep.Runs, rep.Accepted)
		}
	})
}

// soundRoot is tests/soundness.
func soundRoot(t testing.TB) string { return filepath.Join(repoRoot(t), "tests", "soundness") }

// recordFiles lists the .mk files under tests/soundness/<kind>/<state>/.
func recordFiles(t testing.TB, kind, state string) []string {
	t.Helper()
	ents, err := os.ReadDir(filepath.Join(soundRoot(t), kind, state))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range ents {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".mk") {
			out = append(out, filepath.Join(soundRoot(t), kind, state, e.Name()))
		}
	}
	return out
}

// TestSound_RecordFormat: every record under found/ and miscompile/ parses
// and its name is its id; and the encodings round-trip.
func TestSound_RecordFormat(t *testing.T) {
	for _, st := range []string{"open", "closed"} {
		for _, p := range recordFiles(t, "found", st) {
			b, err := os.ReadFile(p)
			if err == nil {
				_, err = ReadFound(p, b)
			}
			if err != nil {
				t.Error(err)
			}
		}
		for _, p := range recordFiles(t, "miscompile", st) {
			b, err := os.ReadFile(p)
			if err == nil {
				_, err = ReadMiscompile(p, b)
			}
			if err != nil {
				t.Error(err)
			}
		}
	}
	dir := t.TempDir()
	f := &FoundRecord{Sound: "VRemovedTag at 14:5 (local x of Run); tag made at 9:10; removed at 12:5", Gen: "1 full seed 0x5f1c9a0e7d3b2a41 run 0x0c4d1e2f3a4b5c6d",
		Found: "2026-11-07, run x, build 00", Minimised: "87 lines to 14, 412 candidates", Text: "export Run(k: i64): i64 {\n    return k\n}\n"}
	p, err := WriteFound(dir, f)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	g, err := ReadFound(p, b)
	if err != nil {
		t.Fatal(err)
	}
	if g.ID != f.ID || g.Kind != "VRemovedTag" || g.Seed != 0x5f1c9a0e7d3b2a41 || g.Run != 0x0c4d1e2f3a4b5c6d || g.Needs != "unknown" {
		t.Fatalf("round trip: %+v", g)
	}
	// Closing (a new verdict, markers, a closed line) keeps the id.
	closed := strings.Replace(string(b), "// verdict: ok", "// verdict: reject E0505", 1)
	closed = strings.Replace(closed, "    return k\n", "    return k // <- E0505\n", 1) + ""
	closed = strings.Replace(closed, "// minimised:", "// closed: fixed in the checker\n// minimised:", 1)
	if RecordID(closed) != f.ID {
		t.Fatalf("closing changed the id: %s, was %s", RecordID(closed), f.ID)
	}
	m := &MiscompileRecord{Oracle: "release differs; eval = dev", Owner: "perf-codegen", Target: "x86_64-windows", Seeds: "k 0x1 eval 0x2 dev 0x2 release 0x3",
		Gen: "1 oracle seed 0x5", Found: "2026-11-07", Minimised: "64 lines to 9, 120 candidates", Text: "export Run(k: i64): i64 {\n    return k\n}\n"}
	mp, err := WriteMiscompile(dir, m)
	if err != nil {
		t.Fatal(err)
	}
	mb, _ := os.ReadFile(mp)
	if g, err := ReadMiscompile(mp, mb); err != nil || g.Owner != "perf-codegen" || g.ID != m.ID {
		t.Fatalf("miscompile round trip: %+v %v", g, err)
	}
}

// TestSound_FoundOpen: every open record still reproduces: a
// crash record still crashes the check, and a violation record is still
// accepted and still gives its kind on its run seed or one of its program
// seed's run seeds. A record that stops reproducing fails here, so it is
// closed by the rule that fixed it.
func TestSound_FoundOpen(t *testing.T) {
	open := recordFiles(t, "found", "open")
	if len(open) == 0 {
		return
	}
	s := soundSess(t)
	for _, p := range open {
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		rec, err := ReadFound(p, data)
		if err != nil {
			t.Fatal(err)
		}
		unit := fmt.Sprintf("sound/open/%d", soundUnitN.Add(1))
		out := s.CheckUnit(driver.UnitInput{Name: unit, Files: []driver.UnitFile{{Path: "sound.mk", Text: rec.Text}}})
		class, _ := classifySound(out.Report)
		if rec.Crash {
			if class != "ice" {
				t.Errorf("%s: the check no longer crashes (%s): close the record", filepath.Base(p), class)
			}
			continue
		}
		if class != "accepted" {
			t.Errorf("%s: the program is now %s: close the record by the rule that rejects it", filepath.Base(p), class)
			continue
		}
		found := false
		for _, k := range append([]uint64{rec.Run}, RunSeeds(rec.Seed, 4)...) {
			r := SoundRunOn(s, SoundRunSpec{Unit: unit, Fn: "Run", Args: []uint64{k}, Tag: &SoundTag{Seed: k, FailAlloc: soundFailAlloc(k)}})
			if len(r.Violations) > 0 && r.Violations[0].Kind == rec.Kind {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("%s: no %s on any of its seeds any more: close the record", filepath.Base(p), rec.Kind)
		}
	}
}

// TestSound_FoundClosed: every closed record is judged as a corpus unit by
// its verdict: exactly its codes on exactly its marked lines.
func TestSound_FoundClosed(t *testing.T) {
	closed := recordFiles(t, "found", "closed")
	if len(closed) == 0 {
		return
	}
	s := soundSess(t)
	for _, p := range closed {
		text, err := readText(p)
		if err != nil {
			t.Fatal(err)
		}
		u, err := makeUnit("tests/soundness/found/closed/"+filepath.Base(p), "soundness", false, []UnitFile{{Path: filepath.Base(p), Text: text}})
		if err != nil {
			t.Fatal(err)
		}
		u.Name = fmt.Sprintf("%s#%d", u.Name, soundUnitN.Add(1))
		r := s.CheckUnit(ToUnitInput(&u, "", driver.UnitCheck))
		if j := JudgeUnit(&u, r, s.StdState(), driver.Available()); j.Status != StatusPass {
			t.Errorf("%s: %s", p, j.Reason)
		}
	}
}

// TestOracle_Records: every open miscompile record still differs, every
// closed one agrees. None is recorded yet.
func TestOracle_Records(t *testing.T) {
	n := len(recordFiles(t, "miscompile", "open")) + len(recordFiles(t, "miscompile", "closed"))
	if n == 0 {
		return
	}
	t.Skipf("%d miscompile records: the rerun is not written yet", n)
}
