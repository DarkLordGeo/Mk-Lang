// Why: a Mk library on Linux runs inside a glibc program that owns the
// process; an ELF check cannot show that its runtime starts, serves calls
// from the host's threads and stops there.
// What: TestLibGate_Linux, the Linux leg of TestLibGate_Windows.
// How: on a Linux host, for the host's own Linux target (x86_64 or
// aarch64), the bundled clang links a glibc C host against the shared and
// the static library and runs it natively.

package driver

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"mk/internal/types"
)

// gateLinuxC is gateC for a glibc host: the runtime started with options,
// Wait on a 200 ms timer from the main thread and from a pthread at once,
// Twice, then a clean stop; with argument "after", a call after the stop,
// which is panic 16.
const gateLinuxC = `#include "mlib.h"
#include <pthread.h>
#include <string.h>
static volatile int32_t second;
static void *worker(void *p) { (void)p; second = Wait(200); return 0; }
int main(int argc, char **argv) {
    MlibMkOptions o = {2, 0};
    if (mlib_mk_start(&o) != 0) return 10;
    if (mlib_mk_start(0) != 0) return 14;
    pthread_t h;
    if (pthread_create(&h, 0, worker, 0) != 0) return 12;
    int32_t first = Wait(200);
    pthread_join(h, 0);
    if (first != 201 || second != 201) return 11;
    if (Twice(21) != 42) return 15;
    Point p = {3, 4};
    MkStr s = {(const uint8_t *)"hello", 5};
    if (Sum(&p) + Len(s) + Next(7) != 20) return 16;
    if (mlib_mk_stop() != 0) return 13;
    if (argc > 1 && strcmp(argv[1], "after") == 0) Wait(1);
    return 0;
}
`

// TestLibGate_Linux: on a Linux host, a glibc C program built with the
// bundled clang links the Mk library for the host's target (libmlib.so.1,
// then libmlib.a), starts its runtime, calls Wait from its main thread and
// from a pthread at once (both answer 201), calls the pure exports and
// stops the runtime, exiting 0; a call after the stop is panic 16 (exit
// 101). Dev and release.
func TestLibGate_Linux(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("SKIP: a Linux host runs the Linux leg natively")
	}
	target := hostLinux()
	tools := needTools(t)
	host := func(t *testing.T, dir string, libs ...string) string {
		t.Helper()
		c := filepath.Join(dir, "gate.c")
		if err := os.WriteFile(c, []byte(gateLinuxC), 0o644); err != nil {
			t.Fatal(err)
		}
		exe := filepath.Join(dir, "gate")
		args := append([]string{"--target=" + target.LLVMTriple, "-fuse-ld=lld", "-Wall", "-Werror", "-std=c11", "-pthread",
			"-I" + dir, c, "-o", exe, "-Wl,-rpath," + dir}, libs...)
		runTool(t, dir, tools.Clang, args...)
		return exe
	}
	for _, prof := range []types.Profile{types.Dev, types.Release} {
		t.Run(prof.String(), func(t *testing.T) {
			dir := t.TempDir()
			so := buildLib(t, libPackage(t, "dynamic"), target, prof, dir)
			exe := host(t, dir, so)
			if _, errOut, code := runExe(t, target, exe, ""); code != 0 {
				t.Errorf("shared gate host: exit %d, stderr %q", code, errOut)
			}
			if _, errOut, code := runExe(t, target, exe, "", "after"); code != 101 || !strings.Contains(errOut, "after the library's stop") {
				t.Errorf("a call after the stop: exit %d, stderr %q; want 101 and panic 16", code, errOut)
			}
			st := t.TempDir()
			lib := buildLib(t, libPackage(t, "static"), target, prof, st)
			exe = host(t, st, lib)
			if _, errOut, code := runExe(t, target, exe, ""); code != 0 {
				t.Errorf("static gate host: exit %d, stderr %q", code, errOut)
			}
		})
	}
}
