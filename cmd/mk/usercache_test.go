// Why: with no MK_CACHE set, the CLI must pick one cache per user in the
// platform's usual place.
// What: TestUserCacheRoot and TestUserCacheDefault.
// How: the lookup reads MK_CACHE, then %LOCALAPPDATA%\mk\cache on Windows and
// $XDG_CACHE_HOME/mk, else ~/.cache/mk, elsewhere; every variable points into
// the test's own directories.

package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestUserCacheRoot and TestUserCacheDefault: the CLI's cache is MK_CACHE,
// else one cache per user, %LOCALAPPDATA%\mk\cache on Windows and
// $XDG_CACHE_HOME/mk, else ~/.cache/mk, elsewhere. Every variable it reads
// points into the test's own directories; TestMain (testmain_test.go) sets
// MK_CACHE for every other test of the package.

func TestUserCacheRoot(t *testing.T) {
	env := func(kv ...string) func(string) string {
		return func(k string) string {
			for i := 0; i+1 < len(kv); i += 2 {
				if kv[i] == k {
					return kv[i+1]
				}
			}
			return ""
		}
	}
	abs := filepath.Join(t.TempDir(), "x")
	for _, tc := range []struct {
		goos string
		get  func(string) string
		want string
	}{
		{"windows", env("LOCALAPPDATA", abs, "XDG_CACHE_HOME", "/xdg"), filepath.Join(abs, "mk", "cache")},
		{"windows", env("HOME", abs), ""},
		{"linux", env("XDG_CACHE_HOME", abs, "HOME", "/home/u"), filepath.Join(abs, "mk")},
		{"linux", env("XDG_CACHE_HOME", "rel", "HOME", abs), filepath.Join(abs, ".cache", "mk")},
		{"linux", env("HOME", abs, "LOCALAPPDATA", "/lad"), filepath.Join(abs, ".cache", "mk")},
		{"linux", env(), ""},
	} {
		if got := userCacheRoot(tc.goos, tc.get); got != tc.want {
			t.Errorf("%s: %q, want %q", tc.goos, got, tc.want)
		}
	}
}

// mk check with MK_CACHE unset writes the per-user cache and no
// <package>/.mk-cache; MK_CACHE overrides it; mk cache size reads the
// same directory a build wrote.
func TestUserCacheDefault(t *testing.T) {
	home := t.TempDir()
	t.Setenv("LOCALAPPDATA", filepath.Join(home, "lad"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, "xdg"))
	t.Setenv("HOME", home)
	t.Setenv("MK_CACHE", "")
	want := filepath.Join(home, "xdg", "mk")
	if runtime.GOOS == "windows" {
		want = filepath.Join(home, "lad", "mk", "cache")
	}
	root := writeFiles(t, map[string]string{"main.mk": "export Main(sys: &System): int {\n    return 0\n}\n"})
	if st, _, errOut := mk("check", root); st != 0 {
		t.Fatalf("mk check: %d %s", st, errOut)
	}
	if _, err := os.Stat(filepath.Join(want, "v1")); err != nil {
		t.Errorf("no store in the per-user cache %s: %v", want, err)
	}
	if _, err := os.Stat(filepath.Join(root, ".mk-cache")); !os.IsNotExist(err) {
		t.Errorf("mk check wrote %s/.mk-cache: %v", root, err)
	}
	if st, out, _ := mk("cache", "size", root); st != 0 || !strings.Contains(out, "total") {
		t.Errorf("mk cache size: %d %q", st, out)
	}
	other := filepath.Join(home, "other")
	t.Setenv("MK_CACHE", other)
	if st, _, errOut := mk("check", root); st != 0 {
		t.Fatalf("mk check with MK_CACHE: %d %s", st, errOut)
	}
	if _, err := os.Stat(filepath.Join(other, "v1")); err != nil {
		t.Errorf("MK_CACHE not used: %v", err)
	}
	if got := userCacheDir(); got != other {
		t.Errorf("userCacheDir %q, want MK_CACHE %q", got, other)
	}
}
