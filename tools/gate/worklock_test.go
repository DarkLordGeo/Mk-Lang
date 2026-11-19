// Why: the machine lock excludes tools/bench only if the gate takes the
// same file whatever -work it is given.
// What: TestLockIgnoresWork and TestEnvWith_WorkRoot.
// How: parse the gate's flags under a set $MK_WORK with another -work, and
// read the lock path and a step's environment.

package main

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"mk/tools/machinelock"
)

// TestLockIgnoresWork: with $MK_WORK=a and -work b, the gate works in b and
// locks a/machine.lock; an empty -lock is the same default, and -lock F
// is made absolute.
func TestLockIgnoresWork(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	t.Setenv(machinelock.WorkEnv, a)
	want := filepath.Join(a, "machine.lock")
	for _, args := range [][]string{{"-work", b}, {"-work", b, "-lock", ""}} {
		g, err := parseArgs(args)
		if err != nil {
			t.Fatal(err)
		}
		if g.work != b || g.lockPath != want {
			t.Errorf("%q: work %q lock %q, want work %q lock %q", args, g.work, g.lockPath, b, want)
		}
		if g.lockPath != machinelock.DefaultPath() {
			t.Errorf("%q: lock %q is not DefaultPath %q", args, g.lockPath, machinelock.DefaultPath())
		}
	}
	t.Chdir(b)
	g, err := parseArgs([]string{"-work", "w", "-lock", "l"})
	if err != nil {
		t.Fatal(err)
	}
	if g.work != filepath.Join(b, "w") || g.lockPath != filepath.Join(b, "l") {
		t.Errorf("relative -work %q -lock %q not made absolute under %q", g.work, g.lockPath, b)
	}
}

// TestEnvWith_WorkRoot: every step's child gets MK_WORK as the resolved
// work root once, so it resolves the gate's lock though TMP changes.
func TestEnvWith_WorkRoot(t *testing.T) {
	a := t.TempDir()
	t.Setenv(machinelock.WorkEnv, a)
	g := &gate{work: t.TempDir()}
	env := g.envWith(spec{argv: []string{"go", "test", "./..."}}, nil)
	n := 0
	for _, kv := range env {
		if k, _, _ := strings.Cut(kv, "="); sameKey(k, machinelock.WorkEnv) {
			n++
		}
	}
	if n != 1 || !slices.Contains(env, machinelock.WorkEnv+"="+a) {
		t.Errorf("MK_WORK entries %d, want one %s=%s", n, machinelock.WorkEnv, a)
	}
}
