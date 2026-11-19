// Why: Windows caps a command line near 32,767 characters, so long links go
// through a response file whose quoting LLVM must read back exactly.
// What: TestLinkResponseFileQuoting, TestLinkResponseFileForced and
// TestLinkResponseFileLongCommand.
// How: round-trips arguments through rspText, then links real programs with
// the response file forced and with a very long object list.

package toolchain

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestLinkResponseFileQuoting: each argument survives LLVM's two
// tokenizers' rules as rspText writes them.
func TestLinkResponseFileQuoting(t *testing.T) {
	for _, c := range [][2]string{
		{`C:\a\b.o`, `C:\a\b.o`},
		{`C:\a b\c.o`, `"C:\a b\c.o"`},
		{`/out:C:\x y\`, `"/out:C:\x y\\"`},
		{`a"b`, `"a\"b"`},
		{`a\"b`, `"a\\\"b"`},
		{``, `""`},
	} {
		if got := quoteWindows(c[0]); got != c[1] {
			t.Errorf("windows %q: got %s, want %s", c[0], got, c[1])
		}
	}
	for _, c := range [][2]string{
		{`/a/b.o`, `/a/b.o`},
		{`/a b/c.o`, `/a\ b/c.o`},
		{`C:\x\y.o`, `C:\\x\\y.o`},
		{`it's "q"`, `it\'s\ \"q\"`},
		{``, `""`},
	} {
		if got := quotePOSIX(c[0]); got != c[1] {
			t.Errorf("posix %q: got %s, want %s", c[0], got, c[1])
		}
	}
}

// TestLinkResponseFileForced: with the response file forced, the smoke
// program links on both flavours from a directory whose name holds a
// space, byte for byte as the plain command line links it, the response
// file is removed, and the Windows one runs.
func TestLinkResponseFileForced(t *testing.T) {
	tl := realTools(t)
	for _, c := range []struct{ triple, file string }{
		{"x86_64-pc-windows-msvc", "smoke_windows.ll"},
		{"x86_64-unknown-linux-gnu", "smoke_linux.ll"},
	} {
		t.Run(c.triple, func(t *testing.T) {
			if miss := tl.Need(c.triple); len(miss) > 0 {
				t.Skipf("SKIP: %s lacks %v", tl.Dir, miss)
			}
			for _, v := range []smokeVariant{{"O0", O0, false}, {"O3-thinlto", O3, true}} {
				t.Run(v.name, func(t *testing.T) {
					plainDir := filepath.Join(t.TempDir(), "plain dir")
					rspDir := filepath.Join(t.TempDir(), "rsp dir")
					for _, d := range []string{plainDir, rspDir} {
						if err := os.MkdirAll(d, 0o755); err != nil {
							t.Fatal(err)
						}
					}
					plain := smokeBuild(t, tl, c.triple, c.file, v, plainDir)
					old := responseFileAt
					responseFileAt = 0
					forced := smokeBuild(t, tl, c.triple, c.file, v, rspDir)
					responseFileAt = old
					if !sameBytes(t, plain, forced) {
						t.Error("the response-file link differs from the plain one")
					}
					ents, _ := os.ReadDir(rspDir)
					for _, e := range ents {
						if strings.HasSuffix(e.Name(), ".rsp") {
							t.Errorf("response file left: %s", e.Name())
						}
					}
					if strings.Contains(c.triple, "windows") && runtime.GOOS == "windows" {
						out, code, _ := smokeRun(t, c.triple, forced)
						if out != smokeOut || code != smokeExit {
							t.Errorf("ran: output %q exit %d", out, code)
						}
					}
				})
			}
		})
	}
}

// TestLinkResponseFileLongCommand: a link of more objects than Windows'
// command line holds (40,000 characters of object paths, the shape of a
// large generated program) reaches the linker, which then names a missing object,
// instead of failing to start.
func TestLinkResponseFileLongCommand(t *testing.T) {
	tl := realTools(t)
	dir := filepath.Join(t.TempDir(), "long dir")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	var objs []string
	for i := 0; len(strings.Join(objs, " ")) < 40000; i++ {
		objs = append(objs, filepath.Join(dir, "missing-object-with-a-long-name-"+strings.Repeat("x", 40)+itoa(i)+".o"))
	}
	for _, f := range []Format{COFF, ELF} {
		out := filepath.Join(dir, "a.exe")
		if f == ELF {
			out = filepath.Join(dir, "a")
		}
		err := tl.Link(context.Background(), LinkJob{Format: f, Output: out, Objects: objs})
		var te *ToolError
		if !errors.As(err, &te) {
			t.Fatalf("%v: %v, want a ToolError", f, err)
		}
		if !te.Started() || !strings.Contains(te.Stderr, "missing-object-with-a-long-name-") {
			t.Errorf("%v: started %v, stderr %q, err %v", f, te.Started(), firstLine(te.Stderr), te.Err)
		}
		if !strings.Contains(te.Command, filepath.Base(objs[len(objs)-1])) {
			t.Errorf("%v: the error's command does not list the objects", f)
		}
	}
}

func itoa(i int) string {
	const digits = "0123456789"
	if i < 10 {
		return digits[i : i+1]
	}
	return itoa(i/10) + digits[i%10:i%10+1]
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
