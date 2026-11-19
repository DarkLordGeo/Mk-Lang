// Why: the compress-gzip-stream bench row's checksum covers only the input, so a cell that wrote
// too little or wrong output would print the same line.
// What: runs the row's Mk, Go and (with zig on PATH) Zig programs over the real text into a file.
// How: reads every gzip member back with Go's compress/gzip, requires the input once per round, and
// logs each cell's compressed size.

package std

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// c2RowDir is the compress-gzip-stream row's directory.
const c2RowDir = "../tests/bench-lanes/std-compress2/gzip-stream"

// TestCompress_GzipStreamOutputs: each cell of compress-gzip-stream
// writes, for 2 rounds over the real text, two gzip members that Go's
// compress/gzip reads back to the text twice, and prints the row's
// checksum (the text's byte sum plus its length, once per round).
func TestCompress_GzipStreamOutputs(t *testing.T) {
	text := czRealInput(t)
	dir := t.TempDir()
	in := filepath.Join(dir, "in.txt")
	if err := os.WriteFile(in, text, 0o644); err != nil {
		t.Fatal(err)
	}
	var sum uint64
	for _, c := range text {
		sum += uint64(c)
	}
	sum += uint64(len(text))
	want := fmt.Sprint(2 * sum)

	src, err := os.ReadFile(filepath.Join(c2RowDir, "mk", "compress-gzip-stream.mk"))
	if err != nil {
		t.Fatal(err)
	}
	cells := map[string]string{"mk": c2Exe(t, "gzipstreamrow", string(src), "--release")}
	gobin := filepath.Join(runtime.GOROOT(), "bin", "go")
	goexe := filepath.Join(dir, "gocell.exe")
	if out, err := exec.Command(gobin, "build", "-o", goexe, filepath.Join(c2RowDir, "testdata", "compress-gzip-stream.go")).CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	cells["go"] = goexe
	if zig, err := exec.LookPath("zig"); err == nil {
		zexe := filepath.Join(dir, "zigcell.exe")
		zsrc, err := filepath.Abs(filepath.Join(c2RowDir, "compress-gzip-stream.zig"))
		if err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command(zig, "build-exe", "-O", "ReleaseFast", zsrc, "-femit-bin="+zexe)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("zig build-exe: %v\n%s", err, out)
		}
		cells["zig"] = zexe
	} else {
		t.Log("zig not on PATH: the Zig cell is not checked")
	}
	for _, name := range []string{"mk", "go", "zig"} {
		exe, ok := cells[name]
		if !ok {
			continue
		}
		outPath := filepath.Join(dir, name+".gz")
		out, err := exec.Command(exe, in, outPath, "2").Output()
		if err != nil {
			t.Fatalf("%s cell: %v", name, err)
		}
		if got := strings.TrimSpace(string(out)); got != want {
			t.Errorf("%s cell: checksum %s, want %s", name, got, want)
		}
		gz, err := os.ReadFile(outPath)
		if err != nil {
			t.Fatal(err)
		}
		r, err := gzip.NewReader(bytes.NewReader(gz))
		if err != nil {
			t.Fatalf("%s cell: %v", name, err)
		}
		plain, err := io.ReadAll(r)
		if err != nil {
			t.Fatalf("%s cell: reading its output back: %v", name, err)
		}
		if !bytes.Equal(plain, append(append([]byte{}, text...), text...)) {
			t.Errorf("%s cell: its output reads back to %d bytes, not the text twice (%d)", name, len(plain), 2*len(text))
		}
		t.Logf("%s cell: %d compressed bytes per round for %d (%.4f)", name, len(gz)/2, len(text), float64(len(gz))/2/float64(len(text)))
	}
}
