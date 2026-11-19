// Why: the edit-loop measurement must time the step it names, from the cache
// it claims, and never a stale build or a no-op that wrote files.
// What: TestEditSequenceOrder to TestEditWritten, over editloop.go.
// How: fakeEditLoop replaces the synth and mk runners with recorders, so the
// tests run the sequence logic without a workspace or a compiler.

package main

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// fakeEditLoop replaces the synth and mk runners with recorders: synth
// edits write one file (noop none), and mk fails when failOn matches the
// step count so far.
type fakeEditLoop struct {
	calls  []string
	caches []string
	mkRuns int
	failOn int // the mk run (1-based) that fails; 0 for none
	noopW  int // files a noop claims to write
	edits  int // body and sig edits so far: the fake program's output
	stale  bool
	// staleCheck: the fake check never reads the body (an unsound warm
	// check); noopSays: what the fake mk prints on its second run.
	staleCheck bool
	noopSays   string
	body       int
}

func (f *fakeEditLoop) install(t *testing.T) {
	oldE, oldS, oldP := editRunner, synthRunner, progRunner
	t.Cleanup(func() { editRunner, synthRunner, progRunner = oldE, oldS, oldP })
	progRunner = func(string) (string, error) {
		if f.stale {
			return "1", nil
		}
		return strconv.Itoa(f.edits), nil
	}
	synthRunner = func(exe string, args ...string) (string, error) {
		edit := args[len(args)-1]
		f.calls = append(f.calls, "synth "+edit)
		if edit == "body" || edit == "sig" {
			f.edits++
		}
		if edit == "body" || edit == "reset" {
			// As tools/synth: the body edit's line in one file, and
			// synth.json's revision.
			if edit == "body" {
				f.body++
			} else {
				f.body = 0
			}
			ws := args[1]
			os.WriteFile(filepath.Join(ws, "synth.json"), []byte(`{"lines": 1000, "body": `+strconv.Itoa(f.body)+`}`), 0o644)
			p := filepath.Join(ws, "b.mk")
			os.WriteFile(p, []byte("module b\nfn g() {\n    var acc: i64 = "+strconv.Itoa(7+f.body)+"\n}\n"), 0o644)
			later := time.Now().Add(time.Duration(f.edits+1) * time.Second)
			os.Chtimes(p, later, later)
		}
		if edit == "noop" {
			if f.noopW > 0 {
				return "synth: edit noop (body revision 0, signature edit false): 3 files written", nil
			}
			return "synth: noop: nothing written", nil
		}
		return "synth: edit " + edit + " (body revision 1, signature edit true): 2 files written", nil
	}
	editRunner = func(c buildCmd) (time.Duration, uint64, string, error) {
		f.mkRuns++
		f.calls = append(f.calls, "mk "+c.Args[1])
		for _, e := range c.Env {
			if v, ok := strings.CutPrefix(e, "MK_CACHE="); ok {
				f.caches = append(f.caches, v)
				os.MkdirAll(v, 0o755) // as mk does
			}
		}
		if f.mkRuns == f.failOn {
			return 0, 0, "", errors.New("exit status 1")
		}
		if c.Args[1] == "check" && !f.staleCheck {
			b, _ := os.ReadFile(filepath.Join(c.Dir, "b.mk"))
			if i := strings.Index(string(b), "mkbenchUndefined"); i >= 0 {
				name := strings.Fields(string(b[i:]))[0]
				return time.Second, 1 << 30, "E0201 unknown name `" + name + "`", errors.New("exit status 1")
			}
		}
		if f.mkRuns == 2 && f.noopSays != "" {
			return time.Second, 1 << 30, f.noopSays, nil
		}
		return time.Second, 1 << 30, "", nil
	}
}

func testEditOptions(t *testing.T) editOptions {
	ws := t.TempDir()
	os.WriteFile(filepath.Join(ws, "synth.json"), []byte(`{"lines": 1000}`), 0o644)
	os.WriteFile(filepath.Join(ws, "a.mk"), []byte("module a\nfn f() {}\n"), 0o644)
	return editOptions{Work: t.TempDir(), WS: ws, Synth: "synth", Runs: 1, By: "test", Modes: []string{"check"},
		Mks: []realMk{{Label: "head", Path: "mk-head"}}, Log: func(string) {}}
}

// TestEditSequenceOrder: one sequence is reset, cold, noop, body, sig,
// reset, every mk run in the same fresh cache, which is gone afterwards.
func TestEditSequenceOrder(t *testing.T) {
	var f fakeEditLoop
	f.install(t)
	o := testEditOptions(t)
	runs, err := editSequence(o, o.Mks[0], "check", 1)
	if err != nil {
		t.Fatal(err)
	}
	// After the body step, two untimed checks: the unknown name, then the body put back.
	want := []string{"synth reset", "mk check", "synth noop", "mk check", "synth body", "mk check", "mk check", "mk check", "synth sig", "mk check", "synth reset"}
	if !slices.Equal(f.calls, want) {
		t.Fatalf("calls %v, want %v", f.calls, want)
	}
	if len(runs) != 4 || runs[0].Step != "cold" || runs[3].Step != "sig" || runs[1].Written != 0 || runs[2].Written != 2 {
		t.Fatalf("runs %+v", runs)
	}
	for _, c := range f.caches {
		if c != f.caches[0] {
			t.Fatalf("one sequence used two caches: %v", f.caches)
		}
	}
	if _, err := os.Stat(filepath.Dir(f.caches[0])); !os.IsNotExist(err) {
		t.Fatalf("the sequence's cache directory is left: %v", err)
	}
}

// TestEditSequenceFailureStops: a failing mk run ends the sequence there.
func TestEditSequenceFailureStops(t *testing.T) {
	f := fakeEditLoop{failOn: 3}
	f.install(t)
	o := testEditOptions(t)
	runs, err := editSequence(o, o.Mks[0], "build", 1)
	if err == nil || !strings.Contains(err.Error(), "body") {
		t.Fatalf("err %v, want a failure at the body step", err)
	}
	if len(runs) != 2 || f.mkRuns != 3 {
		t.Fatalf("runs %d, mk runs %d after a failure at the third", len(runs), f.mkRuns)
	}
}

// TestEditSequenceNoopWrites: a noop that writes files is not a no-op run.
func TestEditSequenceNoopWrites(t *testing.T) {
	f := fakeEditLoop{noopW: 3}
	f.install(t)
	o := testEditOptions(t)
	if _, err := editSequence(o, o.Mks[0], "check", 1); err == nil || !strings.Contains(err.Error(), "noop wrote 3") {
		t.Fatalf("err %v", err)
	}
}

// TestEditLoopFreshCaches: every mk, mode and round gets its own cache, a
// check never shares one with a build, and the binaries are interleaved.
func TestEditLoopFreshCaches(t *testing.T) {
	var f fakeEditLoop
	f.install(t)
	o := testEditOptions(t)
	o.Mks = append(o.Mks, realMk{Label: "main", Path: "mk-main"})
	o.Modes = []string{"check", "build"}
	o.Runs = 2
	res, err := editLoop(o, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Runs) != 2*2*2*4 {
		t.Fatalf("%d runs", len(res.Runs))
	}
	seen := map[string]bool{}
	for _, c := range f.caches {
		seen[c] = true
	}
	if len(seen) != 8 {
		t.Fatalf("%d caches, want 8 (one per mk, mode and round): %v", len(seen), f.caches)
	}
	if res.Runs[0].Mk != "head" || res.Runs[16].Mk != "main" {
		t.Fatalf("round 2 does not start with the other binary: %s, %s", res.Runs[0].Mk, res.Runs[16].Mk)
	}
	if !strings.Contains(res.Label, "non-idle, provisional") || !strings.Contains(res.Label, "measured by test") {
		t.Fatalf("label %q", res.Label)
	}
}

// TestEditCmd: build writes its program beside the cache, check does not,
// and mk's environment drops the caller's throttles and frame-pointer
// setting.
func TestEditCmd(t *testing.T) {
	o := editOptions{WS: `W`, Jobs: 8}
	c := editCmd(o, realMk{Path: "mk"}, "check", filepath.Join("X", "mkcache"))
	if strings.Join(c.Args, " ") != "mk check -j 8 ." || c.Dir != "W" {
		t.Fatalf("check %v in %s", c.Args, c.Dir)
	}
	b := editCmd(o, realMk{Path: "mk"}, "build", filepath.Join("X", "mkcache"))
	if !slices.Contains(b.Args, "-o") || b.Args[len(b.Args)-1] != "." {
		t.Fatalf("build %v", b.Args)
	}
	for _, k := range []string{"GOMAXPROCS=", "GOGC=", "MK_FRAME_POINTERS="} {
		if !slices.Contains(c.Env, k) {
			t.Fatalf("env %v lacks %s", c.Env, k)
		}
	}
	env := compileEnv([]string{"GOMAXPROCS=4", "MK_FRAME_POINTERS=on", "PATH=x"}, c.Env)
	for _, kv := range env {
		if strings.HasPrefix(kv, "GOMAXPROCS") || strings.HasPrefix(kv, "MK_FRAME_POINTERS") {
			t.Fatalf("env keeps %s", kv)
		}
	}
}

// TestEditSequenceStaleBuild: in build mode a body edit whose program
// prints what it printed before fails the sequence.
func TestEditSequenceStaleBuild(t *testing.T) {
	f := fakeEditLoop{stale: true}
	f.install(t)
	o := testEditOptions(t)
	runs, err := editSequence(o, o.Mks[0], "build", 1)
	if err == nil || !strings.Contains(err.Error(), "stale build") || len(runs) != 2 {
		t.Fatalf("err %v after %d runs", err, len(runs))
	}
	g := fakeEditLoop{}
	g.install(t)
	if _, err := editSequence(o, o.Mks[0], "build", 1); err != nil {
		t.Fatal(err)
	}
}

// TestEditSequenceStaleCheck: in check mode, a warm check that does not see
// the edited body (it passes with an unknown name in it) fails the
// sequence, and the workspace's file is put back.
func TestEditSequenceStaleCheck(t *testing.T) {
	f := fakeEditLoop{staleCheck: true}
	f.install(t)
	o := testEditOptions(t)
	runs, err := editSequence(o, o.Mks[0], "check", 1)
	if err == nil || !strings.Contains(err.Error(), "stale check") || len(runs) != 3 {
		t.Fatalf("err %v after %d runs", err, len(runs))
	}
	b, _ := os.ReadFile(filepath.Join(o.WS, "b.mk"))
	if strings.Contains(string(b), "mkbenchUndefined") {
		t.Fatalf("the guard left its edit in the workspace: %s", b)
	}
}

// TestEditSequenceNoopOutput: a no-op run that prints something the cold
// run did not fails the sequence.
func TestEditSequenceNoopOutput(t *testing.T) {
	f := fakeEditLoop{noopSays: "1 module checked again"}
	f.install(t)
	o := testEditOptions(t)
	if _, err := editSequence(o, o.Mks[0], "check", 1); err == nil || !strings.Contains(err.Error(), "no-op run printed") {
		t.Fatalf("err %v", err)
	}
}

// TestEditLoopFailureLabelled: a failed run's results still say who
// measured them and that they are provisional.
func TestEditLoopFailureLabelled(t *testing.T) {
	f := fakeEditLoop{failOn: 2}
	f.install(t)
	o := testEditOptions(t)
	res, err := editLoop(o, nil)
	if err == nil || res == nil || !strings.Contains(res.Label, "measured by test") || !strings.Contains(res.Label, "provisional") {
		t.Fatalf("err %v, label %q", err, res.Label)
	}
}

func TestEditLoopNeedsWho(t *testing.T) {
	o := testEditOptions(t)
	o.By = ""
	if _, err := editLoop(o, nil); err == nil {
		t.Fatal("editloop ran with no -by")
	}
}

func TestEditWritten(t *testing.T) {
	for in, want := range map[string]int{
		"synth: noop: nothing written": 0,
		"synth: edit body (body revision 1, signature edit false): 1 files written": 1,
		"synth: edit sig (body revision 0, signature edit true): 9 files written":   9,
	} {
		if n, err := editWritten(in); err != nil || n != want {
			t.Fatalf("%q: %d %v", in, n, err)
		}
	}
	if _, err := editWritten("garbage"); err == nil {
		t.Fatal("no count accepted")
	}
}
