// Why: AesGcm.Open, HKDF and HMAC read peer bytes in TLS, so they must survive
// any input and agree with Go.
// What: FuzzSTDCRYPTO_TlsPrimitives and TestSTDCRYPTO_FuzzSeedsMatchGo.
// How: each input becomes one driver command for the dev driver; Open gets a
// message Go sealed, with one bit flipped or none, as often as raw bytes.
// Its fuzz targets read untrusted input; tools/gate/safety.go lists each untrusted-input target.

package std

import (
	"bufio"
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// cr2Srv is the dev driver kept running across fuzz inputs, as
// std-crypto's cfAsk keeps its oracle: one process, a line in, a line out.
var cr2Srv struct {
	sync.Mutex
	cmd  *exec.Cmd
	in   io.WriteCloser
	out  *bufio.Reader
	errb *bytes.Buffer
}

// cr2Serve sends lines to the running dev driver (starting it if needed)
// and returns its answers. A dead driver fails the input with its stderr,
// and the next call starts a new one.
func cr2Serve(t *testing.T, lines []string) []string {
	t.Helper()
	cr2Srv.Lock()
	defer cr2Srv.Unlock()
	if cr2Srv.cmd == nil {
		cmd := exec.Command(cr2Exe(t, cr2Variants[0]))
		in, err := cmd.StdinPipe()
		if err != nil {
			t.Fatal(err)
		}
		out, err := cmd.StdoutPipe()
		if err != nil {
			t.Fatal(err)
		}
		var errb bytes.Buffer
		cmd.Stderr = &errb
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		cr2Srv.cmd, cr2Srv.in, cr2Srv.out, cr2Srv.errb = cmd, in, bufio.NewReader(out), &errb
	}
	werr := make(chan error, 1)
	go func() {
		_, err := io.WriteString(cr2Srv.in, strings.Join(lines, "\n")+"\n")
		werr <- err
	}()
	res := make([]string, 0, len(lines))
	for range lines {
		l, err := cr2Srv.out.ReadString('\n')
		if err != nil {
			cr2Srv.in.Close()
			cr2Srv.cmd.Wait()
			stderr := cr2Srv.errb.String()
			cr2Srv.cmd = nil
			t.Fatalf("driver died after %d answers: %v\nstderr:\n%s\nlast command: %.300s", len(res), err, stderr, lines[len(res)])
		}
		res = append(res, strings.TrimRight(l, "\r\n"))
	}
	if err := <-werr; err != nil {
		t.Fatalf("driver: write: %v", err)
	}
	return res
}

// cr2FuzzLine decodes data into one driver command.
func cr2FuzzLine(data []byte) string {
	b := append([]byte(nil), data...)
	for len(b) < 8 {
		b = append(b, 0)
	}
	take := func(n int) []byte {
		if n > len(b) {
			n = len(b)
		}
		x := b[:n]
		b = b[n:]
		return x
	}
	hdr := take(8)
	x := hex.EncodeToString
	switch hdr[0] % 5 {
	case 0, 1:
		kl := []int{16, 32, int(hdr[1] % 40)}[hdr[2]%3]
		key := cr2Rand(uint64(hdr[3]), kl)
		nonce := cr2Rand(uint64(hdr[4])+1, 12)
		aad := take(int(hdr[5] % 40))
		rest := b
		if hdr[0]%5 == 0 {
			return fmt.Sprintf("G %s %s %s %s", x(key), x(nonce), x(aad), x(rest))
		}
		if hdr[6]%2 == 0 && (kl == 16 || kl == 32) {
			sealed := cr2Seal(key, nonce, aad, rest)
			if hdr[7]%3 != 0 {
				bit := int(hdr[7]) * 7 % (8 * len(sealed))
				sealed[bit/8] ^= 1 << (bit % 8)
			}
			return fmt.Sprintf("O %s %s %s %s", x(key), x(nonce), x(aad), x(sealed))
		}
		return fmt.Sprintf("O %s %s %s %s", x(key), x(nonce), x(aad), x(rest))
	case 2, 3:
		bits := []int{256, 384, 512}[hdr[1]%3]
		if hdr[0]%5 == 2 {
			key := take(int(hdr[2]))
			return fmt.Sprintf("M %d %s %s", bits, x(key), x(b))
		}
		salt := take(int(hdr[2] % 160))
		info := take(int(hdr[3] % 64))
		n := (int(hdr[4]) | int(hdr[5])<<8) % 17000
		return fmt.Sprintf("K %d %s %s %s %d", bits, x(salt), x(b), x(info), n)
	}
	// An Open of raw bytes under a 16-byte key: no seal involved.
	return fmt.Sprintf("O %s %s  %s", x(cr2Rand(uint64(hdr[1]), 16)), x(cr2Rand(uint64(hdr[2]), 12)), x(b))
}

// cr2FuzzSeeds are inputs for each command and the AES-GCM vectors'
// messages as raw bytes.
func cr2FuzzSeeds(t testing.TB) [][]byte {
	seeds := [][]byte{
		{0, 0, 0, 1, 2, 3, 0, 0, 'h', 'i'},
		{1, 0, 1, 1, 2, 3, 0, 1, 'a', 'b', 'c'},
		{1, 7, 2, 1, 2, 3, 1, 0},
		{2, 1, 200},
		{3, 2, 10, 5, 0xff, 0x1f, 0, 0, 1, 2, 3},
		{4, 1, 2, 3, 4, 5, 6, 7, 8, 9},
	}
	root := os.Getenv("MK_EVIDENCE")
	if root == "" {
		root = filepath.Join("..", "..", "Mk-evidence")
	}
	if b, err := os.ReadFile(filepath.Join(root, "wycheproof", "testvectors_v1", "aes_gcm_test.json")); err == nil {
		var wp cr2WpAead
		if json.Unmarshal(b, &wp) == nil {
			for gi, g := range wp.TestGroups {
				for ti, c := range g.Tests {
					if ti%9 != 0 {
						continue
					}
					ct, _ := hex.DecodeString(c.Ct + c.Tag)
					seeds = append(seeds, append([]byte{4, byte(gi), byte(ti), 0, 0, 0, 0, 0}, ct...))
				}
			}
		}
	}
	return seeds
}

// FuzzSTDCRYPTO_TlsPrimitives is seeded from the Wycheproof AES-GCM vectors:
//
//	go test ./std -run '^$' -fuzz FuzzSTDCRYPTO_TlsPrimitives -fuzztime 5m -parallel 2
func FuzzSTDCRYPTO_TlsPrimitives(f *testing.F) {
	for _, s := range cr2FuzzSeeds(f) {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 4096 {
			data = data[:4096]
		}
		line := cr2FuzzLine(data)
		got := cr2Serve(t, []string{line})
		if want := cr2Want(line); got[0] != want {
			t.Fatalf("%s\n got  %s\n want %s", cryptoTail(line, 400), cryptoTail(got[0], 400), cryptoTail(want, 400))
		}
	})
}

// TestSTDCRYPTO_FuzzSeedsMatchGo runs every seed's command through every
// driver variant in one batch, so the seeds are checked without -fuzz.
func TestSTDCRYPTO_FuzzSeedsMatchGo(t *testing.T) {
	t.Parallel()
	var lines []string
	for _, s := range cr2FuzzSeeds(t) {
		lines = append(lines, cr2FuzzLine(s))
	}
	cr2Check(t, "seeds", lines)
}
