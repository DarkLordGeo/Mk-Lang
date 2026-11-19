// Why: std.big's tests need many operations per process and Go's math/big
// as the oracle for every result.
// What: bigDriver (the Mk driver program), bigCases and bigRun, shared by the
// std-big tests.
// How: the driver reads operations and operands from standard input and
// writes each result; it is built with mk build (dev and release) over a
// copy of std, and the Go side writes the cases and computes the expected
// results with math/big.
//
// MK_BIG_BASE names a snapshot of std to build over instead of the live
// tree, with std/big taken from this tree: a module elsewhere in std that
// does not check stops every mk command (mk checks all of std).

package std

import (
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// bigStdDir builds the std directory a test's mk uses: the live tree, or
// MK_BIG_BASE with this tree's std/big.
func bigStdDir(t *testing.T, root string) string {
	t.Helper()
	base := os.Getenv("MK_BIG_BASE")
	if base == "" {
		return copyStd(t, root, nil)
	}
	stdDir := filepath.Join(root, "std")
	if err := os.CopyFS(stdDir, os.DirFS(base)); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(stdDir, "big")); err != nil {
		t.Fatal(err)
	}
	if err := os.CopyFS(filepath.Join(stdDir, "big"), os.DirFS("big")); err != nil {
		t.Fatal(err)
	}
	return stdDir
}

// bigBuild builds src with mk build and flags over the std.big std and
// returns the executable, mk's output and its exit status.
func bigBuild(t *testing.T, src string, flags ...string) (string, string, int) {
	t.Helper()
	return bigBuildIn(t, t.TempDir(), src, flags...)
}

// bigBuildIn is bigBuild in the directory root, which outlives the test
// (a fuzz target's executable serves every input).
func bigBuildIn(t *testing.T, root, src string, flags ...string) (string, string, int) {
	t.Helper()
	stdDir := bigStdDir(t, root)
	if err := os.WriteFile(filepath.Join(root, "main.mk"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(root, "main")
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	args := append([]string{"build"}, flags...)
	args = append(args, "-o", exe, "main.mk")
	out, status := mkCmd(t, root, stdDir, args...)
	return exe, out, status
}

// bigExe builds src (dev, or with flags), failing the test on a failed
// build and skipping it when the toolchain is missing.
func bigExe(t *testing.T, src string, flags ...string) string {
	t.Helper()
	exe, out, status := bigBuild(t, src, flags...)
	if status != 0 {
		if strings.Contains(out, "no usable LLVM") {
			t.Skipf("needs the LLVM toolchain: %s", out)
		}
		t.Fatalf("mk build %v: exit %d\n%s", flags, status, out)
	}
	return exe
}

// bigModes are the builds every differential runs: dev and release.
var bigModes = [][]string{nil, {"--release"}}

// bigDriver is the Mk side: each case is an operation name on one line and
// its operands on the following lines; an Int is written "s w0 w1 ...", s
// '+' or '-', each word 16 lower-case hex digits, little-endian (FromWords'
// order), and a small integer in decimal. It prints each result the same
// way, one per line. Its parsing is its own, so a fault of big.Parse or
// Text cannot hide another operation's. "sqr" prints x.Mul(a, &x) and then
// x times an equal copy.
const bigDriver = `import std.big

Nib(c: u8): u64 {
    if c >= 97 {
        return u64(c - 87)
    }
    return u64(c - 48)
}

ReadInt(a: &Arena, line: str): OutOfMemory!big.Int from a {
    b := line.AsBytes()
    n := (b.Len() - 1) / 17
    z := try a.Slice[u64](n)
    for k in 0..n {
        var w: u64 = 0
        for j in 0..16 {
            w = (w << 4) | Nib(b[2 + 17 * k + j])
        }
        z[k] = w
    }
    return big.FromWords(z[..], b[0] == 45)
}

ReadBytes(a: &Arena, line: str): OutOfMemory![]u8 from a {
    b := line.AsBytes()
    z := try a.Slice[u8](b.Len() / 2)
    for k in 0..z.Len() {
        z[k] = Trunc[u8]((Nib(b[2 * k]) << 4) | Nib(b[2 * k + 1]))
    }
    return z[..]
}

WriteInt(out: mut Writer, x: &big.Int): WriteError!void {
    if x.Sign() < 0 {
        try out.Str("-")
    } else {
        try out.Str("+")
    }
    for w in x.Words() {
        try Print(out, " {w:016x}", (w))
    }
    try out.Str("\n")
}

WriteBytes(out: mut Writer, b: []u8): WriteError!void {
    for x in b {
        try Print(out, "{x:02x}", (x))
    }
    try out.Str("\n")
}

Next(lines: mut Lines): str {
    return lines.Next().Must()
}

Small(lines: mut Lines): ParseError!int {
    return Cast[int](try Next(lines).ParseInt())
}

Case(a: &Arena, out: mut Writer, op: str, lines: mut Lines): WriteError|ParseError|OutOfMemory!void {
    if op == "frombytes" {
        b := try ReadBytes(a, Next(lines))
        try WriteInt(out, &(try big.FromBytes(a, b)))
        return
    }
    if op == "parse" {
        base := try Small(lines)
        max := try Small(lines)
        s := Next(lines)
        match big.Parse(a, s, base, max) {
            ok(v) {
                try WriteInt(out, &v)
            }
            err(e) {
                try Print(out, "{e}\n", (e))
            }
        }
        return
    }
    if op == "parsehex" {
        base := try Small(lines)
        max := try Small(lines)
        b := try ReadBytes(a, Next(lines))
        s := str.From(b) else {
            try out.Str("notutf8\n")
            return
        }
        match big.Parse(a, s, base, max) {
            ok(v) {
                try WriteInt(out, &v)
            }
            err(e) {
                try Print(out, "{e}\n", (e))
            }
        }
        return
    }
    x := try ReadInt(a, Next(lines))
    if op == "add" || op == "sub" || op == "mul" || op == "divmod" || op == "mod" || op == "cmp" || op == "eq" || op == "modpow" || op == "modinv" || op == "gcd" {
        y := try ReadInt(a, Next(lines))
        if op == "add" {
            try WriteInt(out, &(try x.Add(a, &y)))
        } else if op == "sub" {
            try WriteInt(out, &(try x.Sub(a, &y)))
        } else if op == "mul" {
            try WriteInt(out, &(try x.Mul(a, &y)))
        } else if op == "divmod" {
            (q, r) := try x.DivMod(a, &y)
            try WriteInt(out, &q)
            try WriteInt(out, &r)
        } else if op == "mod" {
            try WriteInt(out, &(try x.Mod(a, &y)))
        } else if op == "cmp" {
            c := x.Cmp(&y)
            l := x.Less(&y)
            try Print(out, "{c} {l}\n", (c, l))
        } else if op == "eq" {
            e := x.Eq(&y)
            // Hash agrees with Eq: y, a copy in other words, finds x's
            // entry exactly when they are equal.
            c := try y.Clone(a)
            // The key is a clone in a, by value: a reference to the local x
            // in a map made in a is E0505.
            var m := Map[big.Int, int].New(a)
            try m.Set(try x.Clone(a), 1)
            found := m.Get(&c).IsSome()
            try Print(out, "{e} {found}\n", (e, found))
        } else if op == "modpow" {
            m := try ReadInt(a, Next(lines))
            try WriteInt(out, &(try x.ModPow(a, &y, &m)))
        } else if op == "modinv" {
            match try x.ModInverse(a, &y) {
                some(v) {
                    try WriteInt(out, &v)
                }
                none {
                    try out.Str("none\n")
                }
            }
        } else if op == "gcd" {
            try WriteInt(out, &(try x.Gcd(a, &y)))
        }
        return
    }
    if op == "shl" {
        n := try Small(lines)
        try WriteInt(out, &(try x.Shl(a, n)))
    } else if op == "shr" {
        n := try Small(lines)
        try WriteInt(out, &(try x.Shr(a, n)))
    } else if op == "bit" {
        i := try Small(lines)
        b := x.Bit(i)
        try Print(out, "{b}\n", (b))
    } else if op == "neg" {
        try WriteInt(out, &(try x.Neg(a)))
    } else if op == "clone" {
        try WriteInt(out, &(try x.Clone(a)))
    } else if op == "sqrt" {
        try WriteInt(out, &(try x.Sqrt(a)))
    } else if op == "sqr" {
        // Mul with the receiver as its own argument, then with an equal
        // copy: both take the square (sqr.mk).
        c := try x.Clone(a)
        try WriteInt(out, &(try x.Mul(a, &x)))
        try WriteInt(out, &(try x.Mul(a, &c)))
    } else if op == "info" {
        s := x.Sign()
        n := x.BitLen()
        f := x.ToF64().Bits()
        try Print(out, "{s} {n} {f:016x} ", (s, n, f))
        match x.ToI64() {
            some(v) {
                try Print(out, "{v}\n", (v))
            }
            none {
                try out.Str("none\n")
            }
        }
        try WriteBytes(out, try x.Bytes(a))
    } else if op == "text" {
        base := try Small(lines)
        try out.Str(try x.Text(a, base))
        try out.Str("\n")
    } else if op == "format" {
        try Print(out, "{x}\n", (x: &x))
    }
}

export Main(sys: &System): ReadError|WriteError|Utf8Error|ParseError|OutOfMemory!void {
    var inp := sys.Stdin()
    buf := try sys.arena.Slice[u8](1 << 27)
    n := try inp.ReadAll(buf)
    text := try str.From(buf[0..n])
    var out := sys.Stdout()
    var lines := text.Lines()
    var w := try sys.arena.Sub()
    defer w.Free()
    for op := lines.Next() {
        try Case(&w, mut out, op, mut lines)
        w.Reset()
    }
}
`

// bigCases accumulates a differential run's input and expected output.
type bigCases struct {
	in, want strings.Builder
	n        int
}

// bigInt writes x in the driver's form.
func bigInt(x *big.Int) string {
	var b strings.Builder
	if x.Sign() < 0 {
		b.WriteByte('-')
	} else {
		b.WriteByte('+')
	}
	for _, w := range x.Bits() {
		fmt.Fprintf(&b, " %016x", uint64(w))
	}
	return b.String()
}

// add appends one case: op, its operand lines, and its expected lines.
func (c *bigCases) add(op string, operands []string, want ...string) {
	c.in.WriteString(op + "\n")
	for _, o := range operands {
		c.in.WriteString(o + "\n")
	}
	for _, w := range want {
		c.want.WriteString(w + "\n")
	}
	c.n++
}

// bigRun builds the driver in each mode and compares its output on c's
// input with c's expected output.
func bigRun(t *testing.T, c *bigCases) {
	t.Helper()
	for _, mode := range bigModes {
		exe := bigExe(t, bigDriver, mode...)
		r := runNative(t, exe, c.in.String())
		if r.code != 0 {
			t.Fatalf("%v: driver exit %d\nstderr: %s", mode, r.code, r.stderr)
		}
		got := strings.ReplaceAll(r.stdout, "\r\n", "\n")
		bigCompare(t, fmt.Sprint(mode), got, c.want.String())
	}
}

// bigCompare reports the first differing lines of got and want.
func bigCompare(t *testing.T, what, got, want string) {
	t.Helper()
	g := strings.Split(strings.TrimRight(got, "\n"), "\n")
	w := strings.Split(strings.TrimRight(want, "\n"), "\n")
	bad := 0
	for i := 0; i < max(len(g), len(w)); i++ {
		var gl, wl string
		if i < len(g) {
			gl = g[i]
		}
		if i < len(w) {
			wl = w[i]
		}
		if gl != wl {
			t.Errorf("%s line %d:\n got  %.300s\n want %.300s", what, i+1, gl, wl)
			bad++
			if bad == 5 {
				t.Fatalf("%s: stopping after 5 differences (%d lines got, %d want)", what, len(g), len(w))
			}
		}
	}
}
