// Why: an HTTP client must tell running out of memory from a corrupt body.
// What: compress.Content's wrap fails with OutOfMemory when the arena cannot give the decoder's buffers,
// instead of returning a Reader whose first read fails with ReadError.
// How: a std copy with an impossible input buffer size, against the live std as the control.

package std

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// c2WrapOOMProg wraps stdin in the Content named by args[1] and prints
// "wrap oom" when wrap fails, else the first read's answer ("read <n>" or
// "readerror").
const c2WrapOOMProg = `import std.compress

Try(a: &Arena into a, c: compress.Content, src: mut Reader, out: mut Writer): WriteError!void {
    var rb: [512]u8 = [0; 512]
    var r := c.wrap(a, src, mut rb[..]) else {
        try Print(out, "wrap oom\n")
        return
    }
    var buf: [64]u8 = [0; 64]
    got := r.Read(mut buf[..]) else {
        try Print(out, "readerror\n")
        return
    }
    try Print(out, "read {got}\n", (got: got))
}

export Main(sys: &System): WriteError|OutOfMemory!void {
    var out := sys.Stdout()
    var inp := sys.Stdin()
    c := if sys.args[1] == "gzip" { compress.GzipContent } else { compress.DeflateContent }
    var a := try Arena.New(sys.heap)
    defer a.Free()
    try Try(&a, c, mut inp, mut out)
    try out.Flush()
}
`

// c2PatchStd replaces from with to, once, in std/compress/<file> of the std
// copy stdDir; the test fails when from is not there.
func c2PatchStd(t *testing.T, stdDir, file, from, to string) {
	t.Helper()
	p := filepath.Join(stdDir, "compress", file)
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(b, []byte(from)) {
		t.Fatalf("%s has no %q", file, from)
	}
	if err := os.WriteFile(p, bytes.Replace(b, []byte(from), []byte(to), 1), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestCompress_ContentWrapOOM: in a std copy whose InCap is 2^62
// (the window is given, the input buffer cannot be),
// GzipContent's and DeflateContent's wrap fail with OutOfMemory; on the
// live std the same program reads the first decoded bytes.
func TestCompress_ContentWrapOOM(t *testing.T) {
	live := c2Exe(t, "wrapoom", c2WrapOOMProg)
	dir := t.TempDir()
	stdDir := czStdCopy(t, dir)
	c2PatchStd(t, stdDir, "inflater.mk", "const InCap = 16384", "const InCap = 4611686018427387904")
	patched := czBuildStd(t, dir, stdDir, c2WrapOOMProg)
	in := []byte("hello, hello, hello")
	for _, c := range []struct {
		name string
		data []byte
	}{{"gzip", czGzipGo(t, in, 6)}, {"deflate", c2ZlibGo(t, in, 6)}} {
		if got := runNative(t, live, string(c.data), c.name); got.stdout != "read 19\n" || got.code != 0 {
			t.Errorf("%s, live std: got %q (exit %d, stderr %s), want %q", c.name, got.stdout, got.code, got.stderr, "read 19\n")
		}
		if got := runNative(t, patched, string(c.data), c.name); got.stdout != "wrap oom\n" || got.code != 0 {
			t.Errorf("%s, no input buffer: got %q (exit %d, stderr %s), want %q", c.name, got.stdout, got.code, got.stderr, "wrap oom\n")
		}
	}
}
