// Why: the soundness test is only as good as its program generator, which
// must be deterministic, reach every shape and produce checkable programs.
// What: TestSoundGen_Deterministic, _Golden, _HeaderFile, _HeaderAccepted,
// _ShapesReachable, _NoDefects, _AcceptanceReported and _Twin, and
// BenchmarkSoundGenerate and BenchmarkSoundCheck.
// How: generates programs per profile and seed, pins goldens, and checks
// samples through one shared corpus session (soundSession).

package corpus

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"mk/internal/diag"
	"mk/internal/driver"
)

// soundSession is one corpus session for the soundness tests (std checked
// once). MK_STD selects another std directory, as for every session.
var soundSession = sync.OnceValues(func() (*driver.CorpusSession, error) {
	return driver.NewCorpusSession(driver.Config{})
})

var soundUnitN atomic.Int64

// soundCheck checks text as a one-file unit of session s under a fresh
// name; safe from any goroutine (soundSess, which may skip, is called on the
// test's own goroutine first).
func soundCheck(s *driver.CorpusSession, text string) driver.UnitOutcome {
	name := fmt.Sprintf("sound/test/%d", soundUnitN.Add(1))
	return s.CheckUnit(driver.UnitInput{Name: name, Files: []driver.UnitFile{{Path: "sound.mk", Text: text}}})
}

func firstStdDiag(s *driver.CorpusSession) string {
	r := s.Std()
	if len(r.Report.Diags) == 0 {
		return "no std diagnostic"
	}
	d := r.Report.Diags[0].Diag
	p := r.Sources.Position(d.At.StartPos())
	return fmt.Sprintf("%s:%d:%d: %s %s", p.Path, p.Line, p.Col, d.Code, d.Message)
}

func TestSoundGen_Deterministic(t *testing.T) {
	const n = 1000
	want := make([]string, n)
	for i := range n {
		want[i] = GenerateSound(ProfileFull, ProgramSeed(1, i)).Text
	}
	got := make([]string, n)
	var wg sync.WaitGroup
	for w := range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := n - 1 - w; i >= 0; i -= 4 {
				got[i] = GenerateSound(ProfileFull, ProgramSeed(1, i)).Text
			}
		}()
	}
	wg.Wait()
	for i := range n {
		if got[i] != want[i] {
			t.Fatalf("program %d differs between two generations", i)
		}
	}
	gold := uint64(0x9e3779b97f4a7c15)
	if RunSeeds(7, 3)[1] != splitmix64(7^(2*gold)) {
		t.Fatal("RunSeeds does not follow 2.3's formula")
	}
}

// TestSoundGen_Golden pins five seeds per profile
// (testdata/soundgen/<profile>.golden, rewritten only with -update). A
// change that changes one bumps GenVersion.
func TestSoundGen_Golden(t *testing.T) {
	for _, p := range []Profile{ProfileFull, ProfileCore, ProfileTasks, ProfileReject, ProfileOracle, ProfileLang, ProfileVec, ProfileViews, ProfileReject7, ProfileFields, ProfileRejectF} {
		var b strings.Builder
		fmt.Fprintf(&b, "GenVersion %d\n", GenVersion)
		for i := range 5 {
			prog := GenerateSound(p, ProgramSeed(0x4d6b536f756e6431, i))
			fmt.Fprintf(&b, "==== %s seed %#016x params %v shapes %v lines %d\n%s", p, prog.Seed, prog.Params, prog.Shapes, prog.Lines, prog.Text)
		}
		path := filepath.Join("testdata", "soundgen", p.String()+".golden")
		if *update {
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		want, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%v (run with -update to write it)", err)
		}
		if string(want) != b.String() {
			t.Errorf("%s: the generated text differs from %s: bump GenVersion and rewrite with -update", p, path)
		}
	}
}

// TestSoundGen_HeaderFile keeps tests/soundness/header.mk equal to the
// generator's copy (soundHeader); -update rewrites the file.
func TestSoundGen_HeaderFile(t *testing.T) {
	path := filepath.Join(repoRoot(t), "tests", "soundness", "header.mk")
	if *update {
		if err := os.WriteFile(path, []byte(soundHeader), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.ReplaceAll(string(got), "\r\n", "\n") != soundHeader {
		t.Fatal("tests/soundness/header.mk differs from soundshapes.go's soundHeader (go test -run TestSoundGen_HeaderFile -update rewrites the file)")
	}
	if len(headerItems()) != nItems {
		t.Fatalf("%d header items, want %d", len(headerItems()), nItems)
	}
}

// TestSoundGen_HeaderAccepted checks the header alone: no diagnostic.
func TestSoundGen_HeaderAccepted(t *testing.T) {
	out := soundCheck(soundSess(t), soundHeader)
	if len(out.Report.Diags) > 0 {
		t.Fatalf("header.mk is not accepted:\n%s", out.Report.Text(out.Sources))
	}
}

// soundImplemented reports whether a shape is written by this generator
// version: every shape.
func soundImplemented(id int) bool { return id >= 1 && id <= nShapes }

// TestSoundGen_ShapesReachable: each shape of a profile appears within
// 2,000 of its programs, 6,000 for m7 and fields.
func TestSoundGen_ShapesReachable(t *testing.T) {
	for _, p := range []Profile{ProfileFull, ProfileCore, ProfileOracle, ProfileLang, ProfileVec, ProfileViews, ProfileFields} {
		var seen [nShapes + 1]int
		n := 2000
		if p == ProfileViews || p == ProfileFields {
			n = 6000 // the m7 shapes dilute the rare ones (S38)
		}
		for i := range n {
			for _, id := range GenerateSound(p, ProgramSeed(5, i)).Shapes {
				seen[id]++
			}
		}
		for id := 1; id <= nShapes; id++ {
			// S63 and S64 are drawn inside ref and ival, never as statements.
			want := (shapeWeight(id, p) > 0 || (id == sArmRef || id == sArmVal) && viewsProfile(p)) && soundImplemented(id)
			if want && seen[id] == 0 {
				t.Errorf("%s: S%02d never appears in %d programs", p, id, n)
			}
			if !want && seen[id] > 0 {
				t.Errorf("%s: S%02d appears %d times, but the profile excludes it", p, id, seen[id])
			}
		}
	}
}

// soundClassify sorts a checked program: "ice", "defect",
// "rejected" or "accepted".
func soundClassifyTest(r diag.Report) string {
	c, _ := classifySound(r)
	return c
}

// TestSoundGen_NoDefects: 300 programs of each profile, no diagnostic
// outside phase flow and none the generator forbids.
func TestSoundGen_NoDefects(t *testing.T) {
	for _, p := range []Profile{ProfileCore, ProfileFull, ProfileOracle, ProfileLang, ProfileVec, ProfileViews, ProfileFields} {
		t.Run(p.String(), func(t *testing.T) {
			t.Parallel()
			s := soundSess(t)
			defects := 0
			var mu sync.Mutex
			var wg sync.WaitGroup
			sem := make(chan struct{}, 4)
			for i := range 300 {
				wg.Add(1)
				sem <- struct{}{}
				go func() {
					defer wg.Done()
					defer func() { <-sem }()
					prog := GenerateSound(p, ProgramSeed(11, i))
					out := soundCheck(s, prog.Text)
					if c, _ := classifySoundProg(prog, out.Report); c == "defect" || c == "ice" {
						mu.Lock()
						defer mu.Unlock()
						defects++
						if defects <= 3 {
							t.Errorf("%s program %d (seed %#x) is a generator %s:\n%s\n%s", p, i, prog.Seed, c, out.Report.Text(out.Sources), numbered(prog.Text))
						}
					}
				}()
			}
			wg.Wait()
			if defects > 0 {
				t.Errorf("%s: %d defects in 300 programs", p, defects)
			}
		})
	}
}

func numbered(s string) string {
	var b strings.Builder
	for i, l := range strings.Split(strings.TrimRight(s, "\n"), "\n") {
		fmt.Fprintf(&b, "%4d  %s\n", i+1, l)
	}
	return b.String()
}

// TestSoundGen_AcceptanceReported logs the acceptance rate of 1,000 full
// programs with the per-code table; below 20 percent fails.
func TestSoundGen_AcceptanceReported(t *testing.T) {
	s := soundSess(t)
	for _, p := range []Profile{ProfileFull, ProfileCore} {
		var accepted, rejected, other atomic.Int64
		var mu sync.Mutex
		codes := map[string]int{}
		var wg sync.WaitGroup
		sem := make(chan struct{}, 8)
		const n = 1000
		for i := range n {
			wg.Add(1)
			sem <- struct{}{}
			go func() {
				defer wg.Done()
				defer func() { <-sem }()
				prog := GenerateSound(p, ProgramSeed(13, i))
				out := soundCheck(s, prog.Text)
				c, cs := classifySoundProg(prog, out.Report)
				switch c {
				case "accepted":
					accepted.Add(1)
				case "rejected":
					rejected.Add(1)
					mu.Lock()
					for _, x := range cs {
						codes[x]++
					}
					mu.Unlock()
				default:
					other.Add(1)
				}
			}()
		}
		wg.Wait()
		keys := make([]string, 0, len(codes))
		for k := range codes {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var tab []string
		for _, k := range keys {
			tab = append(tab, fmt.Sprintf("%s %d", k, codes[k]))
		}
		rate := float64(accepted.Load()) / n
		t.Logf("%s: %d programs: accepted %d (%.1f%%), rejected %d, defects %d; rejected by code: %s", p, n, accepted.Load(), 100*rate, rejected.Load(), other.Load(), strings.Join(tab, ", "))
		if rate < 0.20 {
			t.Errorf("%s: acceptance %.1f%% is below 20 percent: a generator defect (4.2.10)", p, 100*rate)
		}
	}
}

func BenchmarkSoundGenerate(b *testing.B) {
	b.ReportAllocs()
	for i := range b.N {
		GenerateSound(ProfileFull, ProgramSeed(17, i))
	}
}

func BenchmarkSoundCheck(b *testing.B) {
	progs := make([]string, 256)
	for i := range progs {
		progs[i] = GenerateSound(ProfileFull, ProgramSeed(19, i)).Text
	}
	s := soundSess(b)
	soundCheck(s, progs[0])
	b.ResetTimer()
	for i := range b.N {
		soundCheck(s, progs[i%len(progs)])
	}
}

var _ = slices.Sort[[]string]

// TestSoundGen_Twin: a program's clause-restored twin (GenerateSoundTwin)
// drops no clause and differs from the program only on
// its helpers' signature lines; a program that dropped none is its own
// twin, byte for byte.
func TestSoundGen_Twin(t *testing.T) {
	dropped := 0
	for i := range 2000 {
		seed := ProgramSeed(17, i)
		p, w := GenerateSound(ProfileReject, seed), GenerateSoundTwin(ProfileReject, seed)
		if w.Dropped != 0 || !w.Twin {
			t.Fatalf("seed %#x: twin drops %d clauses (Twin %v)", seed, w.Dropped, w.Twin)
		}
		if p.Dropped == 0 {
			if p.Text != w.Text {
				t.Fatalf("seed %#x drops no clause, but its twin's text differs", seed)
			}
			continue
		}
		dropped++
		a, b := strings.Split(p.Text, "\n"), strings.Split(w.Text, "\n")
		if len(a) != len(b) {
			t.Fatalf("seed %#x: %d lines, twin %d", seed, len(a), len(b))
		}
		diff := 0
		for j := range a {
			if a[j] == b[j] {
				continue
			}
			diff++
			if !strings.HasPrefix(a[j], "H") || !strings.HasSuffix(a[j], "{") {
				t.Fatalf("seed %#x line %d differs outside a helper's signature:\n%s\n%s", seed, j+1, a[j], b[j])
			}
		}
		if diff == 0 {
			t.Fatalf("seed %#x drops %d clauses, but its twin is the same text", seed, p.Dropped)
		}
	}
	if dropped == 0 {
		t.Fatal("no program of 2,000 drops a clause")
	}
	t.Logf("%d of 2,000 programs drop a clause", dropped)
}
