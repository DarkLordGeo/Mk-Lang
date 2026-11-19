// Why: the driver's half of profile-guided optimization (pgo.go) decides
// which profile a build reads and how a bad one is reported.
// What: TestPGOChangesBranchWeights and TestPGOBadProfileIsE0001, over
// --pgo-gen, mk profile merge, --pgo, default.mkprof and --pgo=off.
// How: writes a profile from the instrumented build's own ProfileTable and
// reads the branch weights in the IR of later builds.

package driver

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"

	"mk/internal/pgo"
	"mk/internal/source"
	"mk/internal/types"
)

const pgoProgram = "Pick(x: int): int {\n    if x > 3 {\n        return 1\n    }\n    return 2\n}\n\n" +
	"export Main(sys: &System): int {\n    return Pick(5)\n}\n"

// pgoBuild is one release build of root with mode and file; it returns
// every pack's .ll text by pack name.
func pgoBuild(t *testing.T, root string, mode PGOMode, file string) map[string]string {
	t.Helper()
	var mu sync.Mutex
	lls := map[string]string{}
	cfg := Config{Root: root, CacheDir: t.TempDir(), Target: types.X86_64Windows, Profile: types.Release, PGO: mode, PGOFile: file,
		Tools: needTools(t), TestHooks: TestHooks{OnLL: func(name, ll string) {
			mu.Lock()
			lls[name] = ll
			mu.Unlock()
		}}}
	s, err := NewSession(cfg)
	if err != nil {
		t.Fatal(err)
	}
	r, _, err := s.Build(filepath.Join(t.TempDir(), "p.exe"))
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Report.Diags) > 0 {
		skipKnownBlockers(t, r)
		t.Fatalf("build:\n%s", r.Report.Text(r.Sources))
	}
	return lls
}

// llvmCString decodes the body of an LLVM c"..." constant.
func llvmCString(t *testing.T, s string) []byte {
	t.Helper()
	var out []byte
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' {
			out = append(out, s[i])
			continue
		}
		b, err := hex.DecodeString(s[i+1 : i+3])
		if err != nil {
			t.Fatalf("escape %q: %v", s[i:i+3], err)
		}
		out = append(out, b...)
		i += 2
	}
	return out
}

// profileRecord finds fn's record in an instrumented support pack's
// ProfileTable: its CFG checksum and counter count.
func profileRecord(t *testing.T, support, fn string) (source.Hash128, int) {
	t.Helper()
	re := regexp.MustCompile(`\[16 x i8\] c"((?:[^"\\]|\\[0-9A-F]{2})*)", ptr @"?` + regexp.QuoteMeta(fn) + `\.pgo"?, i64 (\d+)`)
	m := re.FindStringSubmatch(support)
	if m == nil {
		t.Fatalf("no ProfileTable record of %s in the --pgo-gen support pack", fn)
	}
	var sum source.Hash128
	if b := llvmCString(t, m[1]); len(b) != len(sum) {
		t.Fatalf("checksum of %d bytes", len(b))
	} else {
		copy(sum[:], b)
	}
	n, _ := strconv.Atoi(m[2])
	return sum, n
}

// pickWeights is the text of Pick's definition in the packs, and whether
// it carries branch weights.
func pickWeights(t *testing.T, lls map[string]string) bool {
	t.Helper()
	for _, ll := range lls {
		i := strings.Index(ll, "define ")
		for i >= 0 {
			end := strings.Index(ll[i:], "\n}\n")
			if end < 0 {
				break
			}
			def := ll[i : i+end]
			if strings.Contains(def[:strings.Index(def, "\n")], "@mk.main.Pick(") {
				return strings.Contains(def, "!prof")
			}
			next := strings.Index(ll[i+end:], "define ")
			if next < 0 {
				break
			}
			i += end + next
		}
	}
	t.Fatal("no definition of mk.main.Pick in the packs")
	return false
}

// TestPGOChangesBranchWeights: a --pgo-gen build carries Pick's counters and
// its ProfileTable record; a profile for it, merged by mk profile merge,
// gives Pick branch weights in a --pgo build and through default.mkprof, and
// none with --pgo=off or no profile. The program is not run: the profile is
// written from the instrumented build's own table, with the counts a run of
// Pick(5) ten times would give.
func TestPGOChangesBranchWeights(t *testing.T) {
	dir := writeTree(t, map[string]string{"p.mk": pgoProgram})
	root := filepath.Join(dir, "p.mk")
	gen := pgoBuild(t, root, PGOGen, "")
	if !strings.Contains(gen["support"], "@mk.main.Pick.pgo") {
		t.Fatal("the --pgo-gen support pack names no counters of Pick")
	}
	sum, n := profileRecord(t, gen["support"], "mk.main.Pick")
	if n != 3 {
		t.Fatalf("Pick has %d counters, want 3 (entry, then the if's two edges)", n)
	}
	raw := &pgo.Profile{Records: []pgo.Record{{Key: "mk.main.Pick", Checksum: sum, Counts: []uint64{10, 10, 0}}}}
	part := filepath.Join(t.TempDir(), "part.mkprof")
	f, err := os.Create(part)
	if err != nil {
		t.Fatal(err)
	}
	if err := raw.Write(f); err != nil {
		t.Fatal(err)
	}
	f.Close()
	merged := filepath.Join(t.TempDir(), "merged.mkprof")
	if _, err := MergeProfiles(merged, []string{part, part}); err != nil {
		t.Fatal(err)
	}
	if pickWeights(t, pgoBuild(t, root, PGOOff, "")) {
		t.Error("--pgo=off: Pick has branch weights")
	}
	if pickWeights(t, pgoBuild(t, root, PGODefault, "")) {
		t.Error("no default.mkprof: Pick has branch weights")
	}
	if !pickWeights(t, pgoBuild(t, root, PGOUse, merged)) {
		t.Error("--pgo=merged.mkprof: Pick has no branch weights")
	}
	b, err := os.ReadFile(merged)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, DefaultProfile), b, 0o644); err != nil {
		t.Fatal(err)
	}
	if !pickWeights(t, pgoBuild(t, root, PGODefault, "")) {
		t.Error("default.mkprof: Pick has no branch weights")
	}
	if pickWeights(t, pgoBuild(t, root, PGOOff, "")) {
		t.Error("--pgo=off beside default.mkprof: Pick has branch weights")
	}
}

// TestPGOBadProfileIsE0001: a --pgo file that is not a .mkprof is one
// E0001 naming it, and nothing is built.
func TestPGOBadProfileIsE0001(t *testing.T) {
	dir := writeTree(t, map[string]string{"p.mk": pgoProgram, "bad.mkprof": "not a profile"})
	cfg := Config{Root: filepath.Join(dir, "p.mk"), CacheDir: t.TempDir(), Target: types.X86_64Windows, Profile: types.Release,
		PGO: PGOUse, PGOFile: filepath.Join(dir, "bad.mkprof"), Tools: needTools(t)}
	s, err := NewSession(cfg)
	if err != nil {
		t.Fatal(err)
	}
	r, paths, _ := s.Build(filepath.Join(t.TempDir(), "p.exe"))
	if got := codes(r); len(got) != 1 || got[0] != "E0001" || len(paths) != 0 || !strings.Contains(r.Report.Text(r.Sources), "bad.mkprof") {
		t.Errorf("codes %q, paths %q\n%s", got, paths, r.Report.Text(r.Sources))
	}
}
