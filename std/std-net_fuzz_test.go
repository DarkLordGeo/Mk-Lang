// Why: net.ParseAddr is std.net's one parser of outside bytes, so it must
// survive any input and agree with Go.
// What: FuzzStdNet_ParseAddr.
// How: runs std-net2's differential driver on each line and compares it with
// Go's netip.ParseAddrPort and AddrPort.String (net2Want).
// Its fuzz targets read untrusted input; tools/gate/safety.go lists each untrusted-input target.

package std

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"
)

var net3FuzzBuilt struct {
	sync.Mutex
	exe string
}

// net3FuzzExe builds the release driver once per test binary, in a
// package temporary directory that TestMain removes.
func net3FuzzExe(t *testing.T) string {
	t.Helper()
	net3FuzzBuilt.Lock()
	defer net3FuzzBuilt.Unlock()
	if net3FuzzBuilt.exe != "" {
		return net3FuzzBuilt.exe
	}
	root, err := packageTempDir("mknet3fuzz")
	if err != nil {
		t.Fatal(err)
	}
	stdDir := copyStd(t, root, nil)
	if err := os.WriteFile(filepath.Join(root, "main.mk"), []byte(net2DiffMain), 0o644); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(root, "parse")
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	out, status := mkCmd(t, root, stdDir, "build", "--release", "-o", exe, "main.mk")
	if status != 0 {
		if strings.Contains(out, "no usable LLVM") {
			t.Skipf("needs the LLVM toolchain: %s", out)
		}
		t.Fatalf("mk build: exit %d\n%s", status, out)
	}
	net3FuzzBuilt.exe = exe
	return exe
}

// FuzzStdNet_ParseAddr: a panic, a hang (runNative's bound) or a different
// answer fails. The seeds are a sample of std-net2's inputs, so go test runs
// them; go test -fuzz FuzzStdNet_ParseAddr -parallel 2 explores.
func FuzzStdNet_ParseAddr(f *testing.F) {
	for i, s := range net2Inputs() {
		if i%223 == 0 {
			f.Add(s)
		}
	}
	for _, s := range []string{"", ":", "[]:0", "[::]:65535", "[::ffff:1.2.3.4]:1", "1.2.3.4:65536", "[fe80::1%eth0]:80", "0.0.0.0:00", "[1:2:3:4:5:6:7:8:9]:1"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		if len(s) > 256 || !utf8.ValidString(s) || strings.ContainsRune(s, '\n') {
			return
		}
		exe := net3FuzzExe(t)
		in := filepath.Join(t.TempDir(), "in.txt")
		if err := os.WriteFile(in, []byte(s+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		r := runNative(t, exe, "", in)
		got := strings.TrimSuffix(strings.ReplaceAll(r.stdout, "\r\n", "\n"), "\n")
		if want := net2Want(s); r.code != 0 || got != want {
			t.Fatalf("%q: got %q (exit %d, stderr %s), want %q", s, got, r.code, r.stderr, want)
		}
	})
}
