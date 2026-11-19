// Why: the mutation test is only as good as its table of mutants, which must
// parse, apply and cover every borrow rule.
// What: TestMutant_Parse, TestMutant_Apply, TestMutant_OverlayAlso,
// TestMutant_Coverage and TestMutant_TableFresh, with mutantOverlay.
// How: the table checks run without a build; TestMutant_TableFresh
// (MK_SOUND=1) reads borrow's current source, which other edits may move.
// The runs themselves are in mutantrun_test.go.

package corpus

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// readMutantTable reads every tests/soundness/mutants*.txt, in name order
// (mutants.txt, then mutants-flows8.txt); an ID is unique across
// the files.
func readMutantTable(t *testing.T) []Mutant {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(soundRoot(t), "mutants*.txt"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("no mutant table under %s: %v", soundRoot(t), err)
	}
	slices.Sort(paths)
	var all []Mutant
	seen := map[string]string{}
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		ms, err := ReadMutants(p, b)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range ms {
			if prev, ok := seen[m.ID]; ok {
				t.Fatalf("mutant %s in %s and in %s", m.ID, prev, filepath.Base(p))
			}
			seen[m.ID] = filepath.Base(p)
		}
		all = append(all, ms...)
	}
	return all
}

func TestMutant_Parse(t *testing.T) {
	ms := readMutantTable(t)
	if len(ms) < 20 {
		t.Fatalf("%d mutants, want the table of 4.6.3", len(ms))
	}
	for _, bad := range []string{
		"rule R5\n",
		"mutant M-X\nrule R5\nclass Q\nend\n",
		"mutant M-X\nrule R5\nclass R\nfile f.go\nanchor a\nreplace a\nend\n",
		"mutant M-X\nrule R5\nclass R\nfile f.go\nanchor a\nreplace b\n",
		"mutant M-X\nrule R5\nclass R\nfile f.go\nanchor a\nreplace b\nend\nmutant M-X\nrule R5\nclass R\nfile f.go\nanchor a\nreplace b\nend\n",
		"mutant M-X\nwhat x\nend\n",
		// also patches: only on a witness row, each whole, never first.
		"mutant M-X\nrule R5\nclass R\nfile f.go\nanchor a\nreplace b\nalso-file g.go\nalso-anchor c\nalso-replace d\nend\n",
		"mutant M-X\nrule R5\nclass R\nfile f.go\nanchor a\nreplace b\nwitness w.mk\nalso-anchor c\nend\n",
		"mutant M-X\nrule R5\nclass R\nfile f.go\nanchor a\nreplace b\nwitness w.mk\nalso-file g.go\nalso-replace d\nend\n",
		"mutant M-X\nrule R5\nclass R\nfile f.go\nanchor a\nreplace b\nwitness w.mk\nalso-file g.go\nalso-anchor c\nalso-replace c\nend\n",
	} {
		if _, err := ReadMutants("t", []byte(bad)); err == nil {
			t.Errorf("ReadMutants accepted %q", bad)
		}
	}
	got, err := ReadMutants("t", []byte("# c\nmutant M-A\nrule R3 R4\nclass P\nfile internal/borrow/x.go\nanchor a\\tb\\nc\\\\d\nreplace z\nnote n\nend\n"))
	if err != nil || len(got) != 1 || got[0].Anchor != "a\tb\nc\\d" || got[0].Class != 'P' || !slices.Equal(got[0].Rules(), []string{"R3", "R4"}) {
		t.Fatalf("ReadMutants = %+v, %v", got, err)
	}
	got, err = ReadMutants("t", []byte("mutant M-B\nrule R11\nclass R\nfile internal/borrow/x.go\nanchor a\nreplace b\nwitness w.mk\nalso-file internal/borrow/y.go\nalso-anchor c\\t\nalso-replace d\nend\n"))
	want := []MutantPatch{{"internal/borrow/x.go", "a", "b"}, {"internal/borrow/y.go", "c\t", "d"}}
	if err != nil || len(got) != 1 || !slices.Equal(got[0].Patches(), want) {
		t.Fatalf("ReadMutants with an also patch = %+v, %v", got, err)
	}
}

// TestMutant_OverlayAlso: a row's also patches land in the overlay with its
// first, two of one file in order.
func TestMutant_OverlayAlso(t *testing.T) {
	root := t.TempDir()
	for name, text := range map[string]string{"x.go": "a\nb\n", "y.go": "c\n"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	m := Mutant{ID: "M-O", File: "x.go", Anchor: "a\n", Replace: "A\n", Witness: "w.mk",
		Also: []MutantPatch{{"y.go", "c\n", "C\n"}, {"x.go", "b\n", "B\n"}}}
	dir := t.TempDir()
	of, err := mutantOverlay(dir, root, m)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(of)
	if err != nil {
		t.Fatal(err)
	}
	var ov struct{ Replace map[string]string }
	if err := json.Unmarshal(b, &ov); err != nil || len(ov.Replace) != 2 {
		t.Fatalf("overlay %s: %v", b, err)
	}
	for name, want := range map[string]string{"x.go": "A\nB\n", "y.go": "C\n"} {
		got, err := os.ReadFile(ov.Replace[filepath.Join(root, name)])
		if err != nil || string(got) != want {
			t.Errorf("%s patched = %q, %v; want %q", name, got, err, want)
		}
	}
}

func TestMutant_Apply(t *testing.T) {
	m := Mutant{ID: "M-T", Anchor: "x := 1\n", Replace: "x := 2\n", File: "f.go"}
	got, err := ApplyMutant([]byte("a\r\nx := 1\r\nb\r\n"), m)
	if err != nil || string(got) != "a\nx := 2\nb\n" {
		t.Fatalf("ApplyMutant = %q, %v", got, err)
	}
	for _, src := range []string{"a\n", "x := 1\nx := 1\n"} {
		if _, err := ApplyMutant([]byte(src), m); err == nil || !strings.Contains(err.Error(), "times") {
			t.Errorf("ApplyMutant(%q) = %v, want the anchor's count", src, err)
		}
	}
	if _, err := ApplyMutant([]byte("x := 1\n"), Mutant{ID: "M-P", Pending: "later"}); err == nil {
		t.Error("ApplyMutant applied a pending row")
	}
}

// TestMutant_Coverage: a row for every rule the table's rule fields name
// (R2 to R18, R20 and R21), and every row a file of internal/borrow; a row
// with witnesses may be a file of internal/check, and its witnesses exist.
func TestMutant_Coverage(t *testing.T) {
	ms := readMutantTable(t)
	have := map[string]bool{}
	for _, m := range ms {
		for _, r := range m.Rules() {
			have[r] = true
		}
		for _, p := range m.Patches() {
			pkgOK := strings.HasPrefix(p.File, "internal/borrow/") || m.Witness != "" && strings.HasPrefix(p.File, "internal/check/")
			if !pkgOK || !strings.HasSuffix(p.File, ".go") || strings.HasSuffix(p.File, "_test.go") {
				t.Errorf("%s: file %s is not a source file of internal/borrow (or, for a witness row, internal/check)", m.ID, p.File)
			}
		}
		for _, w := range m.Witnesses() {
			if _, err := os.Stat(filepath.Join(soundRoot(t), "witness", w)); err != nil {
				t.Errorf("%s: witness %s: %v", m.ID, w, err)
			}
		}
	}
	for _, r := range MutantRules {
		if !have[r] {
			t.Errorf("no mutant for %s", r)
		}
	}
}

// TestMutant_TableFresh (MK_SOUND=1): every anchor occurs exactly once in
// borrow's current source, and each patched file still parses as Go.
func TestMutant_TableFresh(t *testing.T) {
	if os.Getenv("MK_SOUND") != "1" {
		t.Skip("MK_SOUND=1 not set")
	}
	root := repoRoot(t)
	for _, m := range readMutantTable(t) {
		if m.Pending != "" {
			t.Logf("%s (%s): pending: %s", m.ID, m.Rule, m.Pending)
			continue
		}
		stale := false
		pkgs := map[string]bool{}
		for _, p := range m.Patches() {
			src, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(p.File)))
			if err != nil {
				t.Errorf("%s: %v", m.ID, err)
				stale = true
				continue
			}
			if _, err := ApplyPatch(src, m.ID, p); err != nil {
				t.Errorf("%v (the anchor is stale: rewrite it)", err)
				stale = true
			}
			pkgs["./"+filepath.ToSlash(filepath.Dir(p.File))] = true
		}
		if stale {
			continue
		}
		overlay, err := mutantOverlay(t.TempDir(), root, m)
		if err != nil {
			t.Fatal(err)
		}
		args := []string{"build", "-overlay=" + overlay}
		for _, p := range m.Patches() {
			if d := "./" + filepath.ToSlash(filepath.Dir(p.File)); pkgs[d] {
				args = append(args, d)
				delete(pkgs, d)
			}
		}
		cmd := exec.Command(filepath.Join(runtime.GOROOT(), "bin", "go"), args...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Errorf("%s: the mutated package does not build: %v\n%s", m.ID, err, out)
		}
	}
}

// mutantOverlay writes m's patched files and a go -overlay file mapping
// each source file to its patched copy, under dir, and
// returns the overlay file's path. Patches of one file apply in order.
func mutantOverlay(dir, root string, m Mutant) (string, error) {
	if m.Pending != "" {
		return "", fmt.Errorf("mutant %s is pending: %s", m.ID, m.Pending)
	}
	text := map[string][]byte{}
	var order []string
	for _, p := range m.Patches() {
		path := filepath.Join(root, filepath.FromSlash(p.File))
		src, ok := text[path]
		if !ok {
			b, err := os.ReadFile(path)
			if err != nil {
				return "", err
			}
			src = b
			order = append(order, path)
		}
		patched, err := ApplyPatch(src, m.ID, p)
		if err != nil {
			return "", err
		}
		text[path] = patched
	}
	repl := map[string]string{}
	for i, path := range order {
		pf := filepath.Join(dir, fmt.Sprintf("%d-%s", i, filepath.Base(path)))
		if i == 0 {
			pf = filepath.Join(dir, filepath.Base(path))
		}
		if err := os.WriteFile(pf, text[path], 0o644); err != nil {
			return "", err
		}
		repl[path] = pf
	}
	j, err := json.Marshal(map[string]map[string]string{"Replace": repl})
	if err != nil {
		return "", err
	}
	of := filepath.Join(dir, "overlay.json")
	return of, os.WriteFile(of, j, 0o644)
}
