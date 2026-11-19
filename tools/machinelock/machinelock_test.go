// Why: the lock is only useful if a second holder is really refused, and
// only if every tool finds the same lock file.
// What: TestExclusive and TestWorkRoot.
// How: takes the lock, checks a second open is ErrBusy, releases twice and
// takes it again; resolves the work root with each source of it in turn.

package machinelock

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// TestExclusive: while one Lock holds the file a second open is ErrBusy,
// and after Release it can be taken again.
func TestExclusive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "machine.lock")
	l, err := Acquire(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if f, err := tryLock(path); !errors.Is(err, ErrBusy) {
		if f != nil {
			f.Close()
		}
		t.Fatalf("second open while held: %v, want ErrBusy", err)
	}
	if err := l.Release(); err != nil {
		t.Fatal(err)
	}
	if err := l.Release(); err != nil {
		t.Fatalf("second Release: %v", err)
	}
	l2, err := Acquire(path, nil)
	if err != nil {
		t.Fatalf("after Release: %v", err)
	}
	l2.Release()
}

// TestWorkRoot: an absolute $MK_WORK wins (a relative one is ignored, and
// CheckEnv reports it), then mk-work under the user's cache
// directory, then mk-work under the temp directory; the lock file is
// machine.lock in the root.
func TestWorkRoot(t *testing.T) {
	cache := func() (string, error) { return filepath.FromSlash("/c/cache"), nil }
	noCache := func() (string, error) { return "", errors.New("no home") }
	temp := func() string { return filepath.FromSlash("/c/tmp") }
	env := func(v string) func(string) string {
		return func(k string) string {
			if k == WorkEnv {
				return v
			}
			return ""
		}
	}
	abs := t.TempDir()
	for _, tc := range []struct {
		name  string
		env   string
		cache func() (string, error)
		want  string
	}{
		{"env", abs, cache, abs},
		{"relative env", filepath.FromSlash("w/root"), cache, filepath.FromSlash("/c/cache/mk-work")},
		{"cache", "", cache, filepath.FromSlash("/c/cache/mk-work")},
		{"temp", "", noCache, filepath.FromSlash("/c/tmp/mk-work")},
	} {
		if got := workRoot(env(tc.env), tc.cache, temp); got != tc.want {
			t.Errorf("%s: workRoot %q, want %q", tc.name, got, tc.want)
		}
	}
	t.Setenv(WorkEnv, "rel")
	if err := CheckEnv(); err == nil {
		t.Errorf("CheckEnv accepted a relative %s", WorkEnv)
	}
	root := t.TempDir()
	t.Setenv(WorkEnv, root)
	if err := CheckEnv(); err != nil {
		t.Errorf("CheckEnv: %v", err)
	}
	if got, want := DefaultPath(), filepath.Join(root, "machine.lock"); got != want {
		t.Errorf("DefaultPath %q, want %q", got, want)
	}
	if _, err := os.Stat(DefaultPath()); !os.IsNotExist(err) {
		t.Errorf("DefaultPath created the lock file: %v", err)
	}
}
