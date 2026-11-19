// Why: the Encoder's tables hold u32 positions rebased every RebaseStep past RebaseAt, which a real
// stream reaches only after 2 GiB, so ordinary tests never rebase.
// What: a test of the streaming Encoder's table rebase.
// How: a std copy whose deflate.mk rebases every 64 KiB past 128 KiB makes a 1 MiB stream rebase many times.

package std

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCompress_EncoderRebase: an Encoder over a std copy that rebases
// every 64 KiB writes exactly the bytes the normal build writes, for every
// framing and kind of input, in pieces small and large, with and without
// flushes, and Go reads each stream back to the input. 64 KiB is more than
// the window plus a match, so every entry a rebase drops is out of reach
// in both builds.
func TestCompress_EncoderRebase(t *testing.T) {
	dir, err := packageTempDir("mkcompress2rebase")
	if err != nil {
		t.Fatal(err)
	}
	stdDir := czStdCopy(t, dir)
	p := filepath.Join(stdDir, "compress", "deflate.mk")
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, r := range [][2]string{{"const RebaseAt = 2147483648", "const RebaseAt = 131072"}, {"const RebaseStep = 1073741824", "const RebaseStep = 65536"}} {
		if !strings.Contains(s, r[0]) {
			t.Fatalf("deflate.mk has no %q", r[0])
		}
		s = strings.Replace(s, r[0], r[1], 1)
	}
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
	small := czBuildStd(t, dir, stdDir, c2EncProg)
	normal := c2Exe(t, "enc", c2EncProg)
	inputs := map[string][]byte{"real": czRealInput(t)}
	for kind := 0; kind < 5; kind++ {
		inputs[fmt.Sprintf("kind %d", kind)] = czInput(kind, 1<<20, uint64(kind)+77)
	}
	n := 0
	for name, data := range inputs {
		for f := 0; f < 3; f++ {
			maxp := []int{70000, 4096, 100000}[f]
			chance := []int{0, 40, 0}[f]
			a, _ := c2Encode(t, small, data, f, 6, uint64(f)+3, maxp, chance, 0)
			w, _ := c2Encode(t, normal, data, f, 6, uint64(f)+3, maxp, chance, 0)
			if !bytes.Equal(a, w) {
				t.Errorf("%s framing %d: rebasing build wrote %d bytes, normal build %d, and they differ", name, f, len(a), len(w))
			}
			got, err := c2Decode(f, a)
			if err != nil || !bytes.Equal(got, data) {
				t.Errorf("%s framing %d: Go read the rebasing build's %d bytes back to %d bytes, %v", name, f, len(a), len(got), err)
			}
			n++
		}
	}
	t.Logf("%d streams", n)
}
