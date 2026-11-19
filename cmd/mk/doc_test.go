// Why: mk doc is how users read an API and check its examples, from any
// directory and for std too.
// What: tests of mk doc --check (each /// block judged by its verdict) and
// of mk doc --std, in its standalone and --embed forms.
// How: runs the command in-process over small packages in temporary
// directories.

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDocCheck(t *testing.T) {
	dir := t.TempDir()
	src := "/// ```mk\n/// // verdict: ok\n/// DocUse(): int {\n///     return 2\n/// }\n/// ```\nexport F(): int {\n    return 1\n}\n\n" +
		"/// ```mk\n/// // verdict: reject E0301\n/// DocBad(): int {\n///     return \"s\" // <- E0301\n/// }\n/// ```\nexport G(): int {\n    return 0\n}\n\nexport Main() {\n}\n"
	if err := os.WriteFile(filepath.Join(dir, "main.mk"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	var so, se bytes.Buffer
	if st := run([]string{"doc", "--check", dir}, &so, &se); st != 0 || !strings.Contains(so.String(), "PASS ") || !strings.Contains(so.String(), "2 doc blocks, 0 failed") {
		t.Fatalf("status %d\n%s\n%s", st, so.String(), se.String())
	}
	if !strings.Contains(so.String(), "main.mk:2") || !strings.Contains(so.String(), "main.mk:12") {
		t.Errorf("block keys are not the file's lines:\n%s", so.String())
	}
	// A block whose verdict no longer holds fails.
	bad := strings.Replace(src, "return 2\n", "return \"two\"\n", 1)
	if err := os.WriteFile(filepath.Join(dir, "main.mk"), []byte(bad), 0o644); err != nil {
		t.Fatal(err)
	}
	so.Reset()
	if st := run([]string{"doc", "--check", dir}, &so, &se); st != 1 || !strings.Contains(so.String(), "FAIL ") {
		t.Errorf("a broken ok block: status %d\n%s", st, so.String())
	}
}

// TestDocStdNeedsNoPackage: mk doc --std documents std from anywhere, the
// prelude's names in std.core included.
func TestDocStdNeedsNoPackage(t *testing.T) {
	var so, se bytes.Buffer
	if st := run([]string{"doc", "--std", "std.core"}, &so, &se); st != 0 {
		t.Fatalf("status %d\n%s", st, se.String())
	}
	for _, w := range []string{"## Module `std.core`", "### `List`", "### `Arena`", "### `Arch`"} {
		if !strings.Contains(so.String(), w) {
			t.Errorf("mk doc --std std.core lacks %q", w)
		}
	}
	if st := run([]string{"doc", "--std", "std.core", "."}, &so, &se); st != 2 {
		t.Errorf("--std with a path: status %d", st)
	}
}

// TestDocEmbed: --embed writes a module's API for a page that has its own
// title: no top-level heading, no module heading, a stability line, items
// at ###. It takes no --html. --std std.raw documents the std-only module.
func TestDocEmbed(t *testing.T) {
	var so, se bytes.Buffer
	if st := run([]string{"doc", "--std", "std.testing", "--embed"}, &so, &se); st != 0 {
		t.Fatalf("status %d\n%s", st, se.String())
	}
	out := so.String()
	if !strings.HasPrefix(out, "Stability: Stable.\n\n### `Bench`") || strings.Contains(out, "# std") || strings.Contains(out, "## Module") {
		t.Errorf("mk doc --embed:\n%s", out)
	}
	if st := run([]string{"doc", "--std", "std.testing", "--embed", "--html"}, &so, &se); st != 2 {
		t.Errorf("--embed with --html: status %d", st)
	}
	so.Reset()
	if st := run([]string{"doc", "--std", "std.raw"}, &so, &se); st != 0 || !strings.Contains(so.String(), "## Module `std.raw`") {
		t.Errorf("mk doc --std std.raw: status %d\n%.300s", st, so.String())
	}
}

// TestDocCheckPerBlock: a block with no verdict line is one failure and the
// others still run; a block that declares Use, as the test prelude does,
// keeps its own; a failing block's diagnostics carry the file's line
// numbers, not the block's.
func TestDocCheckPerBlock(t *testing.T) {
	dir := t.TempDir()
	lines := []string{
		"/// ```mk",
		"/// UseIt(): int {",
		"///     return 1",
		"/// }",
		"/// ```",
		"export F(): int {",
		"    return 1",
		"}",
		"",
		"/// ```mk",
		"/// // verdict: ok",
		"/// Use(x: int): int {",
		"///     return x",
		"/// }",
		"/// ```",
		"export G(): int {",
		"    return 0",
		"}",
		"",
		"/// ```mk",
		"/// // verdict: ok",
		"/// Bad(): int {",
		"///     return \"two\"",
		"/// }",
		"/// ```",
		"export H(): int {",
		"    return 0",
		"}",
		"",
		"export Main() {",
		"}",
	}
	if err := os.WriteFile(filepath.Join(dir, "main.mk"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var so, se bytes.Buffer
	st := run([]string{"doc", "--check", dir}, &so, &se)
	out := so.String()
	if st != 1 || !strings.Contains(out, "3 doc blocks, 2 failed") {
		t.Fatalf("status %d\n%s\n%s", st, out, se.String())
	}
	if !strings.Contains(out, "PASS ") || !strings.Contains(out, "main.mk:11") {
		t.Errorf("the Use block did not pass:\n%s", out)
	}
	// "two" is on the file's line 23.
	if !strings.Contains(out, "main.mk:23 E0301") {
		t.Errorf("the failing block's diagnostic is not at the file's line 23:\n%s", out)
	}
}
