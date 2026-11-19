// Why: std.big is optional and safe: no other std module may depend on it,
// and it must hold no unsafe code.
// What: TestSTDBIG_NoOtherStdModuleImportsBig and TestSTDBIG_OpenSetAndNoUnsafe.
// How: scans std's .mk files: no other module imports std.big; std/big holds
// no unsafe block and imports only std.raw; only big.Error is written open.

package std

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var bigImport = regexp.MustCompile(`(?m)^\s*import\s+(\w+\s*=\s*)?std\.big\s*$`)

// TestSTDBIG_NoOtherStdModuleImportsBig: std.big is imported by no other
// std module (RSA verification is std.crypto's own arithmetic).
func TestSTDBIG_NoOtherStdModuleImportsBig(t *testing.T) {
	var bad []string
	err := fs.WalkDir(os.DirFS("."), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".mk") || strings.HasPrefix(p, "big/") || strings.HasPrefix(p, "corpus/") {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if bigImport.Match(b) {
			bad = append(bad, p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(bad) > 0 {
		t.Errorf("std modules importing std.big (no std module may): %v", bad)
	}
}

// TestSTDBIG_OpenSetAndNoUnsafe: big.Error is written open
// and nothing else in std/big is; std/big has no unsafe block and imports only
// std.raw's safe intrinsics (it is not in unsafe_modules).
func TestSTDBIG_OpenSetAndNoUnsafe(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("big", "*.mk"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no std/big files: %v", err)
	}
	open := 0
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n")
		for i, l := range lines {
			s := strings.TrimSpace(l)
			if strings.HasPrefix(s, "export open ") {
				open++
				if s != "export open type Error = ParseError | TooLarge | OutOfMemory" {
					t.Errorf("%s:%d: %q is written open, want only big.Error", f, i+1, s)
				}
			}
			if strings.HasPrefix(s, "unsafe ") || strings.Contains(s, " unsafe \"") {
				t.Errorf("%s:%d: unsafe in std/big: %s", f, i+1, s)
			}
			if strings.HasPrefix(s, "import ") && s != "import std.raw" {
				t.Errorf("%s:%d: unexpected import %s", f, i+1, s)
			}
		}
	}
	if open != 1 {
		t.Errorf("found %d declarations written open in std/big, want 1 (big.Error)", open)
	}
}
