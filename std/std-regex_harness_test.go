// Why: the std.regex tests all build and run Mk programs and compare them
// with Go's regexp; this file holds what they share.
// What: regexStdCopy, program build and run helpers, Mk literal quoting.
// How: a copy of std holding this tree's std/regex (or MK_REGEX_BASE's
// snapshot); output compared line by line with Go's regexp.

package std

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// regexStdCopy writes the std a test builds against under root and returns
// its directory. With MK_REGEX_BASE set (a snapshot of HEAD's std, for
// when other std files are mid-edit) it is that
// snapshot with std/regex replaced by this tree's; else the live std.
func regexStdCopy(t *testing.T, root string) string {
	t.Helper()
	base := os.Getenv("MK_REGEX_BASE")
	if base == "" {
		return copyStd(t, root, nil)
	}
	stdDir := filepath.Join(root, "std")
	if err := os.CopyFS(stdDir, os.DirFS(base)); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(stdDir, "regex")); err != nil {
		t.Fatal(err)
	}
	ents, err := os.ReadDir("regex")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(stdDir, "regex"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".mk") {
			continue
		}
		b, err := os.ReadFile(filepath.Join("regex", e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(stdDir, "regex", e.Name()), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return stdDir
}

// regexBuild builds src as main.mk with mk build and the given flags, and
// returns the executable. A missing LLVM skips the test.
func regexBuild(t *testing.T, src string, flags ...string) string {
	t.Helper()
	root := t.TempDir()
	stdDir := regexStdCopy(t, root)
	if err := os.WriteFile(filepath.Join(root, "main.mk"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(root, "main")
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	args := append([]string{"build"}, flags...)
	args = append(args, "-o", exe, "main.mk")
	out, status := mkCmd(t, root, stdDir, args...)
	if status != 0 {
		if strings.Contains(out, "no usable LLVM") {
			t.Skipf("needs the LLVM toolchain: %s", out)
		}
		t.Fatalf("mk build: exit %d\n%s", status, out)
	}
	return exe
}

// regexExec runs exe with args and returns its output with \r\n as \n.
func regexExec(t *testing.T, exe string, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	b, err := exec.CommandContext(ctx, exe, args...).CombinedOutput()
	out := strings.ReplaceAll(string(b), "\r\n", "\n")
	if err != nil {
		t.Fatalf("%s %v: %v\n%s", exe, args, err, out)
	}
	return out
}

// rxQuote is s as a Mk string literal: printable ASCII as itself, every
// other scalar as \u{H}.
func rxQuote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '"' || r == '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case r >= 0x20 && r < 0x7F:
			b.WriteRune(r)
		default:
			fmt.Fprintf(&b, "\\u{%x}", r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// rxStrs is a Mk static slice literal of strs.
func rxStrs(strs []string) string {
	var b strings.Builder
	b.WriteString("[\n")
	for _, s := range strs {
		b.WriteString("    ")
		b.WriteString(rxQuote(s))
		b.WriteString(",\n")
	}
	b.WriteString("]")
	return b.String()
}

// rxInts is a Mk static slice literal of ints.
func rxInts(xs []int) string {
	var b strings.Builder
	b.WriteString("[")
	for i, x := range xs {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "%d", x)
	}
	b.WriteString("]")
	return b.String()
}

// regexCompare reports the first differing lines of got and want; label
// names the case each line index belongs to.
func regexCompare(t *testing.T, got, want string, label func(line string) string) {
	t.Helper()
	g := strings.Split(strings.TrimSuffix(got, "\n"), "\n")
	w := strings.Split(strings.TrimSuffix(want, "\n"), "\n")
	bad := 0
	for i := 0; i < max(len(g), len(w)); i++ {
		var a, b string
		if i < len(g) {
			a = g[i]
		}
		if i < len(w) {
			b = w[i]
		}
		if a != b {
			t.Errorf("line %d (%s):\n got  %s\n want %s", i+1, label(b), a, b)
			if bad++; bad == 12 {
				t.Fatal("stopping after 12 differences")
			}
		}
	}
}
