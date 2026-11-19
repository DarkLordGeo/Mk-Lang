// Why: gen dyn.Of's output dispatches through a type key, which must reach
// the right implementation and stop a forged mix.
// What: TestSTDDYN_KernelRuns and TestSTDDYN_ForgedMixPanics.
// How: built and run natively, dev and release, with the real type key
// std.dyn.erased.Key[T], which codegen and the evaluator fold.

package std

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// dynBuild builds src as main.mk over a copy of std and its own cache, and
// returns the executable, mk's output and its status.
func dynBuild(t *testing.T, src string, flags ...string) (string, string, int) {
	t.Helper()
	r := newDynRoot(t, nil)
	r.write(t, "main.mk", src)
	exe := filepath.Join(r.dir, "main")
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	args := append([]string{"build"}, flags...)
	out, status := r.mk(t, append(args, "-o", exe, "main.mk")...)
	if status != 0 && strings.Contains(out, "no usable LLVM") {
		t.Skipf("needs the LLVM toolchain: %s", out)
	}
	return exe, out, status
}

// dynRun builds and runs src and checks the run's exit code and stdout, and
// that stderr holds stderrHas.
func dynRun(t *testing.T, src string, code int, stdout, stderrHas string, flags ...string) {
	t.Helper()
	exe, out, status := dynBuild(t, src, flags...)
	if status != 0 {
		t.Fatalf("mk build: exit %d\n%s", status, out)
	}
	r := runNative(t, exe, "")
	wantRun(t, r, code, stdout)
	if !strings.Contains(r.stderr, stderrHas) {
		t.Errorf("stderr lacks %q:\n%s", stderrHas, r.stderr)
	}
}

// kernelMain runs the hosted kernel (kernelHosted): Boot's two writes and
// its refused syscall through Kernel's DeviceDyn, two implementations
// through one array, and a DeviceDyn passed as T: Device. The Uart's bytes
// are summed, so the output shows every write reached it in order.
const kernelMain = `
Fan(u: mut Uart, z: mut Null): int {
    var ds := [DeviceDyn.Of(u), DeviceDyn.Of(z)]
    var total := 0
    for d in mut ds {
        total += d.Write("ab".AsBytes())
    }
    return total
}

Drive[T: Device](d: mut T): int {
    return d.Write("x".AsBytes())
}

Twice(u: mut Uart): int {
    var d := DeviceDyn.Of(u)
    return Drive(mut d)
}

export Main(sys: &System): WriteError!void {
    var out := sys.Stdout()
    var u := Uart(buf: [0; 64], n: 0)
    var z := Null(dropped: 0)
    boot := Boot(mut u)
    fan := Fan(mut u, mut z)
    twice := Twice(mut u)
    n := u.n
    dropped := z.dropped
    var sum := 0
    var mix := 0
    for i in 0..n {
        b := Cast[int](u.buf[i])
        sum += b
        mix = (mix * 31 + b) % 1000000007
    }
    try Print(mut out, "{boot} {fan} {twice} {n} {dropped} {sum} {mix}\n", (boot, fan, twice, n, dropped, sum, mix))
}
`

// kernelWant is kernelMain's line: Boot 5 + 7 bytes and 2 writes; Fan 2
// (the Null returns 0) with 2 dropped; Twice 1; 15 bytes of
// "hello kernelabx".
func kernelWant() string {
	text := "hello kernelabx"
	sum, mix := 0, 0
	for i := 0; i < len(text); i++ {
		sum += int(text[i])
		mix = (mix*31 + int(text[i])) % 1000000007
	}
	return "14 2 1 15 2 " + itoaDyn(sum) + " " + itoaDyn(mix) + "\n"
}

func itoaDyn(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for ; n > 0; n /= 10 {
		b = append([]byte{byte('0' + n%10)}, b...)
	}
	return string(b)
}

// TestSTDDYN_KernelRuns: the hosted kernel of doc/examples.md runs and every
// call reaches the object it was boxed from, dev and release.
func TestSTDDYN_KernelRuns(t *testing.T) {
	t.Parallel()
	for _, flags := range [][]string{nil, {"--release"}} {
		name := "dev"
		if flags != nil {
			name = "release"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			dynRun(t, kernelHosted+kernelMain, 0, kernelWant(), "", flags...)
		})
	}
}

// TestSTDDYN_ForgedMixPanics: handwritten code in the user's module may
// build a DeviceDyn by struct literal (generated code has the user's
// privileges), here pairing a Null's Ref with a Uart's table. The
// call does not reach the Null as a Uart: Ref.Mut finds the key of another
// type and panics with code 8 (exit 101), in dev and in release, where
// Ref.Mut is inlined into the adapter and keeps the key test.
func TestSTDDYN_ForgedMixPanics(t *testing.T) {
	t.Parallel()
	src := kernelDevice + "\n" + uartDecl + `
Forge(u: mut Uart, z: mut Null): int {
    a := DeviceDyn.Of(u)
    var bad := DeviceDyn(obj: dyn.Ref.Of(z), table: a.table)
    return bad.Write("boom".AsBytes())
}

export Main(sys: &System): WriteError!void {
    var out := sys.Stdout()
    var u := Uart(buf: [0; 64], n: 0)
    var z := Null(dropped: 0)
    try out.Str("start\n")
    k := Forge(mut u, mut z)
    try Print(mut out, "unreachable {k}\n", (k: k))
}
`
	for _, flags := range [][]string{nil, {"--release"}} {
		name := "dev"
		if flags != nil {
			name = "release"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			dynRun(t, src, 101, "start\n", "dyn: an erased object recovered as a type it was not made from", flags...)
		})
	}
}
