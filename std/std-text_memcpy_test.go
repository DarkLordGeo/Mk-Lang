// Why: a text.Capped write is one raw.Copy of an unknown length, a call of the
// support pack's memcpy, whose size classes must be right at every offset.
// What: TestMemcpyThroughCapped.
// How: writes every length 0 to 300 from every source offset 0 to 15 into a
// Capped at every destination offset 0 to 15, and checks the 400-byte buffer.

package std

import (
	"testing"
)

const cappedMemcpyProgram = `import std.text

Mix(x: u64): u64 {
    var z := x
    z = (z ^ (z >> 30)) *% 0xbf58476d1ce4e5b9
    z = (z ^ (z >> 27)) *% 0x94d049bb133111eb
    return z ^ (z >> 31)
}

Put(dst: mut []u8, b: []u8): WriteError!void {
    var c := text.Capped.New(dst)
    var w := c.Writer()
    try w.Bytes(b)
}

export Main(sys: &System): WriteError!void {
    var src: [400]u8 = [0; 400]
    var st: u64 = 13
    for i in 0..400 {
        st = st +% 0x9e3779b97f4a7c15
        src[i] = Trunc[u8](Mix(st))
        if src[i] == 0xEE {
            src[i] = 0x11
        }
    }
    var dst: [400]u8 = [0; 400]
    var checked := 0
    var bad := 0
    for os in 0..16 {
        for od in 0..16 {
            for n in 0..301 {
                for i in 0..400 {
                    dst[i] = 0xEE
                }
                try Put(mut dst[od..od + n], src[os..os + n])
                for i in 0..400 {
                    want: u8 = if i >= od && i < od + n { src[os + i - od] } else { 0xEE }
                    if dst[i] != want {
                        bad += 1
                    }
                }
                checked += 1
            }
        }
    }
    var out := sys.Stdout()
    try Print(mut out, "checked {checked} bad {bad}\n", (checked, bad))
}
`

func TestMemcpyThroughCapped(t *testing.T) {
	t.Parallel()
	for _, flags := range c2Flags {
		t.Run(c2FlagName(flags), func(t *testing.T) {
			t.Parallel()
			r := runNative(t, buildNative(t, nil, cappedMemcpyProgram, flags...), "")
			if r.code != 0 || r.stdout != "checked 77056 bad 0\n" {
				t.Errorf("exit %d, stdout %q, want 0 and %q\nstderr: %s", r.code, r.stdout, "checked 77056 bad 0\n", r.stderr)
			}
		})
	}
}
