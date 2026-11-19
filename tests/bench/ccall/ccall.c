// ccall, C side: the freestanding object of tests/bench/ccall (README.md:
// the budget "C call from a task" and the runtime tests of an extern on the
// system stack). The package ref (ref/ref.mk) declares its three functions
// as externs.
//
// No libc, no CRT, no import, no static constructor. The only symbol it may
// leave undefined is __chkstk on x86_64-pc-windows-msvc, which clang calls
// for RefStack's variable-sized frame and which every Mk executable defines
// (the support pack). Built by `go run ./tools/bench
// link tests/bench/ccall tests/bench/ccall/ccall.c` with clang 21 at
// -O3 -march=x86-64-v2 -ffp-contract=off -fno-math-errno -ffreestanding -c
// (tools/bench/codegen.go codegenCCommand), a native object linked into the
// Mk program, so no LTO can inline RefStack past the extern's stack switch.
#include <stdint.h>

// RefAdd is the call the timing loop makes, one stack switch each from Mk.
__attribute__((noinline)) int64_t RefAdd(int64_t a, int64_t b) {
    return a + b;
}

// RefAddLoop makes n direct C-to-C calls of RefAdd: the direct-call
// baseline. It returns 35000000 for n = 10,000,000.
int64_t RefAddLoop(int64_t n) {
    int64_t s = 0;
    for (int64_t i = 0; i < n; i++) {
        s = RefAdd(s, i & 7);
    }
    return s;
}

// RefStack uses kib KiB (a positive multiple of 4) of the stack it runs on:
// a VLA, probed page by page through __chkstk on Windows, one volatile byte
// written per 4 KiB page, then summed. It returns 8128 for 512 and 228480
// for 7,168.
int64_t RefStack(int64_t kib) {
    int64_t n = kib * 1024;
    volatile unsigned char buf[n];
    for (int64_t i = n - 1; i >= 0; i -= 4096) {
        buf[i] = (unsigned char)(i >> 12);
    }
    int64_t s = 0;
    for (int64_t i = n - 1; i >= 0; i -= 4096) {
        s += buf[i];
    }
    return s;
}
