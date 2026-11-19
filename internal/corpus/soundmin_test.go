// Why: a soundness finding is only useful once it is minimised to a few
// lines, so the minimiser must shrink reliably and keep what is asked.
// What: TestSoundMin_Shrinks, TestSoundMin_KeepsAccepted, and tests of the
// tree the passes work on.
// How: planted faults with marked lines, shrunk under a candidate bound.

package corpus

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestSoundMin_TreeRoundTrip(t *testing.T) {
	for i := range 200 {
		p := GenerateSound(ProfileFull, ProgramSeed(23, i))
		if got := renderTree(parseTree(p.Text)); got != p.Text {
			t.Fatalf("program %d: the tree does not render back to the text", i)
		}
	}
}

// TestSoundMin_Shrinks: a planted fault, a predicate true when the text
// holds two marked lines, shrinks a program of at least 200 lines to those
// two lines within the budget, the same way twice (the candidate bound, not
// the time, decides).
func TestSoundMin_Shrinks(t *testing.T) {
	var p SoundProgram
	for i := 0; ; i++ {
		p = GenerateSound(ProfileFull, ProgramSeed(29, i))
		if p.Lines >= 200 {
			break
		}
	}
	lines := strings.Split(strings.TrimRight(p.Text, "\n"), "\n")
	// Mark two statement lines deep in the program.
	var marked []string
	for j := len(lines) * 2 / 3; j < len(lines) && len(marked) < 2; j++ {
		if strings.HasPrefix(lines[j], "        ") && !strings.HasSuffix(lines[j], "{") && !strings.HasPrefix(strings.TrimSpace(lines[j]), "}") {
			lines[j] += fmt.Sprintf(" // mark %d", len(marked))
			marked = append(marked, strings.TrimSpace(lines[j]))
		}
	}
	text := strings.Join(lines, "\n") + "\n"
	keep := func(s string) bool { return strings.Contains(s, "// mark 0") && strings.Contains(s, "// mark 1") }
	var outs []string
	for range 2 {
		got, st := MinimiseStats(text, keep, MinBudget{Candidates: 2000, Time: time.Minute, Workers: 4})
		outs = append(outs, got)
		t.Logf("%d lines to %d in %d candidates, %d rounds", st.FromLines, st.ToLines, st.Candidates, st.Rounds)
		var kept []string
		for _, l := range strings.Split(strings.TrimRight(got, "\n"), "\n") {
			kept = append(kept, strings.TrimSpace(l))
		}
		// The expression pass may simplify a marked line (Id(r) to r), so
		// the lines are compared by their marks.
		if len(kept) != 2 || !strings.HasSuffix(kept[0], "// mark 0") || !strings.HasSuffix(kept[1], "// mark 1") {
			t.Fatalf("minimised to %d lines %q, want the two marked ones %q", len(kept), kept, marked)
		}
	}
	if outs[0] != outs[1] {
		t.Fatal("two minimisations of the same text differ")
	}
}

// TestSoundMin_KeepsAccepted: minimising with a keep that needs the
// compiler's acceptance and a planted line never yields a rejected
// candidate, and the result still holds the line.
func TestSoundMin_KeepsAccepted(t *testing.T) {
	s := soundSess(t)
	var p SoundProgram
	for i := 0; ; i++ {
		p = GenerateSound(ProfileCore, ProgramSeed(31, i))
		if p.Lines >= 60 {
			if c, _ := classifySound(soundCheck(s, p.Text).Report); c == "accepted" {
				break
			}
		}
	}
	text := strings.Replace(p.Text, "    h = h *% 31 +% k\n", "    h = h *% 31 +% 42424242\n    h = h *% 31 +% k\n", 1)
	keep := func(s2 string) bool {
		if !strings.Contains(s2, plantViolation) {
			return false
		}
		c, _ := classifySound(soundCheck(s, s2).Report)
		return c == "accepted"
	}
	if !keep(text) {
		t.Fatal("the planted program is not accepted")
	}
	got, st := MinimiseStats(text, keep, MinBudget{Candidates: 1500, Time: time.Minute, Workers: 4})
	t.Logf("%d lines to %d in %d candidates", st.FromLines, st.ToLines, st.Candidates)
	if !keep(got) {
		t.Fatalf("the result is not accepted, or lost the plant:\n%s", got)
	}
	if st.ToLines > 12 {
		t.Errorf("minimised only to %d lines:\n%s", st.ToLines, got)
	}
}
