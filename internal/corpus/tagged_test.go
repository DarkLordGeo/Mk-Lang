// Why: field-level loan sets make the checker accept programs that
// whole-value loans reject, and each such program must be shown sound by
// running it.
// What: TestTagged (tagged runs of a directory of programs) and
// TestFlatTwin (per-field result clauses against their flattened twins).
// How: both read their inputs from environment variables and skip without
// them; the runs use the tagged evaluator with calibRuns' tags.
//
// TestTagged judges each .mk file of the directory MK_C3_TAGGED names as
// TestFlowsWitnesses judges a witness (one-file unit, the test prelude, a
// doc block's module), then runs every top-level function the tagged
// evaluator can call (integer, Io and &Arena parameters) with calibRuns'
// six tags: clean ones when the file is accepted, with Rejected when it is
// not (the calibration side: an unsound rejected program should show its
// dead read). It prints one C3-TAGGED line per file and fails on a
// violation in an accepted file.
//
// A rejected file's runs are bounded: each run takes MK_C3_REJECTED_FUEL
// steps (c3RejectedFuel), and the file MK_C3_REJECTED_SECONDS of wall time
// (c3RejectedSeconds), checked between runs. A rejected program may loop
// forever, and the tagged evaluator's cost per step grows with what the
// loop allocates, so one run at the default fuel took minutes. A file that
// reaches either bound is reported as not run, on its line and in the
// closing count.

package corpus

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"mk/internal/driver"
)

// c3Marker is a body line's expected-diagnostic marker, dropped with the
// header: the file is judged for its acceptance only.
var c3Marker = regexp.MustCompile(`[ \t]*// <- E[0-9]{4}[^\n]*`)

// The bounds of a rejected file's runs, and their switches.
const (
	c3RejectedFuel    = 200_000
	c3RejectedSeconds = 30
)

func TestTagged(t *testing.T) {
	dir := os.Getenv("MK_C3_TAGGED")
	if dir == "" {
		t.Skip("MK_C3_TAGGED not set")
	}
	rejFuel := uint64(soundInt(t, "MK_C3_REJECTED_FUEL", c3RejectedFuel))
	rejWall := time.Duration(soundInt(t, "MK_C3_REJECTED_SECONDS", c3RejectedSeconds)) * time.Second
	var notRun []string
	files, err := filepath.Glob(filepath.Join(dir, "*.mk"))
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(files)
	s := soundSess(t)
	pre, err := Prelude(repoRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range files {
		text, err := readText(p)
		if err != nil {
			t.Fatal(err)
		}
		name := filepath.Base(p)
		// Judged for its acceptance only: a verdict line of its own goes
		// first (a twin or a model program may carry none, or another).
		for strings.HasPrefix(text, "//") {
			_, text, _ = strings.Cut(text, "\n")
		}
		text = "// verdict: ok\n" + c3Marker.ReplaceAllString(text, "")
		u, err := makeUnit("c3/"+name, "soundness", true, []UnitFile{{Path: name, Text: text}})
		if err != nil {
			t.Fatal(err)
		}
		u.Name = fmt.Sprintf("%s#%d", u.Name, soundUnitN.Add(1))
		in := ToUnitInput(&u, pre, driver.UnitCheck)
		in.Design = true
		r := s.CheckUnit(in)
		accepted := len(r.Report.Diags) == 0
		var codes []string
		for _, d := range r.Report.Diags {
			if c := d.Diag.Code.String(); !slices.Contains(codes, c) {
				codes = append(codes, c)
			}
		}
		var fns, runs, viol, skipped, errs int
		first, bound := "", ""
		var fuel uint64
		start := time.Now()
		if !accepted {
			fuel = rejFuel
		}
		seen := map[string]bool{}
		for _, m := range topFns.FindAllStringSubmatch(text, -1) {
			fn := m[1]
			if seen[fn] || fn == "Cond" || fn == "Use" || fn == "UseMut" {
				continue
			}
			seen[fn] = true
			ran := false
			for _, tag := range calibRuns(!accepted) {
				if !accepted && bound == "" && time.Since(start) > rejWall {
					bound = fmt.Sprintf("over %s of wall time", rejWall)
				}
				if bound != "" {
					break
				}
				res, ok := runEntry(s, u.Name, fn, tag, fuel)
				if !ok {
					break
				}
				ran = true
				runs++
				if !accepted && res.ErrKind == "fuel" && bound == "" {
					bound = fmt.Sprintf("%s ran out of its %d steps", fn, fuel)
				}
				switch {
				case len(res.Violations) > 0:
					viol++
					if first == "" {
						first = fmt.Sprintf("%s (Cond %d, seed %d): %s %s", fn, tag.Cond, tag.Seed, res.Violations[0].Kind, res.Violations[0].Text)
					}
				case res.Skipped != "":
					skipped++
				case res.ErrKind != "":
					errs++
				}
			}
			if ran {
				fns++
			}
		}
		verdict := "accepted"
		if !accepted {
			verdict = "rejected " + strings.Join(codes, ",")
		}
		if bound != "" {
			verdict += " (not run: " + bound + ")"
			notRun = append(notRun, name)
		}
		fmt.Printf("C3-TAGGED %s\t%s\tfns %d runs %d violations %d skipped %d errors %d\t%s\n", name, verdict, fns, runs, viol, skipped, errs, first)
		if accepted && viol > 0 {
			t.Errorf("%s: accepted, and a run reports %s", name, first)
		}
	}
	t.Logf("%d files; %d rejected files not run, over a bound (fuel %d, %s): %v", len(files), len(notRun), rejFuel, rejWall, notRun)
}

// c3Flatten writes every per-field result clause `from (f: s1, s2, g: s3)`
// as its union `from s1, s2, s3`: the program's flattened twin, which
// keeps one loan set per result.
func c3Flatten(src string) (string, int) {
	var b strings.Builder
	n, i := 0, 0
	for {
		k := strings.Index(src[i:], "from (")
		if k < 0 {
			break
		}
		k += i
		j, depth := k+len("from ("), 1
		for depth > 0 {
			switch src[j] {
			case '(':
				depth++
			case ')':
				depth--
			}
			j++
		}
		var srcs []string
		for _, p := range strings.Split(src[k+len("from ("):j-1], ",") {
			p = strings.TrimSpace(p)
			if c := strings.Index(p, ":"); c >= 0 {
				p = strings.TrimSpace(p[c+1:])
			}
			if p != "" && !slices.Contains(srcs, p) {
				srcs = append(srcs, p)
			}
		}
		b.WriteString(src[i:k])
		b.WriteString("from " + strings.Join(srcs, ", "))
		i, n = j, n+1
	}
	b.WriteString(src[i:])
	return b.String(), n
}

// TestFlatTwin is the precision test of per-field clauses:
// MK_C3_FLAT programs of profile fields (base DefaultSoundBase), each
// checked as written and with every per-field clause flattened to its
// union, on this compiler. A program whose flattened twin is accepted
// while the per-field form is rejected with a loan code (E05xx) is a
// finding; the counts of every cell are logged, and MK_C3_FLAT_DUMP names
// a directory that receives the first 300 programs with a clause and their
// twins, for the one-set twin on the snapshot's compiler (mk-old.exe).
func TestFlatTwin(t *testing.T) {
	n := soundInt(t, "MK_C3_FLAT", 0)
	if n == 0 {
		t.Skip("MK_C3_FLAT not set")
	}
	dump := os.Getenv("MK_C3_FLAT_DUMP")
	s := soundSess(t)
	cells := map[string]int{}
	var bad []string
	dumped := 0
	for i := range n {
		p := GenerateSound(ProfileFields, ProgramSeed(DefaultSoundBase, i))
		flat, k := c3Flatten(p.Text)
		if k == 0 {
			cells["no clause"]++
			continue
		}
		a := soundCheck(s, p.Text)
		b := soundCheck(s, flat)
		ca, codesA := classifySoundProg(p, a.Report)
		cb, _ := classifySoundProg(p, b.Report)
		cells[ca+"/flat "+cb]++
		if cb == "accepted" && ca == "rejected" {
			cells["rejected/flat accepted: "+strings.Join(codesA, ",")]++
			for _, c := range codesA {
				if bodyCode(c) {
					bad = append(bad, fmt.Sprintf("%d %#016x %s", i, p.Seed, strings.Join(codesA, ",")))
					break
				}
			}
		}
		if dump != "" && dumped < 300 {
			dumped++
			_ = os.WriteFile(filepath.Join(dump, fmt.Sprintf("p%05d.mk", i)), []byte(p.Text), 0o644)
			_ = os.WriteFile(filepath.Join(dump, fmt.Sprintf("p%05d.flat.mk", i)), []byte(flat), 0o644)
		}
	}
	t.Logf("%d programs: %s", n, countTable(cells))
	for j, e := range bad {
		if j < 20 {
			t.Errorf("flattened twin accepted, per-field form rejected with a loan code: %s", e)
		}
	}
	if len(bad) > 0 {
		t.Errorf("%d findings", len(bad))
	}
}
