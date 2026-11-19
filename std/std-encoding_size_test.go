// Why: a program links only the tables of the encodings it names; a stray reference would pull them all in.
// What: release programs that decode only windows-1252 or only UTF-8 hold no row of utf16.mk's PackShuf
// table, and one that decodes UTF-16LE (the positive control) does.
// How: builds each program and searches the executable's bytes for encPackRows.

package std

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// encPackRows is rows 1 and 2 of utf16.mk's PackShuf, computed by its
// rule: for mask m, lane j gives 1 + bit j + bit j+4 bytes, taken from
// 4j, 4j+1, ...; the rest of the 16 is 16.
func encPackRows() []byte {
	var out []byte
	for m := 1; m <= 2; m++ {
		var row []byte
		for j := 0; j < 4; j++ {
			n := 1 + (m>>j)&1 + (m>>(j+4))&1
			for k := 0; k < n; k++ {
				row = append(row, byte(4*j+k))
			}
		}
		for len(row) < 16 {
			row = append(row, 16)
		}
		out = append(out, row...)
	}
	return out
}

func TestStdEncoding_LinksOnlyNamed(t *testing.T) {
	if testing.Short() {
		t.Skip("builds three release programs")
	}
	needle := encPackRows()
	root := t.TempDir()
	stdDir := encStdDir(t, root)
	t.Setenv("MK_CACHE", filepath.Join(root, "cache"))
	for _, c := range []struct {
		name string
		call string
		want bool
	}{
		{"w1252", "encoding.Windows1252.Decode", false},
		{"utf8", "encoding.Utf8.Decode", false},
		{"utf16le", "encoding.Utf16Le.Decode", true},
	} {
		dir := filepath.Join(root, c.name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		src := strings.ReplaceAll(`import std.encoding

export Main(sys: &System): OutOfMemory|WriteError!void {
    n := (try CALL(sys.arena, sys.args[0].AsBytes())).Len()
    var w := sys.Stdout()
    try Print(mut w, "{n}\n", (n))
}
`, "CALL", c.call)
		if err := os.WriteFile(filepath.Join(dir, "main.mk"), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
		exe := filepath.Join(dir, "main")
		if runtime.GOOS == "windows" {
			exe += ".exe"
		}
		out, status := mkCmd(t, dir, stdDir, "build", "--release", "-o", exe, "main.mk")
		if status != 0 {
			if strings.Contains(out, "no usable LLVM") {
				t.Skipf("needs the LLVM toolchain: %s", out)
			}
			t.Fatalf("%s: mk build exit %d\n%s", c.name, status, out)
		}
		bin, err := os.ReadFile(exe)
		if err != nil {
			t.Fatal(err)
		}
		if got := bytes.Contains(bin, needle); got != c.want {
			t.Errorf("%s (%d bytes): PackShuf's rows present %v, want %v", c.name, len(bin), got, c.want)
		}
	}
}
