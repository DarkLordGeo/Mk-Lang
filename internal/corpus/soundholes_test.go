// Why: the hole shapes are the generator's way to put a known-unsound
// function in a program, and their rejections must stay where intended.
// What: TestSound_HoleRejections over the hole strata S82 to S88 of
// soundholes.go, with holeFnOf and topFuncs.
// How: checks generated programs of profile fields and maps each
// diagnostic to the function it falls in.
//
// The hole strata S82 to S88: where the tree rejects them. A hole function
// must be rejected by its own rule inside its own body, so its row's mutant
// is the only thing between the program and a dead read; a benign one must
// carry no diagnostic, so its half is accepted wherever the rest of the
// program is. Either property could be lost silently by a checker or
// generator change, and only a teeth run (TestSound_Mutants) would show it.

package corpus

import (
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"

	"mk/internal/driver"
)

// holeFn is one generated function of a hole shape.
type holeFn struct {
	shape   int
	variant string
	hole    bool
	code    string // the code the tree gives a hole inside it
}

var gxClause = regexp.MustCompile(`from \(a: (\w), b: (\w)\) \{\n    return Fp\(a: (\w), b: (\w)\)`)

// holeFnOf classifies the top-level function name with body text; ok is
// false for a function of no hole shape.
func holeFnOf(name, body string) (f holeFn, ok bool) {
	pre := strings.TrimRight(name, "0123456789")
	if pre == name {
		return f, false
	}
	switch pre {
	case "Cl", "Cn", "Cf":
		f = holeFn{shape: map[string]int{"Cl": 82, "Cn": 83, "Cf": 84}[pre], variant: pre, hole: strings.Contains(body, ", &z)\n"), code: "E0505"}
	case "Sp":
		f = holeFn{shape: 85, variant: pre, hole: strings.Contains(body, "*l.s.a = v"), code: "E0604"}
	case "So", "Si":
		f = holeFn{shape: 85, variant: pre}
	case "Gx":
		m := gxClause.FindStringSubmatch(body)
		if m == nil {
			return f, false
		}
		f = holeFn{shape: 86, variant: pre, hole: m[1] != m[3], code: "E0602"}
	case "Gc":
		f = holeFn{shape: 86, variant: pre}
	case "Kf":
		v := "direct"
		switch {
		case strings.Contains(body, "KkSlot("):
			v = "carrier"
		case strings.Contains(body, "KkMid("):
			v = "mid"
		case strings.Contains(body, "KkStar("):
			v = "star"
		case strings.Contains(body, "if *r != *r {"):
			v = "join"
		case strings.Contains(body, "KkName(") && strings.Index(body, "KkName(") < strings.Index(body, "KkCell("):
			v = "deepadd"
		case strings.Contains(body, "KkName("):
			v = "shallowadd"
		}
		f = holeFn{shape: 87, variant: "Kf " + v, hole: strings.Contains(body, "KkPoke(mut k, &y)"), code: "E0505"}
	case "Gs":
		v := "Gm"
		switch {
		case strings.Contains(body, "f: fn(): &Mutex"):
			v = "lit"
		case strings.Contains(body, "f: fn(x: &i64)"):
			v = "Gy"
		case strings.Contains(body, "(&lm)"):
			v = "Gb"
		}
		f = holeFn{shape: 88, variant: "Gs " + v, hole: v != "Gb" && strings.Contains(body, "gd.Store(&n)"), code: "E0605"}
	case "Gb":
		f = holeFn{shape: 88, variant: pre, hole: strings.Contains(body, "return &GM"), code: "E0602"}
	case "Gm", "Gy", "Gr":
		f = holeFn{shape: 88, variant: pre}
	default:
		return f, false
	}
	return f, true
}

// topFuncs is each top-level function of text: its name and its first
// and last lines (1-based).
func topFuncs(text string) map[string][2]int {
	spans := map[string][2]int{}
	cur, start := "", 0
	for i, l := range strings.Split(text, "\n") {
		switch {
		case l == "" || l[0] == ' ' || l[0] == '/':
		case l[0] == '}':
			if cur != "" {
				spans[cur] = [2]int{start, i + 1}
				cur = ""
			}
		case strings.HasSuffix(l, "{") && !strings.HasPrefix(l, "struct ") && !strings.HasPrefix(l, "export "):
			if j := strings.IndexByte(l, '('); j > 0 {
				cur, start = l[:j], i+1
			}
		}
	}
	return spans
}

// TestSound_HoleRejections: among MK_SOUND_HOLE_N (default 3,000)
// programs of profile fields (the default base), every hole function of
// S82 to S88 is rejected with exactly its code inside it (each of its
// diagnostics has that code), no benign one has a diagnostic inside it,
// every (shape, variant, hole) form occurs, and each of S82 to S88 is in
// an accepted program.
func TestSound_HoleRejections(t *testing.T) {
	s := soundSess(t)
	n := soundInt(t, "MK_SOUND_HOLE_N", 3000)
	type key struct {
		variant string
		hole    bool
	}
	seen := map[key]int{}
	acc := map[int]int{}
	var bad []string
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 4)
	for i := range n {
		p := GenerateSound(ProfileFields, ProgramSeed(DefaultSoundBase, i))
		if !slices.ContainsFunc(p.Shapes, func(id uint16) bool { return id >= sC3List }) {
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			name := fmt.Sprintf("sound/holes%d", soundUnitN.Add(1))
			defer s.Forget([]string{name})
			out := s.CheckUnit(driver.UnitInput{Name: name, Files: []driver.UnitFile{{Path: "sound.mk", Text: p.Text}}})
			class, _ := classifySoundProg(p, out.Report)
			lines := strings.Split(p.Text, "\n")
			type diagAt struct {
				line int
				code string
			}
			var ds []diagAt
			for _, e := range out.Report.Diags {
				pos := out.Sources.Position(e.Diag.At.StartPos())
				if filepath.Base(pos.Path) == "sound.mk" {
					ds = append(ds, diagAt{int(pos.Line), e.Diag.Code.String()})
				}
			}
			mu.Lock()
			defer mu.Unlock()
			if class == "accepted" {
				for _, id := range p.Shapes {
					acc[int(id)]++
				}
			} else if class != "rejected" {
				bad = append(bad, fmt.Sprintf("program %d: %s", i, class))
				return
			}
			for fn, sp := range topFuncs(p.Text) {
				f, ok := holeFnOf(fn, strings.Join(lines[sp[0]-1:sp[1]], "\n"))
				if !ok {
					continue
				}
				seen[key{fmt.Sprintf("S%d %s", f.shape, f.variant), f.hole}]++
				var in []string
				for _, d := range ds {
					if d.line >= sp[0] && d.line <= sp[1] {
						in = append(in, d.code)
					}
				}
				switch {
				case f.hole && (len(in) == 0 || slices.ContainsFunc(in, func(c string) bool { return c != f.code })):
					bad = append(bad, fmt.Sprintf("program %d: hole function %s (S%d) has not exactly %s inside it (codes inside: %v)", i, fn, f.shape, f.code, in))
				case !f.hole && len(in) > 0:
					bad = append(bad, fmt.Sprintf("program %d: benign function %s (S%d) has diagnostics %v inside it", i, fn, f.shape, in))
				}
			}
		}()
	}
	wg.Wait()
	for _, b := range bad[:min(len(bad), 20)] {
		t.Error(b)
	}
	if len(bad) > 20 {
		t.Errorf("... and %d more", len(bad)-20)
	}
	want := []key{
		{"S82 Cl", true}, {"S82 Cl", false}, {"S83 Cn", true}, {"S83 Cn", false}, {"S84 Cf", true}, {"S84 Cf", false},
		{"S85 Sp", true}, {"S85 Sp", false}, {"S85 So", false}, {"S85 Si", false},
		{"S86 Gx", true}, {"S86 Gx", false}, {"S86 Gc", false},
		{"S88 Gs Gm", true}, {"S88 Gs Gm", false}, {"S88 Gs lit", true}, {"S88 Gs lit", false},
		{"S88 Gs Gy", true}, {"S88 Gs Gy", false}, {"S88 Gs Gb", false}, {"S88 Gb", true}, {"S88 Gb", false},
		{"S88 Gm", false}, {"S88 Gy", false}, {"S88 Gr", false},
	}
	for _, v := range []string{"direct", "carrier", "deepadd", "join", "mid", "star", "shallowadd"} {
		want = append(want, key{"S87 Kf " + v, true}, key{"S87 Kf " + v, false})
	}
	for _, k := range want {
		t.Logf("%-18s hole=%-5v %5d functions", k.variant, k.hole, seen[k])
		if seen[k] == 0 {
			t.Errorf("%s hole=%v: no function of that form in %d programs", k.variant, k.hole, n)
		}
	}
	if len(seen) != len(want) {
		t.Errorf("%d forms seen, %d expected: %v", len(seen), len(want), seen)
	}
	for id := sC3List; id <= sGlobalCall; id++ {
		if acc[id] == 0 {
			t.Errorf("S%02d is in no accepted program of %d", id, n)
		}
	}
}
