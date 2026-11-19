// Why: simd.ValidUtf8's ASCII prefilter below DispatchMin (all bytes below 0x80
// means valid, no table lookup) must not change an answer.
// What: TestSTDSIMD_ValidUtf8AsciiPrefilter, its program pfProgram and model
// pfModel, over the bad bytes sdBad and the sequences sdSeqs.
// How: every length 0 to 80 at four start offsets, each bad byte and sequence at
// every position, bytes around the slice set to change the answer if read.

package std

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"
)

const pfProgram = `import std.simd

H(h: u64, v: bool): u64 {
    var x: u64 = 1
    if v {
        x = 2
    }
    return (h ^ x) *% 0x100000001b3
}

export Main(sys: &System): OutOfMemory|WriteError!void {
    var out := sys.Stdout()
    buf := try sys.arena.Slice[u8](128)
    bad := [0x00, 0x7F, 0x80, 0x8F, 0x90, 0x9F, 0xA0, 0xBF, 0xC0, 0xC1, 0xC2, 0xDF, 0xE0, 0xED, 0xEF, 0xF0, 0xF4, 0xF5, 0xFF]
    seqs := [0x61, 0x7F, 0xC280, 0xDFBF, 0xE0A080, 0xED9FBF, 0xEE8080, 0xEFBFBF, 0xF0908080, 0xF48FBFBF, 0xC3A9, 0xE282AC, 0xF09F9880]
    lens := [1, 1, 2, 2, 3, 3, 3, 3, 4, 4, 2, 3, 4]
    offs := [0, 1, 7, 15]
    for n in 0..81 {
        var h: u64 = 0
        for a in offs {
            st := 8 + a
            for i in 0..128 {
                buf[i] = 0xF0
            }
            for i in 0..n {
                buf[st + i] = Trunc[u8](0x41 + (i * 7 + a) % 26)
            }
            for i in st + n..128 {
                buf[i] = 0x80
            }
            h = H(h, simd.ValidUtf8(buf[st..st + n]))
            for p in 0..n {
                c := buf[st + p]
                for b in bad {
                    buf[st + p] = Trunc[u8](b)
                    h = H(h, simd.ValidUtf8(buf[st..st + n]))
                }
                buf[st + p] = c
                for k in 0..13 {
                    var q := 0
                    for q < lens[k] && p + q < n {
                        buf[st + p + q] = Trunc[u8]((seqs[k] >> ((lens[k] - 1 - q) * 8)) & 0xFF)
                        q += 1
                    }
                    h = H(h, simd.ValidUtf8(buf[st..st + n]))
                    for r in 0..q {
                        buf[st + p + r] = Trunc[u8](0x41 + ((p + r) * 7 + a) % 26)
                    }
                }
            }
        }
        try Print(mut out, "PF {n} {h}\n", (n, h))
    }
}
`

// pfModel is pfProgram's output with Go's utf8.Valid.
func pfModel() string {
	var w strings.Builder
	buf := make([]byte, 128)
	hv := func(h uint64, v bool) uint64 {
		x := uint64(1)
		if v {
			x = 2
		}
		return (h ^ x) * 0x100000001b3
	}
	for n := 0; n <= 80; n++ {
		var h uint64
		for _, a := range []int{0, 1, 7, 15} {
			st := 8 + a
			for i := range buf {
				buf[i] = 0xF0
			}
			for i := 0; i < n; i++ {
				buf[st+i] = byte(0x41 + (i*7+a)%26)
			}
			for i := st + n; i < 128; i++ {
				buf[i] = 0x80
			}
			s := buf[st : st+n]
			h = hv(h, utf8.Valid(s))
			for p := 0; p < n; p++ {
				c := s[p]
				for _, b := range sdBad {
					s[p] = b
					h = hv(h, utf8.Valid(s))
				}
				s[p] = c
				for _, seq := range sdSeqs {
					q := 0
					for q < len(seq) && p+q < n {
						s[p+q] = seq[q]
						q++
					}
					h = hv(h, utf8.Valid(s))
					for r := 0; r < q; r++ {
						s[p+r] = byte(0x41 + ((p+r)*7+a)%26)
					}
				}
			}
		}
		fmt.Fprintf(&w, "PF %d %d\n", n, h)
	}
	return w.String()
}

// TestSTDSIMD_ValidUtf8AsciiPrefilter: every length 0 to 80 (both sides of
// 16 and of 64, where the prefilter stops) at four start offsets, over an
// ASCII text with, at every position in turn, each byte of sdBad and each
// well-formed sequence of sdSeqs (cut by the end where it runs past it),
// with leads before the slice and continuations after it so that a read
// outside it would change the answer, against Go's utf8.Valid; dev and
// release.
func TestSTDSIMD_ValidUtf8AsciiPrefilter(t *testing.T) {
	t.Parallel()
	want := pfModel()
	for _, flags := range c2Flags {
		t.Run(c2FlagName(flags), func(t *testing.T) {
			t.Parallel()
			diffLines(t, sdRun(t, pfProgram, flags...), want)
		})
	}
}
