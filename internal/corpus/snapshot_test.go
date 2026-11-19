// Why: the JSON report of every error code is a public format, and its bytes
// must not change by accident.
// What: TestSnapshots, TestE0000_Snapshot, TestSnapshotsDeterministic,
// TestSnapshotsMatchMkCheck, TestCRLFCheckoutStillMatches
// and the snapshot helpers.
// How: compares each passing codes/ unit's mk check --json bytes with
// codes/<code>.json; -update rewrites them.
//
// Snapshots live in tests/conformance/codes/; this package writes them only
// under -update (go test ./internal/corpus -run TestSnapshots -update), and
// the diff is read before it is kept. Only a passing unit has a snapshot to
// take; a missing one is an error in a gate run (MK_GATE=1) and logged
// otherwise.

package corpus

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"mk/internal/diag"
	"mk/internal/driver"
	"mk/internal/source"
)

// gateRun reports whether this is a gate run (the gate runs
// MK_GATE=1 go test ./...).
func gateRun() bool { return os.Getenv("MK_GATE") == "1" }

// checkSnapshot compares one check: a mismatch is an error, a missing file
// an error in a gate run; -update rewrites both. It returns whether the file
// was missing.
func checkSnapshot(t *testing.T, root string, c SnapshotCheck) (missing bool) {
	t.Helper()
	c.Got = snapshotTarget(c.Got)
	if bytes.Contains(c.Got, []byte(conformanceDir)) {
		t.Errorf("%s: the snapshot holds a repository path: %s", c.Key, c.Got)
	}
	switch {
	case c.Matches():
		return false
	case *update:
		if _, err := WriteSnapshots(root, []SnapshotCheck{c}); err != nil {
			t.Fatal(err)
		}
		t.Logf("wrote %s (read the diff before keeping it)", c.File)
		return c.Want == nil
	case c.Want == nil:
		if gateRun() {
			t.Errorf("%s: no snapshot %s (go test ./internal/corpus -run %s -update)", c.Key, c.File, updateRun(c))
		}
		return true
	}
	t.Errorf("%s: snapshot %s differs (go test ./internal/corpus -run %s -update, then read the diff)\n got: %s\nwant: %s", c.Key, c.File, updateRun(c), c.Got, c.Want)
	return false
}

// snapshotTarget rewrites the host target's generated-code directory in a
// check's JSON ("gen/x86_64-linux/", in E0905's facts) to x86_64-windows's,
// the target the snapshots are taken for, so a run on another host
// compares the same bytes.
func snapshotTarget(got []byte) []byte {
	host := driver.HostTarget().Name
	if host == "x86_64-windows" {
		return got
	}
	return bytes.ReplaceAll(got, []byte("gen/"+host+"/"), []byte("gen/x86_64-windows/"))
}

// Every passing codes/ unit's mk check --json bytes equal
// its snapshot; every snapshot file names a codes/ unit.
func TestSnapshots(t *testing.T) {
	root := repoRoot(t)
	r := fullRun(t)
	checks, err := Snapshots(root, r)
	if err != nil {
		t.Fatal(err)
	}
	var missing []string
	for _, c := range checks {
		if checkSnapshot(t, root, c) {
			missing = append(missing, c.Key)
		}
	}
	// Every snapshot file belongs to a codes/ unit (E0000 and E0001 to the Go
	// tests); a file whose unit is not passing is not compared.
	named := map[string]string{"E0000.json": "TestE0000_Snapshot", "E0001.json": "TestE0001_Snapshot"}
	var units, notPassing int
	for _, u := range r.Units {
		if n := SnapshotName(u.Unit); n != "" {
			named[n] = u.Unit.Key
			units++
			if u.Status != StatusPass {
				notPassing++
			}
		}
	}
	files, _ := filepath.Glob(filepath.Join(root, filepath.FromSlash(SnapshotDir), "*.json"))
	for _, f := range files {
		if _, ok := named[filepath.Base(f)]; !ok {
			t.Errorf("%s/%s names no codes/ unit", SnapshotDir, filepath.Base(f))
		}
	}
	t.Logf("%d codes/ units with a snapshot name: %d passing (compared: %d, missing: %d), %d not passing (no snapshot to take yet); %d snapshot files",
		units, len(checks), len(checks)-len(missing), len(missing), notPassing, len(files))
	if len(missing) > 0 && !gateRun() && !*update {
		t.Logf("missing snapshots (an error with MK_GATE=1): %s", strings.Join(missing, " "))
	}
}

// A unit whose one item's check raises an ICE (the
// driver's TestHooks.PanicIn) reports exactly one E0000 with facts where and
// what, and its snapshot is codes/E0000.json. The ICE is raised in stage 6,
// so the run uses the stand-in std, which lets units reach it (the embedded
// std holds them at parse until it declares the prelude).
func TestE0000_Snapshot(t *testing.T) {
	root := repoRoot(t)
	u := &Unit{Key: SnapshotDir + "/E0000.mk", Name: SnapshotDir + "/E0000.mk", Verdict: VerdictOK,
		Files: []UnitFile{{Path: "E0000.mk", Text: "F(): int {\n    return 1\n}\n"}}}
	cfg := driver.Config{StdDir: standInStd(t, root)}
	cfg.TestHooks.PanicIn = u.Name + ".F"
	comp := DriverCompiler(root, cfg)
	std, err := comp.Start()
	if err != nil {
		t.Fatal(err)
	}
	if std.Through < diag.PhaseTypes {
		t.Fatalf("through %s: the stand-in std must reach stage 6 (types)", std.Through)
	}
	r := comp.Check(u, driver.UnitCheck)
	ds := r.Report.Diags
	if len(ds) != 1 || ds[0].Diag.Code != diag.E0000 {
		t.Fatalf("want exactly one E0000, got %s", r.Report.Text(r.Sources))
	}
	var keys []string
	for _, f := range ds[0].Diag.Facts {
		keys = append(keys, f.Key)
	}
	if !slices.Contains(keys, "where") || !slices.Contains(keys, "what") {
		t.Errorf("E0000 facts %v, want where and what", keys)
	}
	got := Snapshot(u, r)
	if bytes.Contains(got, []byte(u.Name)) || !bytes.Contains(got, []byte("main.F")) {
		t.Errorf("the snapshot keeps the unit's repository path or loses the item: %s", got)
	}
	file := SnapshotDir + "/E0000.json"
	want, err := readCommitted(filepath.Join(root, filepath.FromSlash(file)))
	if err != nil {
		want = nil
	}
	checkSnapshot(t, root, SnapshotCheck{Key: "TestE0000_Snapshot", File: file, Got: got, Want: want})
}

// The snapshot is WriteJSON's envelope with the unit's
// virtual root removed from file paths and location facts, a subdirectory
// module named as mk check names it, and the root module main.
func TestSnapshotPathsAreUnitRelative(t *testing.T) {
	sm := source.NewSourceMap()
	name := SnapshotDir + "/E0205"
	a := sm.Add(name+"/a/a.mk", []byte("import b\n"))
	sm.Add(name+"/main.mk", []byte("F() { }\n"))
	d := diag.New(diag.E0205, source.NewSpan(a.ID, 0, 6), "import cycle "+name+"/a and "+name+"/b in "+name).
		Loc("other", source.NewSpan(a.ID, 7, 8))
	r := driver.BlockResult{Report: diag.Order([]*diag.Diagnostic{d}, sm, diag.NoCap), Sources: sm}
	u := &Unit{Key: name, Name: name, Codes: []diag.Code{diag.E0205}}
	got := string(Snapshot(u, r))
	for _, want := range []string{`"file":"a/a.mk"`, `import cycle a and b in main`, `"other":"a/a.mk:1:8"`} {
		if !strings.Contains(got, want) {
			t.Errorf("snapshot lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, conformanceDir) || !strings.HasSuffix(got, "}\n") || strings.Count(got, "\n") != 1 {
		t.Errorf("snapshot is not one unit-relative LF-terminated line:\n%q", got)
	}
}

// SnapshotName: codes/<code>.mk and codes/<code>/ have one; extra triggers,
// other suites and doc blocks do not.
func TestSnapshotName(t *testing.T) {
	for key, want := range map[string]string{
		SnapshotDir + "/E0501.mk":          "E0501.json",
		SnapshotDir + "/E0204":             "E0204.json",
		SnapshotDir + "/E0501_twice.mk":    "",
		conformanceDir + "/rules/E0501.mk": "",
		"doc/memory.md:197":                "",
	} {
		u := &Unit{Key: key, Codes: []diag.Code{diag.E0501}}
		if key == SnapshotDir+"/E0204" {
			u.Codes = []diag.Code{diag.E0204}
		}
		if got := SnapshotName(u); got != want {
			t.Errorf("SnapshotName(%s) = %q, want %q", key, got, want)
		}
	}
	two := &Unit{Key: SnapshotDir + "/E0501.mk", Codes: []diag.Code{diag.E0501, diag.E0505}}
	if SnapshotName(two) != "" {
		t.Errorf("a unit with two codes has a snapshot name")
	}
}

// WriteSnapshots writes exactly the missing and differing files, under the
// directory it is given (here a temporary one, never the tree).
func TestWriteSnapshotsWritesOnlyChanges(t *testing.T) {
	dir := t.TempDir()
	same := SnapshotCheck{Key: "a", File: SnapshotDir + "/E0101.json", Got: []byte("x\n"), Want: []byte("x\n")}
	diff := SnapshotCheck{Key: "b", File: SnapshotDir + "/E0102.json", Got: []byte("y\n"), Want: []byte("z\n")}
	miss := SnapshotCheck{Key: "c", File: SnapshotDir + "/E0103.json", Got: []byte("w\n")}
	wrote, err := WriteSnapshots(dir, []SnapshotCheck{same, diff, miss})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(wrote, []string{diff.File, miss.File}) {
		t.Errorf("wrote %v, want the differing and the missing file", wrote)
	}
	for _, c := range []SnapshotCheck{diff, miss} {
		b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(c.File)))
		if err != nil || !bytes.Equal(b, c.Got) {
			t.Errorf("%s: %q, %v", c.File, b, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(same.File))); err == nil {
		t.Errorf("an unchanged snapshot was written")
	}
}

// The snapshots of the codes/ units are the same bytes at -j 1 and
// -j 8, on two sessions.
func TestSnapshotsDeterministic(t *testing.T) {
	var runs [2][]SnapshotCheck
	for i, jobs := range []int{1, 8} {
		r, err := Run(Options{Conformance: true, Only: SnapshotDir + "/*", Jobs: jobs})
		if err != nil {
			t.Fatal(err)
		}
		if runs[i], err = Snapshots(repoRoot(t), r); err != nil {
			t.Fatal(err)
		}
	}
	if len(runs[0]) != len(runs[1]) {
		t.Fatalf("%d snapshots at -j 1, %d at -j 8", len(runs[0]), len(runs[1]))
	}
	for i := range runs[0] {
		if runs[0][i].Key != runs[1][i].Key || !bytes.Equal(runs[0][i].Got, runs[1][i].Got) {
			t.Errorf("%s: -j 1 and -j 8 differ:\n%s\n%s", runs[0][i].Key, runs[0][i].Got, runs[1][i].Got)
		}
	}
	t.Logf("%d snapshots equal at -j 1 and -j 8", len(runs[0]))
}

// updateRun is the -run pattern that rewrites c: the Go test of an E0000 or
// E0001 snapshot, else TestSnapshots.
func updateRun(c SnapshotCheck) string {
	if strings.HasPrefix(c.Key, "Test") {
		return "'^" + c.Key + "$'"
	}
	return "TestSnapshots"
}

// Files are LF only: a checkout with core.autocrlf=true and no
// .gitattributes writes the snapshots and the programs' .out, .err and .in
// files with CR LF. Snapshots and Runner.Expected read them as committed,
// so a correct compiler still matches; a file whose text differs still does
// not, and a program's own CR LF output is never translated. A file with a
// NUL byte (binary to git, which never translates it) is read unchanged.
func TestCRLFCheckoutStillMatches(t *testing.T) {
	root := t.TempDir()
	got := []byte(`{"phase":"names","diagnostics":[],"omitted":0}` + "\n")
	u := &Unit{Key: SnapshotDir + "/E0201.mk", Codes: []diag.Code{diag.E0201}}
	write := func(rel string, b []byte) {
		t.Helper()
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	crlf := func(b []byte) []byte { return bytes.ReplaceAll(b, []byte("\n"), []byte("\r\n")) }
	write(SnapshotDir+"/E0201.json", crlf(got))
	res := &Result{Units: []UnitResult{{Unit: u, Judgement: Judgement{Status: StatusPass}, Snapshot: got}}}
	checks, err := Snapshots(root, res)
	if err != nil || len(checks) != 1 {
		t.Fatalf("Snapshots: %v, %d checks", err, len(checks))
	}
	if !checks[0].Matches() {
		t.Errorf("a CR LF checkout of a matching snapshot does not match:\n got %q\nwant %q", checks[0].Got, checks[0].Want)
	}
	write(SnapshotDir+"/E0201.json", crlf([]byte(`{"phase":"types","diagnostics":[],"omitted":0}`+"\n")))
	if checks, _ = Snapshots(root, res); checks[0].Matches() {
		t.Errorf("a CR LF snapshot of other bytes matches")
	}

	out := []byte("1\n2\nFizz\n")
	write("tests/programs/x.out", crlf(out))
	write("tests/programs/x.err", []byte("a\x00\r\n"))
	r := NewRunner(root, driver.Config{}, t.TempDir())
	stdout, stderr, err := r.Expected(ProgramCase{Stdout: "tests/programs/x.out", Stderr: "tests/programs/x.err"})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(stdout, out) {
		t.Errorf("Expected stdout %q, want the committed %q", stdout, out)
	}
	if !bytes.Equal(stderr, []byte("a\x00\r\n")) {
		t.Errorf("Expected changed a binary file: %q", stderr)
	}
	if bytes.Equal(crlf(out), stdout) {
		t.Errorf("a program printing CR LF would match its LF .out")
	}
}

// snapshotUnitsNeedingPkgMk are the codes/ directory units whose snapshot
// mk check cannot reproduce until the unit has a pkg.mk: each holds modules
// in subdirectories, which the corpus session reads as modules (a
// conformance directory unit may hold several directories and a pkg.mk),
// while mk check's directory mode, with no pkg.mk, is a package of one
// module, so its `import util` is an E0202. The fix is a pkg.mk in each
// unit. The test fails when a listed unit starts to match, so the list
// only shrinks. It is empty: codes/E0204 and codes/E0205 have a pkg.mk,
// and mk check reproduces both snapshots.
var snapshotUnitsNeedingPkgMk []string

// A snapshot is the bytes mk check --json writes for the unit: for every
// codes/ unit with a snapshot file and no test prelude, a driver session
// rooted at the unit (mk check's root modes) writes exactly the snapshot,
// once the file's path as given is made unit-relative. A unit with
// `prelude: yes` is not compared: mk check never adds the test prelude, so
// its snapshot is the corpus session's.
func TestSnapshotsMatchMkCheck(t *testing.T) {
	root := repoRoot(t)
	units, err := Units(root)
	if err != nil {
		t.Fatal(err)
	}
	var compared, prelude int
	for i := range units {
		u := &units[i]
		name := SnapshotName(u)
		if name == "" {
			continue
		}
		if u.Meta.Prelude {
			prelude++
			continue
		}
		want, err := readCommitted(filepath.Join(root, filepath.FromSlash(SnapshotDir), name))
		if err != nil {
			continue // a missing snapshot is TestSnapshots' finding
		}
		compared++
		t.Run(u.Key, func(t *testing.T) {
			t.Parallel()
			p := filepath.Join(root, filepath.FromSlash(u.Key))
			s, err := driver.NewSession(driver.Config{Root: p, NoCache: true})
			if err != nil {
				t.Fatal(err)
			}
			res := s.Check()
			got := res.Report.WriteJSON(res.Sources)
			got = bytes.ReplaceAll(got, []byte(filepath.ToSlash(filepath.Dir(p))+"/"), nil)
			got = snapshotTarget(got)
			known := slices.Contains(snapshotUnitsNeedingPkgMk, u.Key)
			switch {
			case bytes.Equal(got, want) && known:
				t.Errorf("mk check now matches the snapshot: remove %s from snapshotUnitsNeedingPkgMk", u.Key)
			case bytes.Equal(got, want):
			case known:
				t.Logf("known: needs a pkg.mk; mk check --json gives %s", got)
			default:
				t.Errorf("mk check --json differs from %s/%s\n got: %s\nwant: %s", SnapshotDir, name, got, want)
			}
		})
	}
	if compared == 0 {
		t.Fatal("no codes/ unit compared")
	}
	t.Logf("%d snapshots compared with mk check, %d prelude units not comparable", compared, prelude)
}
