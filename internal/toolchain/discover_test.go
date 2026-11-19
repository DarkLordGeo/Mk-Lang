// Why: the compiler must find one complete LLVM toolchain, in a fixed and
// reported order, and never mix tools from two directories.
// What: tests of Discover's order, complete-directory preference, errors,
// the version floor, Identity, triple parsing and Describe.
// How: builds fake tool directories (fakeDir) of empty files and points
// --llvm, MK_LLVM, the default locations and PATH at them.

package toolchain

import (
	"errors"
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// Accepted so that go test ./... -update works in every package with tests;
// this package has no golden files.
var _ = flag.Bool("update", false, "no golden files in this package")

// fakeDir makes a directory holding empty files with the given tool names
// (the executable suffix for goos added).
func fakeDir(t *testing.T, goos string, names ...string) string {
	t.Helper()
	d := t.TempDir()
	for _, n := range names {
		if goos == "windows" {
			n += ".exe"
		}
		if err := os.WriteFile(filepath.Join(d, n), []byte("fake"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return d
}

func fakeOpts(goos string, env map[string]string, defaults []string) Options {
	return Options{
		GOOS:     goos,
		Getenv:   func(k string) string { return env[k] },
		LookPath: func(string) (string, error) { return "", errors.New("no LookPath in tests") },
		Defaults: defaults,
		VersionLine: func(string) (string, error) {
			return "clang version 21.1.8\nTarget: x86_64-pc-windows-msvc\n", nil
		},
		// No bundle beside a test binary (TestDiscoverBundleOrder sets one).
		Executable: func() (string, error) { return "", errors.New("no executable in tests") },
	}
}

var allTools = []string{NameClang, NameLLDLink, NameLDLLD, NameLLVMLib}

// TestDiscoverOrder: --llvm beats MK_LLVM, which beats the default location,
// which beats PATH; tools are never mixed across directories.
func TestDiscoverOrder(t *testing.T) {
	for _, goos := range []string{"windows", "linux"} {
		t.Run(goos, func(t *testing.T) {
			flagD := fakeDir(t, goos, allTools...)
			envD := fakeDir(t, goos, allTools...)
			defD := fakeDir(t, goos, allTools...)
			pathD := fakeDir(t, goos, allTools...)
			sep := ":"
			if goos == "windows" {
				sep = ";"
			}
			env := map[string]string{"MK_LLVM": envD, "PATH": pathD}
			cases := []struct {
				name    string
				flagDir string
				env     map[string]string
				defs    []string
				wantDir string
				wantSrc Source
			}{
				{"flag", flagD, env, []string{defD}, flagD, FlagDir},
				{"env", "", env, []string{defD}, envD, EnvDir},
				{"default", "", map[string]string{"PATH": pathD}, []string{defD}, defD, DefaultDir},
				{"path", "", map[string]string{"PATH": "" + sep + pathD}, []string{}, pathD, PathDir},
				{"env-without-clang-falls-through", "", map[string]string{"MK_LLVM": t.TempDir(), "PATH": pathD}, []string{defD}, defD, DefaultDir},
			}
			for _, c := range cases {
				if c.wantSrc == PathDir && strings.Contains(pathD, sep) {
					continue // a Windows drive path cannot sit in a ':' list
				}
				o := fakeOpts(goos, c.env, c.defs)
				o.LLVMDir = c.flagDir
				tl, err := Discover(o)
				if err != nil {
					t.Fatalf("%s: %v", c.name, err)
				}
				if tl.Dir != c.wantDir || tl.Found != c.wantSrc {
					t.Errorf("%s: got %s (%s), want %s (%s)", c.name, tl.Dir, tl.Found, c.wantDir, c.wantSrc)
				}
				for _, p := range []string{tl.Clang, tl.LLDLink, tl.LDLLD, tl.LLVMLib} {
					if filepath.Dir(p) != c.wantDir {
						t.Errorf("%s: tool %s not from %s", c.name, p, c.wantDir)
					}
				}
				if tl.Version.Major != 21 {
					t.Errorf("%s: version %v", c.name, tl.Version)
				}
			}
		})
	}
	// Tools are never mixed: a chosen directory without lld-link leaves it
	// "", even when PATH has one elsewhere.
	only := fakeDir(t, "windows", NameClang)
	full := fakeDir(t, "windows", allTools...)
	tl, err := Discover(fakeOpts("windows", map[string]string{"MK_LLVM": only, "PATH": full}, []string{}))
	if err != nil {
		t.Fatal(err)
	}
	if tl.Dir != only || tl.LLDLink != "" || tl.LLVMLib != "" || tl.LDLLD != "" {
		t.Errorf("mixed directories: %+v", tl)
	}
	if got := tl.Need("x86_64-pc-windows-msvc"); !reflect.DeepEqual(got, []string{NameLLDLink}) {
		t.Errorf("Need windows = %v", got)
	}
	if got := tl.Need("x86_64-unknown-linux-gnu"); !reflect.DeepEqual(got, []string{NameLDLLD}) {
		t.Errorf("Need linux = %v", got)
	}
}

// TestDiscoverPrefersCompleteDir: within PATH (and within the default
// locations), the first directory that also holds the host linker (named,
// or lld) beats an earlier one holding clang alone (a MinGW clang on PATH,
// for example). llvm-lib is not needed: the linker's /lib mode writes the
// same import libraries.
func TestDiscoverPrefersCompleteDir(t *testing.T) {
	win1 := fakeDir(t, "windows", NameClang, NameLLVMLib) // no linker
	win2 := fakeDir(t, "windows", allTools...)
	tl, err := Discover(fakeOpts("windows", map[string]string{"PATH": win1 + ";" + win2}, []string{}))
	if err != nil {
		t.Fatal(err)
	}
	if tl.Dir != win2 || tl.Found != PathDir {
		t.Errorf("windows PATH: got %s, want %s", tl.Dir, win2)
	}
	// Only incomplete directories: the first holding clang.
	tl, err = Discover(fakeOpts("windows", map[string]string{"PATH": win1}, []string{}))
	if err != nil || tl.Dir != win1 {
		t.Errorf("incomplete only: %v %v", tl, err)
	}
	// A directory with clang and lld alone is complete.
	winLLD := fakeDir(t, "windows", NameClang, NameLLD)
	if tl, err = Discover(fakeOpts("windows", map[string]string{"PATH": win1 + ";" + winLLD}, []string{})); err != nil || tl.Dir != winLLD {
		t.Errorf("windows lld only: %v %v", tl, err)
	}
	lin1 := fakeDir(t, "linux", NameClang) // /usr/bin in WSL: clang, no ld.lld
	lin2 := fakeDir(t, "linux", NameClang, NameLDLLD, NameLLDLink, NameLLVMLib)
	tl, err = Discover(fakeOpts("linux", map[string]string{}, []string{filepath.Join(t.TempDir(), "llvm-21"), lin1, lin2}))
	if err != nil {
		t.Fatal(err)
	}
	if tl.Dir != lin2 || tl.Found != DefaultDir {
		t.Errorf("linux defaults: got %s, want %s", tl.Dir, lin2)
	}
	if got := DefaultDirs("linux"); !reflect.DeepEqual(got, []string{"/usr/lib/llvm-21/bin", "/usr/local/bin", "/usr/bin"}) {
		t.Errorf("DefaultDirs(linux) = %v", got)
	}
	if got := DefaultDirs("windows"); !reflect.DeepEqual(got, []string{`C:\Program Files\LLVM\bin`}) {
		t.Errorf("DefaultDirs(windows) = %v", got)
	}
}

// TestDiscoverMissingNamesSearched: the error names every directory in
// search order and the missing tool.
func TestDiscoverMissingNamesSearched(t *testing.T) {
	a, b, c := t.TempDir(), t.TempDir(), t.TempDir()
	_, err := Discover(fakeOpts("windows", map[string]string{"MK_LLVM": a, "PATH": c}, []string{b}))
	var me *MissingError
	if !errors.As(err, &me) {
		t.Fatalf("err = %v, want *MissingError", err)
	}
	if !reflect.DeepEqual(me.Searched, []string{a, b, c}) || !reflect.DeepEqual(me.Missing, []string{NameClang}) {
		t.Errorf("got %+v", me)
	}
	if !strings.Contains(err.Error(), a) || !strings.Contains(err.Error(), c) {
		t.Errorf("message does not name the directories: %v", err)
	}
	// --llvm without clang is an error even when PATH would have one.
	full := fakeDir(t, "windows", allTools...)
	o := fakeOpts("windows", map[string]string{"PATH": full}, []string{})
	o.LLVMDir = a
	_, err = Discover(o)
	if !errors.As(err, &me) || !reflect.DeepEqual(me.Searched, []string{a}) {
		t.Errorf("--llvm without clang: %v", err)
	}
}

func TestVersionParse(t *testing.T) {
	cases := []struct {
		in   string
		want Version
	}{
		{"clang version 21.1.8\nTarget: x86_64-pc-windows-msvc\nThread model: posix\n",
			Version{21, 1, 8, "clang version 21.1.8"}},
		{"Ubuntu clang version 18.1.3 (1ubuntu1)\r\nTarget: x86_64-pc-linux-gnu\n",
			Version{18, 1, 3, "Ubuntu clang version 18.1.3 (1ubuntu1)"}},
		{"clang version 19.0.0git (https://github.com/llvm/llvm-project abc)",
			Version{19, 0, 0, "clang version 19.0.0git (https://github.com/llvm/llvm-project abc)"}},
		{"Apple clang version 17", Version{17, 0, 0, "Apple clang version 17"}},
	}
	for _, c := range cases {
		got, err := ParseVersion(c.in)
		if err != nil || got != c.want {
			t.Errorf("ParseVersion(%q) = %+v, %v; want %+v", c.in, got, err, c.want)
		}
	}
	for _, bad := range []string{"", "gcc (GCC) 13.2", "clang version x"} {
		if _, err := ParseVersion(bad); err == nil {
			t.Errorf("ParseVersion(%q) accepted", bad)
		}
	}
	if s := (Version{21, 1, 8, ""}).String(); s != "21.1.8" {
		t.Errorf("String = %q", s)
	}
}

// TestVersionBelow21Refused: the LLVM floor is 21; 20 is refused,
// 21 is accepted.
func TestVersionBelow21Refused(t *testing.T) {
	d := fakeDir(t, "linux", NameClang, NameLDLLD)
	o := fakeOpts("linux", map[string]string{}, []string{d})
	o.VersionLine = func(string) (string, error) { return "Ubuntu clang version 20.1.2 (0ubuntu1)\n", nil }
	_, err := Discover(o)
	var ve *VersionError
	if !errors.As(err, &ve) || ve.Version.Major != 20 || ve.Path != filepath.Join(d, NameClang) {
		t.Fatalf("err = %v, want *VersionError for 20", err)
	}
	if !strings.Contains(err.Error(), "LLVM 21 or newer") {
		t.Errorf("message: %v", err)
	}
	o.VersionLine = func(string) (string, error) { return "clang version 21.1.8\n", nil }
	if _, err := Discover(o); err != nil {
		t.Errorf("21 refused: %v", err)
	}
}

// TestIdentity: the identity changes with any path, size or version line.
func TestIdentity(t *testing.T) {
	d := fakeDir(t, "windows", allTools...)
	o := fakeOpts("windows", map[string]string{"MK_LLVM": d}, []string{})
	a, err := Discover(o)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := Discover(o)
	if a.Identity() != b.Identity() {
		t.Fatal("identity not stable")
	}
	o.VersionLine = func(string) (string, error) { return "clang version 21.1.9\n", nil }
	c, _ := Discover(o)
	if c.Identity() == a.Identity() {
		t.Error("identity ignores the version line")
	}
	if err := os.WriteFile(filepath.Join(d, "lld-link.exe"), []byte("bigger fake"), 0o755); err != nil {
		t.Fatal(err)
	}
	e, _ := Discover(fakeOpts("windows", map[string]string{"MK_LLVM": d}, []string{}))
	if e.Identity() == a.Identity() {
		t.Error("identity ignores a tool's size")
	}
}

func TestTripleParts(t *testing.T) {
	cases := []struct{ triple, os, arch string }{
		{"x86_64-pc-windows-msvc", "windows", "amd64"},
		{"x86_64-pc-windows-msvc19.33.0", "windows", "amd64"},
		{"x86_64-unknown-linux-gnu", "linux", "amd64"},
		{"aarch64-unknown-linux-gnu", "linux", "arm64"},
		{"arm64-apple-macosx14.0.0", "darwin", "arm64"},
		{"wasm32-unknown-unknown", "", ""},
	}
	for _, c := range cases {
		if OSOf(c.triple) != c.os || ArchOf(c.triple) != c.arch {
			t.Errorf("%s: os %q arch %q", c.triple, OSOf(c.triple), ArchOf(c.triple))
		}
	}
}

// TestDescribe: the report names the rule, the directory, every tool and an
// absent one as such: the choice is never silent.
func TestDescribe(t *testing.T) {
	d := fakeDir(t, "windows", NameClang, NameLLDLink, NameLLVMLib)
	tl, err := Discover(fakeOpts("windows", map[string]string{"MK_LLVM": d}, []string{}))
	if err != nil {
		t.Fatal(err)
	}
	s := tl.Describe()
	for _, want := range []string{"LLVM 21.1.8", d, "MK_LLVM", filepath.Join(d, "clang.exe"), "ld.lld   (absent)"} {
		if !strings.Contains(s, want) {
			t.Errorf("Describe lacks %q:\n%s", want, s)
		}
	}
}
