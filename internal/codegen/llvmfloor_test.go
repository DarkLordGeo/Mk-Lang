// Why: Mk's IR needs LLVM 21, and an older clang found first on PATH must be
// refused with a clear error, not fail later on the IR.
// What: TestLLVMFloor21: toolchain.MinMajor is 21, so on Linux only
// /usr/lib/llvm-21/bin is searched before /usr/local/bin and /usr/bin, and a
// clang of 18 to 20 is a *VersionError (E0001 unsupported-version).
// How: calls internal/toolchain/version.go directly; codegen may import
// toolchain in tests only.

package codegen

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"mk/internal/toolchain"
)

func TestLLVMFloor21(t *testing.T) {
	if toolchain.MinMajor != 21 {
		t.Fatalf("MinMajor = %d, want 21", toolchain.MinMajor)
	}
	if got, want := toolchain.DefaultDirs("linux"), []string{"/usr/lib/llvm-21/bin", "/usr/local/bin", "/usr/bin"}; !reflect.DeepEqual(got, want) {
		t.Errorf("DefaultDirs(linux) = %v, want %v", got, want)
	}
	for _, c := range []struct {
		line string
		ok   bool
	}{
		{"Ubuntu clang version 18.1.3 (1ubuntu1)", false}, {"clang version 19.1.7", false},
		{"clang version 20.1.0", false}, {"clang version 21.1.8", true}, {"clang version 22.0.0git", true},
	} {
		v, err := toolchain.ParseVersion(c.line)
		if err != nil {
			t.Fatalf("ParseVersion(%q): %v", c.line, err)
		}
		if v.Supported() != c.ok {
			t.Errorf("%q: Supported() = %v, want %v", c.line, v.Supported(), c.ok)
		}
	}
	// Discovery refuses a complete directory whose clang is 20, and takes
	// the same directory at 21.
	dir := t.TempDir()
	for _, n := range []string{toolchain.NameClang, toolchain.NameLDLLD} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("fake"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	opts := func(line string) toolchain.Options {
		return toolchain.Options{GOOS: "linux", Getenv: func(string) string { return "" },
			LookPath: func(string) (string, error) { return "", errors.New("no LookPath in tests") },
			Defaults: []string{dir}, VersionLine: func(string) (string, error) { return line + "\n", nil }}
	}
	_, err := toolchain.Discover(opts("clang version 20.1.0"))
	var ve *toolchain.VersionError
	if !errors.As(err, &ve) || ve.Version.Major != 20 {
		t.Fatalf("clang 20: err = %v, want *VersionError for 20", err)
	}
	if !strings.Contains(err.Error(), "LLVM 21 or newer") {
		t.Errorf("clang 20: message %q does not name the floor", err)
	}
	if tl, err := toolchain.Discover(opts("clang version 21.1.8")); err != nil || tl.Dir != dir {
		t.Errorf("clang 21 refused: %v %v", tl, err)
	}
}
