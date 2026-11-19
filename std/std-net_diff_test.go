// Why: net.ParseAddr and Addr's Format follow Go's netip, so Go is the oracle.
// What: a differential test against netip.ParseAddrPort and AddrPort.String.
// How: a Mk program (dev and release) prints "ok <Format>" or "err" per line;
// a zoned address differs on purpose: Go accepts it, Mk refuses it.

package std

import (
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const net2DiffMain = `import std.fs
import std.net

export Main(sys: &System): fs.Error|OutOfMemory|WriteError!void {
    text := try fs.ReadText(sys.arena, sys.io, sys.args[1])
    var w := sys.Stdout()
    b := text.AsBytes()
    var start := 0
    for i in 0..b.Len() {
        if b[i] == 10 {
            line := str.From(b[start..i]).Must()
            a := net.ParseAddr(line) else {
                try Print(mut w, "err\n")
                start = i + 1
                continue
            }
            try Print(mut w, "ok {a}\n", (a))
            start = i + 1
        }
    }
    try w.Flush()
}
`

// net2Want is the reference's line for s.
func net2Want(s string) string {
	ap, err := netip.ParseAddrPort(s)
	if err != nil || ap.Addr().Zone() != "" {
		return "err"
	}
	return "ok " + ap.String()
}

// net2Rng is splitmix64.
type net2Rng struct{ s uint64 }

func (r *net2Rng) next() uint64 {
	r.s += 0x9E3779B97F4A7C15
	z := r.s
	z = (z ^ (z >> 30)) * 0xBF58476D1CE4E5B9
	z = (z ^ (z >> 27)) * 0x94D049BB133111EB
	return z ^ (z >> 31)
}

func (r *net2Rng) below(n int) int { return int(r.next() % uint64(n)) }

// net2Inputs: the adversarial table, then seeded random addresses written
// in many spellings, then seeded random strings over the address alphabet.
func net2Inputs() []string {
	in := []string{
		"", ":", "::", ":80", "1.2.3.4", "1.2.3.4:", "1.2.3.4:80", "0.0.0.0:0", "255.255.255.255:65535",
		"1.2.3.4:65536", "1.2.3.4:99999", "1.2.3.4:0000000000080", "1.2.3.4:-1", "1.2.3.4:+1", "01.2.3.4:1",
		"1.2.3.04:1", "0.0.0.00:1", "256.1.1.1:1", "1.2.3:1", "1.2.3.4.5:1", "1..2.3:1", ".1.2.3:1", "1.2.3.:1",
		"[::]:0", "[::1]:1", "[::1]", "[::1]1", "[::1:1", "::1]:1", "::1:1", "[1.2.3.4]:1", "[]:1", "[:]:1",
		"[:::]:1", "[1:::2]:1", "[1::2::3]:1", "[12345::]:1", "[1:2:3:4:5:6:7:8]:1", "[1:2:3:4:5:6:7:8:9]:1",
		"[1:2:3:4:5:6:7::]:1", "[::2:3:4:5:6:7:8]:1", "[1:2:3:4:5:6:7:8::]:1", "[::1:2:3:4:5:6:7:8]:1",
		"[1:2:3:4:5:6:7]:1", "[1:2:3:4:5:6:7:]:1", "[:1:2:3:4:5:6:7]:1", "[::ffff:1.2.3.4]:1", "[::FFFF:102:304]:1",
		"[::1.2.3.4]:1", "[1:2:3:4:5:6:1.2.3.4]:1", "[1:2:3:4:5:6:7:1.2.3.4]:1", "[1:2:3:4:5::1.2.3.4]:1",
		"[1:2:3:4:5:6::1.2.3.4]:1", "[::1.2.3]:1", "[::1.2.3.4.5]:1", "[1.2.3.4::]:1", "[::01.2.3.4]:1",
		"[::a.2.3.4]:1", "[::1%eth0]:1", "[fe80::1%1]:1", "[::1%]:1", "[%1]:1", "[0:0:0:0:0:0:0:0]:1",
		"[0:0:0:0:0:0:0:1]:1", "[1:0:0:2:0:0:0:3]:1", "[1:0:2:0:3:0:4:0]:1", "[0:1:0:0:2:0:0:0]:1",
		"[2001:0db8:0000:0000:0000:ff00:0042:8329]:1", "[::ffff:0:0]:1", "[::ffff:255.255.255.255]:1",
		"[0000:0000:0000:0000:0000:ffff:0102:0304]:1", "[::fffe:1.2.3.4]:1", "[64:ff9b::1.2.3.4]:1",
		" 1.2.3.4:1", "1.2.3.4:1 ", "1.2.3.4 :1", "[ ::1]:1", "[::1 ]:1", "[::g]:1", "[0x1::]:1",
	}
	r := &net2Rng{s: 0x6E657432}
	hex := "0123456789abcdefABCDEF"
	for i := 0; i < 20000; i++ {
		var ip [16]byte
		for k := 0; k < 8; k++ {
			if r.below(3) == 0 {
				continue
			}
			v := r.next()
			switch r.below(4) {
			case 0:
				v &= 0xF
			case 1:
				v &= 0xFF
			}
			ip[2*k], ip[2*k+1] = byte(v>>8), byte(v)
		}
		port := r.below(65536)
		var s string
		switch r.below(5) {
		case 0:
			s = fmt.Sprintf("%d.%d.%d.%d:%d", ip[0], ip[1], ip[2], ip[3], port)
		case 1:
			s = netip.AddrPortFrom(netip.AddrFrom16(ip), uint16(port)).String()
		case 2:
			// Every group written in full, letters in random case.
			var b strings.Builder
			b.WriteByte('[')
			for k := 0; k < 8; k++ {
				if k > 0 {
					b.WriteByte(':')
				}
				g := fmt.Sprintf("%04x", int(ip[2*k])<<8|int(ip[2*k+1]))
				if r.below(2) == 0 {
					g = strings.ToUpper(g)
				}
				b.WriteString(g)
			}
			fmt.Fprintf(&b, "]:%d", port)
			s = b.String()
		case 3:
			ip[10], ip[11] = 0xFF, 0xFF
			for k := 0; k < 10; k++ {
				ip[k] = 0
			}
			s = netip.AddrPortFrom(netip.AddrFrom16(ip), uint16(port)).String()
		default:
			// A valid address with one random byte changed, inserted or
			// removed.
			b := []byte(netip.AddrPortFrom(netip.AddrFrom16(ip), uint16(port)).String())
			alpha := hex + ":.[]%"
			p := r.below(len(b))
			switch r.below(3) {
			case 0:
				b[p] = alpha[r.below(len(alpha))]
			case 1:
				b = append(b[:p], append([]byte{alpha[r.below(len(alpha))]}, b[p:]...)...)
			default:
				b = append(b[:p], b[p+1:]...)
			}
			s = string(b)
		}
		in = append(in, s)
	}
	alpha := "0123456789abcdefABCDEF:.[]%:::...."
	for i := 0; i < 20000; i++ {
		n := r.below(48)
		b := make([]byte, n)
		for k := range b {
			b[k] = alpha[r.below(len(alpha))]
		}
		in = append(in, string(b))
	}
	return in
}

func TestNet_DiffParseFormat(t *testing.T) {
	in := net2Inputs()
	root := t.TempDir()
	data := filepath.Join(root, "in.txt")
	if err := os.WriteFile(data, []byte(strings.Join(in, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	stdDir := net2Std(t, root, nil)
	if err := os.WriteFile(filepath.Join(root, "main.mk"), []byte(net2DiffMain), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MK_CACHE", filepath.Join(root, "cache"))
	for _, prof := range []string{"dev", "release"} {
		exe := filepath.Join(root, "diff-"+prof)
		if runtime.GOOS == "windows" {
			exe += ".exe"
		}
		args := []string{"build", "-o", exe, "main.mk"}
		if prof == "release" {
			args = []string{"build", "--release", "-o", exe, "main.mk"}
		}
		out, status := mkCmd(t, root, stdDir, args...)
		if status != 0 {
			if strings.Contains(out, "no usable LLVM") {
				t.Skipf("needs the LLVM toolchain: %s", out)
			}
			t.Fatalf("mk build: exit %d\n%s", status, out)
		}
		r := runNative(t, exe, "", data)
		if r.code != 0 {
			t.Fatalf("%s: exit %d\n%s", prof, r.code, r.stderr)
		}
		got := strings.Split(strings.TrimSuffix(strings.ReplaceAll(r.stdout, "\r\n", "\n"), "\n"), "\n")
		if len(got) != len(in) {
			t.Fatalf("%s: %d lines for %d inputs", prof, len(got), len(in))
		}
		bad, oks := 0, 0
		for i, s := range in {
			want := net2Want(s)
			if strings.HasPrefix(want, "ok") {
				oks++
			}
			if got[i] != want {
				bad++
				if bad <= 20 {
					t.Errorf("%s: %q: got %q, want %q", prof, s, got[i], want)
				}
			}
		}
		if bad > 0 {
			t.Errorf("%s: %d of %d lines differ", prof, bad, len(in))
		}
		t.Logf("%s: %d inputs, %d accepted by both", prof, len(in), oks)
	}
}
