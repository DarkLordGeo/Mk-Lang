# ccall

C calls from tasks. The package serves two things:

- the budget row "C call from a task: at most 5 ns over a direct call" (a `tests/bench` kernel calling an `extern` in a loop);
- the freestanding object that std's runtime tests of an extern on the system stack call (`std/task/tests_extern.mk`).

The object is `ccall.c`, freestanding C in `tests/bench`, the one directory where C is allowed.

## The object

`ccall.c` defines three functions and no data:

| Function | C | Use |
|---|---|---|
| `RefAdd(a, b: i64): i64` | `__attribute__((noinline))`, `return a + b` | the call the timing loop makes |
| `RefAddLoop(n: i64): i64` | `n` direct C-to-C calls `s = RefAdd(s, i & 7)` | the direct-call baseline; returns 35000000 for n = 10,000,000 |
| `RefStack(kib: i64): i64` | a VLA of `kib` KiB; writes byte `(i >> 12)` at offset `i` for `i = n-1, n-1-4096, ...`, then sums those bytes | uses `kib` KiB of the stack it runs on; returns 8128 for 512 and 228480 for 7,168 |

**Freestanding**, as `ccall.ref.ll` was checked (Windows 11 Pro 10.0.26300, clang 21.1.8, 2026-11-05): `clang --target=<t> -x ir -O3 -c ccall.ref.ll` exits 0 for `x86_64-pc-windows-msvc` and `x86_64-unknown-linux-gnu`; `llvm-nm -u` lists only `__chkstk` for the COFF object (LLVM's probe for the VLA, defined by every Mk executable's support pack) and nothing for the ELF object. No libc, CRT, import or constructor. On Linux the VLA is not probed (clang's default C, no `-fstack-clash-protection`); the carrier system stacks are committed read-write above their guard page, so that is enough. `"probe-stack"="inline-asm"` is not used, because on a Windows triple LLVM 21 takes its value as the probe function's name (`llvm-nm -u` showed `inline-asm` undefined).

## The program

`ccall.mk` (`Main`, at the package root; `ref/ref.mk` holds the three `extern`s and safe wrappers, the only `unsafe` module):

1. `stacks <a> <b> <c> <d>`: `RefStack(512)` from `Main`; 64 tasks that each `Yield` and then call `RefStack(512)`, summed; `RefStack(7168)` from `Main`; `RefStack(7168)` from a spawned task. Expected `stacks 8128 520192 228480 228480`. The 7 MiB call from a task fits only on a carrier's 8 MiB system stack, never in a 1 MiB task slot, so it shows the extern ran on the system stack (the runtime's rule for externs). When the object was compiled with `-flto=thin` like a release pack, the same figure also showed that ThinLTO did not inline `RefStack` past the stack switch onto the task stack, where a 7 MiB frame would fault; the `ccall.c` object is native, so no such inlining is possible. With the default carrier count the 64 calls spread over carriers; asserting which carriers ran them needs a runtime test hook that user code cannot reach, so that check stays with std's own runtime test.
2. 11 rounds `round <r> mk <ns> ref <ns> checksum 35000000 35000000`: 10,000,000 calls of `RefAdd` from `Main`'s task through the extern (one stack switch each), then `RefAddLoop(10000000)`. `(mk - ref) / 10^7` is the extern's cost over a direct call, plus Mk's loop overhead.

The program links only with the object, so it is a `tests/bench` package, not a `tests/programs` case. It is built by `go run ./tools/bench link [-mk <mk.exe>] [-o <out.exe>] tests/bench/ccall tests/bench/ccall/ccall.c`: clang 21 compiles `ccall.c` to a native object (`--target=x86_64-pc-windows-msvc19.33.0 -std=c17 -O3 -march=x86-64-v2 -ffp-contract=off -fno-math-errno -ffreestanding -c`), `llvm-nm` must show nothing undefined but the Mk runtime's symbols (here `__chkstk` only), and `mk build --release --pgo=off --cpu x86-64-v2 --no-cache` links it through tools/bench's `--llvm` shim (tests/bench/README.md, "The codegen table"). The object is native, not ThinLTO bitcode, so no inlining across the extern's stack switch can happen at all. Checked on 2026-11-07: `stacks 8128 520192 228480 228480`, every round `checksum 35000000 35000000`, `llvm-nm -u` lists only `__chkstk`. `driver.Config.ExtraObjects` is the in-process way for a Go test, such as std's runtime tests of externs; compile `ccall.c` with clang for it.

## Runs

2026-11-05, `mk` built from the tree that day, the IR object `ccall.ref.ll`, compiled with the release pack flags (`-O3 -march=x86-64-v2 -flto=thin -ffunction-sections -fdata-sections -fno-addrsig`) and added to the driver's own link job by a scratch harness outside the repository (the `OnLink` hook). Those numbers, dev and release, both targets, are not kept in the tree.
