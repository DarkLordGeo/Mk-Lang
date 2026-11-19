# Benchmark results

Written by `go run ./tools/bench publish` on 2026-11-09. Do not edit by hand: every number here comes from `tests/bench/results/2026-11-09/results.json`, whose raw hyperfine exports are beside it. The contract these numbers are held to is `tests/bench/README.md` (plan/06-m6.md section 4.1).

**Run 1** (rows intsum, mandelbrot, matmul, nbody, sieve, spectralnorm)

| Fact | Value |
|---|---|
| Commit | `0551e733f3ce1e0e77a8b95ee5e2f24077ce36e8` (working tree had uncommitted changes to tracked files) |
| mk | `C:\work\bench\mk-bf1.exe`, sha256 `d626abb8e8c9f2a3` |
| Started | 2026-11-09T03:25:04+04:00 |
| Machine | AMD Ryzen 7 9800X3D 8-Core Processor, windows Microsoft Windows [Version 10.0.26300.9818] |
| Power scheme | Power Scheme GUID: 8c5e7fda-e8bf-4a96-9a85-a6e23a8c635c  (High performance) |
| Defender real-time protection | False |
| Affinity | 0x4 (one logical CPU, set on tools/bench and inherited) |
| GOGC | 100 |
| Program environment | GOGC=100 GOMAXPROCS=1 set by tools/bench on every program it runs; GODEBUG, GOMEMLIMIT, GOTRACEBACK, MK_CARRIERS, MK_IO, MK_PROFILE_DIR, MK_INHERIT and MK_TEST_* cleared |
| Timing | hyperfine `-N --warmup 3 --runs 10`, 3 sweeps in rotated column order, pooled; RSD above 3% reruns once, then the cell is marked noisy |
| Other compilers and test binaries running at the start | none |

**Run 2** (rows binarytrees, fannkuch, fmtints, jsonscan, perf-codegen/keywords, sortints, std-dyn/dispatch, std-dyn/dispatchalt)

| Fact | Value |
|---|---|
| Commit | `0551e733f3ce1e0e77a8b95ee5e2f24077ce36e8` (working tree had uncommitted changes to tracked files) |
| mk | `C:\work\bench\mk-bf1.exe`, sha256 `d626abb8e8c9f2a3` |
| Started | 2026-11-09T04:09:09+04:00 |
| Machine | AMD Ryzen 7 9800X3D 8-Core Processor, windows Microsoft Windows [Version 10.0.26300.9818] |
| Power scheme | Power Scheme GUID: 8c5e7fda-e8bf-4a96-9a85-a6e23a8c635c  (High performance) |
| Defender real-time protection | False |
| Affinity | 0x4 (one logical CPU, set on tools/bench and inherited) |
| GOGC | 100 |
| Program environment | GOGC=100 GOMAXPROCS=1 set by tools/bench on every program it runs; GODEBUG, GOMEMLIMIT, GOTRACEBACK, MK_CARRIERS, MK_IO, MK_PROFILE_DIR, MK_INHERIT and MK_TEST_* cleared |
| Timing | hyperfine `-N --warmup 3 --runs 10`, 3 sweeps in rotated column order, pooled; RSD above 3% reruns once, then the cell is marked noisy |
| Other compilers and test binaries running at the start | none |

**Run 3** (rows hello)

| Fact | Value |
|---|---|
| Commit | `1308f7b261fc75899344e8155aaec7ad9bf2b0cc` (working tree had uncommitted changes to tracked files) |
| mk | `C:\work\bench\mk-bf1.exe`, sha256 `d626abb8e8c9f2a3` |
| Started | 2026-11-09T05:09:52+04:00 |
| Machine | AMD Ryzen 7 9800X3D 8-Core Processor, windows Microsoft Windows [Version 10.0.26300.9818] |
| Power scheme | Power Scheme GUID: 8c5e7fda-e8bf-4a96-9a85-a6e23a8c635c  (High performance) |
| Defender real-time protection | False |
| Affinity | 0x4 (one logical CPU, set on tools/bench and inherited) |
| GOGC | 100 |
| Program environment | GOGC=100 GOMAXPROCS=1 set by tools/bench on every program it runs; GODEBUG, GOMEMLIMIT, GOTRACEBACK, MK_CARRIERS, MK_IO, MK_PROFILE_DIR, MK_INHERIT and MK_TEST_* cleared |
| Timing | hyperfine `-N --warmup 3 --runs 10`, 3 sweeps in rotated column order, pooled; RSD above 3% reruns once, then the cell is marked noisy |
| Other compilers and test binaries running at the start | none |

The machine was shared during this run (2 of its 7 timed cells stayed noisy after their rerun), so these numbers are a first table, not the 1.0 one: their medians cannot carry the 1.25 bar. `bench-final` publishes that one on a quiet machine.

## Columns

| Column | Toolchain | Bounds | Overflow | Panic | LTO | Linker | CRT | std at x86-64-v2 |
|---|---|---|---|---|---|---|---|---|
| mk | mk built from 0551e733f3ce1e0e77a8b95ee5e2f24077ce36e8 (2026-11-08T22:44:19Z) with uncommitted changes, LLVM 21.1.8 from C:\Program Files\LLVM\bin (default location) | checked | trap | abort (exit 101) | ThinLTO | lld-link | none (Mk runtime, kernel32 only) | yes (built with the program) |
| mk-wrap | mk built from 0551e733f3ce1e0e77a8b95ee5e2f24077ce36e8 (2026-11-08T22:44:19Z) with uncommitted changes, LLVM 21.1.8 from C:\Program Files\LLVM\bin (default location) | checked | wrap (`+%` operators) | abort (exit 101) | ThinLTO | lld-link | none (Mk runtime, kernel32 only) | yes (built with the program) |
| c | clang version 21.1.8 | none | UB (signed), wrap (unsigned) | none | none (single TU) | lld-link | static UCRT (-fms-runtime-lib=static) | no (prebuilt UCRT) |
| rust | rustc 1.99.1 (58cf8f9c3 2026-10-13), LLVM 23.1.1 | checked | wrap (overflow-checks off at opt-level=3) | abort | fat | link.exe (rustc default) | static (+crt-static) | no (prebuilt std at x86-64) |
| rust-ovf | rustc 1.99.1 (58cf8f9c3 2026-10-13), LLVM 23.1.1 | checked | trap (-C overflow-checks=on) | abort | fat | link.exe (rustc default) | static (+crt-static) | no (prebuilt std at x86-64) |
| zig-fast | zig 0.17.0, clang version 22.1.8 | none | UB (ReleaseFast) | none | none (one compilation unit) | zig (lld) | none (x86_64-windows-gnu, no -lc) | yes (std compiled with the program) |
| zig-safe | zig 0.17.0, clang version 22.1.8 | checked | trap (ReleaseSafe) | abort with trace | none (one compilation unit) | zig (lld) | none (x86_64-windows-gnu, no -lc) | yes (std compiled with the program) |
| go | go version go1.27.3 windows/amd64 | checked | wrap | panic (exit 2) | n/a (gc whole program) | go | none (Go runtime) | yes (GOAMD64=v2 rebuilds std) |

The build command of every cell is in `results.json` (`builds`). Only Mk and C share an LLVM (clang 21); against Rust (its own LLVM), Zig (its bundled LLVM) and Go (gc), a row compares whole toolchains and their std, not code generators alone (B7). On Windows, Mk's release path is ThinLTO through lld-link, which runs no SLP vectorizer today (STATUS.md, perf-codegen's item).

## Table "same"

Same algorithm: each row is one reference algorithm written in every language with the same data structures, loop structure and arithmetic types (its `README.md`). Whole programs, timed from outside. Cells: median wall time, and the ratio of medians to the fastest cell of the row with its 95% bootstrap interval.

| Row | Overflow class | mk | mk-wrap | c | rust | rust-ovf | zig-fast | zig-safe | go |
|---|---|---|---|---|---|---|---|---|---|
| intsum | never | 1.120 s, x1.00 | 1.121 s, x1.00 | 1.120 s, x1.00 | 1.120 s, x1.00 | 2.257 s, x2.02 | 2.228 s, x1.99 | 2.236 s, x2.00 | 2.255 s, x2.01 |
| mandelbrot | never | 1.219 s, x1.01 | not written: the hot loop is float; its only integer arithmetic is the iteration counter | 1.220 s, x1.01 | 1.210 s, x1.00 | 1.210 s, x1.00 | 1.216 s, x1.01 | 1.215 s, x1.00 | 1.263 s, x1.04 |
| matmul | never | 1.160 s, x1.03 | not written: only index arithmetic is integer; a wrapping column is not needed for the budgets | 1.131 s, x1.00 | 1.154 s, x1.02 | 1.162 s, x1.03 | 2.002 s, x1.77 | 2.383 s, x2.11 | 3.898 s, x3.45 |
| nbody | never | 1.193 s, x1.00 | not written: the hot loop has no integer arithmetic beyond loop counters, so a wrapping column would equal mk | 1.754 s, x1.47 | 1.827 s, x1.53 | 1.826 s, x1.53 | 1.215 s, x1.02 | 1.313 s, x1.10 | 2.425 s, x2.03 |
| sieve | never | 1.126 s, x1.01 | 1.118 s, x1.00 | 1.204 s, x1.08 | 1.197 s, x1.07 | 1.272 s, x1.14 | 1.202 s, x1.08 | 1.132 s, x1.01 | 1.300 s, x1.16 |
| spectralnorm | never | 1.080 s, x1.00 | not written: not needed for the budgets; A(i, j)'s integer arithmetic is checked in mk | 1.100 s, x1.02 | 1.091 s, x1.01 | 1.105 s, x1.02 | 1.103 s, x1.02 | 1.100 s, x1.02 | 1.093 s, x1.01 |
| binarytrees | never | 2.519 s, x1.23 | not written: not needed for the budgets; written if the checked column shows an overflow-check cost | 2.049 s, x1.00 | 2.075 s, x1.01 | 2.696 s, x1.32 | 2.137 s, x1.04 | 2.643 s, x1.29 | 2.501 s, x1.22 |
| fannkuch | never | 1.740 s, x1.06 | not written: not needed for the budgets; written if the checked column shows an overflow-check cost | 1.777 s, x1.09 | 1.771 s, x1.08 | 1.778 s, x1.09 | 1.814 s, x1.11 | 1.888 s, x1.15 | 1.636 s, x1.00 |
| fmtints | never | 1.267 s, x1.18 | not written: not needed for the budgets; written if the checked column shows an overflow-check cost | 1.070 s, x1.00 | 1.123 s, x1.05 | 1.216 s, x1.14 | 1.106 s, x1.03 | 1.391 s, x1.30 | 1.383 s, x1.29 |
| jsonscan | wraps | 1.054 s, x1.03 | not written: the hash already uses the wrapping operators in every column (overflow class wraps) | 1.135 s, x1.11 | 1.056 s, x1.03 | 1.214 s, x1.19 | 1.021 s, x1.00 | 1.178 s, x1.15 | 1.202 s, x1.18 |
| perf-codegen/keywords | wraps | 830.0 ms, x1.01 | 830.2 ms, x1.01 | 822.0 ms, x1.00 | 1.233 s, x1.50 | 1.216 s, x1.48 | 1.766 s, x2.15 | 1.967 s, x2.39 | 1.173 s, x1.43 |
| sortints | never | 1.219 s, x1.03 | not written: the sort does only index arithmetic; a wrapping column is not needed for the budgets | 1.187 s, x1.00 | 1.236 s, x1.04 | 1.246 s, x1.05 | 1.188 s, x1.00 | 1.305 s, x1.10 | 1.273 s, x1.07 |
| std-dyn/dispatch | wraps | 2.680 s, x1.02 | not written: the algorithm already uses the wrapping operators; there is no checked arithmetic to remove | 2.637 s, x1.00 | 2.771 s, x1.05 | 2.771 s, x1.05 | 2.792 s, x1.06 | 2.715 s, x1.03 | 2.855 s, x1.08 |
| std-dyn/dispatchalt | wraps | 711.4 ms, x1.20 | not written: the algorithm already uses the wrapping operators; there is no checked arithmetic to remove | 591.7 ms, x1.00 | 652.1 ms, x1.10 | 652.0 ms, x1.10 | 651.5 ms, x1.10 | 698.9 ms, x1.18 | 653.5 ms, x1.10 |

### intsum

Sum of n int64 values in 0..1023, repeated rounds times with a write-back between passes (README.md). Arguments `1000000 12000 1`, checksum `6136887200936`.

| Column | Class | Median | Min | MAD | RSD | Runs | Ratio to best [95%] | Ratio to best of its class [95%] |
|---|---|---|---|---|---|---|---|---|
| mk | checked | 1.120 s | 1.116 s | 1.9 ms | 0.6% | 30 | 1.00 [0.99, 1.01] | 1.00 [0.99, 1.01] |
| mk-wrap | bounds | 1.121 s | 1.117 s | 1.0 ms | 0.6% | 30 | 1.00 [0.99, 1.01] | 1.00 [0.99, 1.01] |
| c | unchecked | 1.120 s | 1.117 s | 3.0 ms | 0.7% | 30 | 1.00 [0.99, 1.01] | 1.00 [0.99, 1.01] |
| rust | bounds | 1.120 s | 1.117 s | 2.3 ms | 0.7% | 30 | 1.00 [0.99, 1.01] | 1.00 [0.99, 1.01] |
| rust-ovf | checked | 2.257 s | 2.233 s | 10.6 ms | 0.7% | 30 | 2.02 [2.00, 2.02] | 2.02 [2.00, 2.02] |
| zig-fast | unchecked | 2.228 s | 2.223 s | 3.5 ms | 0.4% | 30 | 1.99 [1.98, 2.00] | 1.99 [1.98, 2.00] |
| zig-safe | checked | 2.236 s | 2.225 s | 9.7 ms | 2.5% | 30 | 2.00 [1.98, 2.01] | 2.00 [1.98, 2.01] |
| go | bounds | 2.255 s | 2.234 s | 10.9 ms | 1.5% | 30 | 2.01 [2.00, 2.02] | 2.01 [2.00, 2.02] |


### mandelbrot

Scalar Mandelbrot over an n by n grid, at most 50 iterations per point; the checksum is the total iteration count (README.md). Arguments `5600`, checksum `746474893`.

| Column | Class | Median | Min | MAD | RSD | Runs | Ratio to best [95%] | Ratio to best of its class [95%] |
|---|---|---|---|---|---|---|---|---|
| mk | checked | 1.219 s | 1.216 s | 1.2 ms | 0.5% | 30 | 1.01 [1.01, 1.01] | 1.01 [1.01, 1.01] |
| c | unchecked | 1.220 s | 1.218 s | 1.6 ms | 0.2% | 30 | 1.01 [1.01, 1.01] | 1.00 [1.00, 1.00] |
| rust | bounds | 1.210 s | 1.209 s | 953 us | 0.2% | 30 | 1.00 [1.00, 1.00] | 1.00 [1.00, 1.00] |
| rust-ovf | checked | 1.210 s | 1.208 s | 1.3 ms | 0.3% | 30 | 1.00 [1.00, 1.00] | 1.00 [1.00, 1.00] |
| zig-fast | unchecked | 1.216 s | 1.215 s | 776 us | 0.2% | 30 | 1.01 [1.00, 1.01] | 1.00 [1.00, 1.00] |
| zig-safe | checked | 1.215 s | 1.215 s | 408 us | 0.7% | 30 | 1.00 [1.00, 1.01] | 1.00 [1.00, 1.01] |
| go | bounds | 1.263 s | 1.262 s | 959 us | 0.3% | 30 | 1.04 [1.04, 1.05] | 1.04 [1.04, 1.05] |


### matmul

Dense f64 matrix multiply, i-k-j loop over flat row-major arrays, repeated rounds times into one result; exact by construction (README.md). Arguments `512 76 1`, checksum `165617122137808`.

| Column | Class | Median | Min | MAD | RSD | Runs | Ratio to best [95%] | Ratio to best of its class [95%] |
|---|---|---|---|---|---|---|---|---|
| mk | checked | 1.160 s | 1.154 s | 5.4 ms | 0.5% | 30 | 1.03 [1.02, 1.03] | 1.00 [1.00, 1.00] |
| c | unchecked | 1.131 s | 1.126 s | 3.5 ms | 0.4% | 30 | 1.00 [1.00, 1.00] | 1.00 [1.00, 1.00] |
| rust | bounds | 1.154 s | 1.149 s | 3.9 ms | 0.4% | 30 | 1.02 [1.02, 1.02] | 1.00 [1.00, 1.00] |
| rust-ovf | checked | 1.162 s | 1.158 s | 2.7 ms | 0.3% | 30 | 1.03 [1.02, 1.03] | 1.00 [1.00, 1.00] |
| zig-fast | unchecked | 2.002 s | 1.996 s | 2.2 ms | 0.5% | 30 | 1.77 [1.76, 1.77] | 1.77 [1.76, 1.77] |
| zig-safe | checked | 2.383 s | 2.353 s | 11.5 ms | 0.7% | 30 | 2.11 [2.10, 2.11] | 2.05 [2.04, 2.06] |
| go | bounds | 3.898 s | 3.892 s | 4.7 ms | 0.2% | 30 | 3.45 [3.44, 3.45] | 3.38 [3.37, 3.39] |


### nbody

Five-body leapfrog in f64, structure of arrays, steps of 0.0078125; the checksum is the final energy times 1e9 truncated to an integer (README.md). Arguments `50000000`, checksum `-110176510`.

| Column | Class | Median | Min | MAD | RSD | Runs | Ratio to best [95%] | Ratio to best of its class [95%] |
|---|---|---|---|---|---|---|---|---|
| mk | checked | 1.193 s | 1.192 s | 688 us | 0.1% | 30 | 1.00 [1.00, 1.00] | 1.00 [1.00, 1.00] |
| c | unchecked | 1.754 s | 1.751 s | 1.9 ms | 0.1% | 30 | 1.47 [1.47, 1.47] | 1.44 [1.44, 1.44] |
| rust | bounds | 1.827 s | 1.824 s | 1.4 ms | 0.2% | 30 | 1.53 [1.53, 1.53] | 1.00 [1.00, 1.00] |
| rust-ovf | checked | 1.826 s | 1.824 s | 965 us | 0.1% | 30 | 1.53 [1.53, 1.53] | 1.53 [1.53, 1.53] |
| zig-fast | unchecked | 1.215 s | 1.214 s | 644 us | 0.1% | 30 | 1.02 [1.02, 1.02] | 1.00 [1.00, 1.00] |
| zig-safe | checked | 1.313 s | 1.312 s | 480 us | 0.1% | 30 | 1.10 [1.10, 1.10] | 1.10 [1.10, 1.10] |
| go | bounds | 2.425 s | 2.423 s | 1.4 ms | 0.1% | 30 | 2.03 [2.03, 2.03] | 1.33 [1.33, 1.33] |


### sieve

Sieve of Eratosthenes over n + 1 byte flags, rounds times; the checksum is rounds times the count of primes up to n (README.md). Arguments `20000000 46`, checksum `58447922`.

| Column | Class | Median | Min | MAD | RSD | Runs | Ratio to best [95%] | Ratio to best of its class [95%] |
|---|---|---|---|---|---|---|---|---|
| mk | checked | 1.126 s | 1.120 s | 2.5 ms | 0.3% | 30 | 1.01 [1.01, 1.01] | 1.00 [1.00, 1.00] |
| mk-wrap | bounds | 1.118 s | 1.111 s | 1.8 ms | 0.2% | 30 | 1.00 [1.00, 1.00] | 1.00 [1.00, 1.00] |
| c | unchecked | 1.204 s | 1.188 s | 2.3 ms | 0.5% | 30 | 1.08 [1.08, 1.08] | 1.00 [1.00, 1.00] |
| rust | bounds | 1.197 s | 1.195 s | 908 us | 0.2% | 30 | 1.07 [1.07, 1.07] | 1.07 [1.07, 1.07] |
| rust-ovf | checked | 1.272 s | 1.254 s | 6.8 ms | 0.8% | 30 | 1.14 [1.13, 1.14] | 1.13 [1.12, 1.14] |
| zig-fast | unchecked | 1.202 s | 1.196 s | 2.4 ms | 0.3% | 30 | 1.08 [1.07, 1.08] | 1.00 [1.00, 1.00] |
| zig-safe | checked | 1.132 s | 1.112 s | 10.6 ms | 1.3% | 30 | 1.01 [1.01, 1.02] | 1.01 [1.00, 1.01] |
| go | bounds | 1.300 s | 1.291 s | 1.5 ms | 0.3% | 30 | 1.16 [1.16, 1.16] | 1.16 [1.16, 1.16] |


### spectralnorm

Spectral norm of the infinite matrix A(i,j) = 1/((i+j)(i+j+1)/2+i+1) by 10 power iterations of AtA (the Benchmarks Game's spectral-norm, scalar) (README.md). Arguments `6000`, checksum `1274224152`.

| Column | Class | Median | Min | MAD | RSD | Runs | Ratio to best [95%] | Ratio to best of its class [95%] |
|---|---|---|---|---|---|---|---|---|
| mk | checked | 1.080 s | 1.077 s | 710 us | 0.1% | 30 | 1.00 [1.00, 1.00] | 1.00 [1.00, 1.00] |
| c | unchecked | 1.100 s | 1.096 s | 606 us | 0.1% | 30 | 1.02 [1.02, 1.02] | 1.00 [1.00, 1.00] |
| rust | bounds | 1.091 s | 1.089 s | 676 us | 0.1% | 30 | 1.01 [1.01, 1.01] | 1.00 [1.00, 1.00] |
| rust-ovf | checked | 1.105 s | 1.103 s | 632 us | 0.2% | 30 | 1.02 [1.02, 1.02] | 1.02 [1.02, 1.02] |
| zig-fast | unchecked | 1.103 s | 1.101 s | 607 us | 0.5% | 30 | 1.02 [1.02, 1.02] | 1.00 [1.00, 1.00] |
| zig-safe | checked | 1.100 s | 1.098 s | 917 us | 0.1% | 30 | 1.02 [1.02, 1.02] | 1.02 [1.02, 1.02] |
| go | bounds | 1.093 s | 1.090 s | 837 us | 0.2% | 30 | 1.01 [1.01, 1.01] | 1.00 [1.00, 1.00] |


### binarytrees

binary-trees with one bump-allocated buffer of u32-index nodes in every language (B9); the sum of every tree check (README.md). Arguments `22`, checksum `1361750702`.

| Column | Class | Median | Min | MAD | RSD | Runs | Ratio to best [95%] | Ratio to best of its class [95%] |
|---|---|---|---|---|---|---|---|---|
| mk | checked | 2.519 s | 2.505 s | 4.5 ms | 0.3% | 30 | 1.23 [1.23, 1.23] | 1.00 [1.00, 1.00] |
| c | unchecked | 2.049 s | 2.037 s | 2.7 ms | 0.5% | 30 | 1.00 [1.00, 1.00] | 1.00 [1.00, 1.00] |
| rust | bounds | 2.075 s | 2.070 s | 2.5 ms | 1.1% | 30 | 1.01 [1.01, 1.01] | 1.00 [1.00, 1.00] |
| rust-ovf | checked | 2.696 s | 2.690 s | 2.9 ms | 0.5% | 30 | 1.32 [1.31, 1.32] | 1.07 [1.07, 1.07] |
| zig-fast | unchecked | 2.137 s | 2.128 s | 4.5 ms | 0.5% | 30 | 1.04 [1.04, 1.05] | 1.04 [1.04, 1.05] |
| zig-safe | checked | 2.643 s | 2.636 s | 2.9 ms | 0.2% | 30 | 1.29 [1.29, 1.29] | 1.05 [1.05, 1.05] |
| go | bounds | 2.501 s | 2.485 s | 10.5 ms | 0.5% | 30 | 1.22 [1.22, 1.23] | 1.21 [1.20, 1.21] |


### fannkuch

fannkuch-redux: every permutation of n, counting prefix reversals; checksum * 1000 + maxflips (README.md). Arguments `11`, checksum `556355051`.

| Column | Class | Median | Min | MAD | RSD | Runs | Ratio to best [95%] | Ratio to best of its class [95%] |
|---|---|---|---|---|---|---|---|---|
| mk | checked | 1.740 s | 1.737 s | 834 us | 0.1% | 30 | 1.06 [1.06, 1.07] | 1.00 [1.00, 1.00] |
| c | unchecked | 1.777 s | 1.765 s | 733 us | 0.3% | 30 | 1.09 [1.08, 1.09] | 1.00 [1.00, 1.00] |
| rust | bounds | 1.771 s | 1.746 s | 4.5 ms | 0.7% | 30 | 1.08 [1.08, 1.09] | 1.08 [1.08, 1.09] |
| rust-ovf | checked | 1.778 s | 1.755 s | 5.8 ms | 0.5% | 30 | 1.09 [1.08, 1.09] | 1.02 [1.02, 1.02] |
| zig-fast | unchecked | 1.814 s | 1.808 s | 2.1 ms | 0.2% | 30 | 1.11 [1.11, 1.11] | 1.02 [1.02, 1.02] |
| zig-safe | checked | 1.888 s | 1.879 s | 1.0 ms | 0.1% | 30 | 1.15 [1.15, 1.16] | 1.08 [1.08, 1.09] |
| go | bounds | 1.636 s | 1.620 s | 5.0 ms | 0.4% | 30 | 1.00 [1.00, 1.00] | 1.00 [1.00, 1.00] |


### fmtints

Integers in [-1e9, 1e9] to decimal text with the textbook digit loop, then the byte sum, rounds times (README.md). Arguments `1000000 100 1`, checksum `49968808800`.

| Column | Class | Median | Min | MAD | RSD | Runs | Ratio to best [95%] | Ratio to best of its class [95%] |
|---|---|---|---|---|---|---|---|---|
| mk | checked | 1.267 s | 1.267 s | 509 us | 0.1% | 30 | 1.18 [1.18, 1.19] | 1.04 [1.04, 1.04] |
| c | unchecked | 1.070 s | 1.068 s | 692 us | 0.2% | 30 | 1.00 [1.00, 1.00] | 1.00 [1.00, 1.00] |
| rust | bounds | 1.123 s | 1.122 s | 785 us | 0.2% | 30 | 1.05 [1.05, 1.05] | 1.00 [1.00, 1.00] |
| rust-ovf | checked | 1.216 s | 1.214 s | 691 us | 0.2% | 30 | 1.14 [1.14, 1.14] | 1.00 [1.00, 1.00] |
| zig-fast | unchecked | 1.106 s | 1.103 s | 1.4 ms | 0.2% | 30 | 1.03 [1.03, 1.03] | 1.03 [1.03, 1.03] |
| zig-safe | checked | 1.391 s | 1.385 s | 2.7 ms | 0.5% | 30 | 1.30 [1.30, 1.30] | 1.14 [1.14, 1.15] |
| go | bounds | 1.383 s | 1.378 s | 2.4 ms | 0.3% | 30 | 1.29 [1.29, 1.30] | 1.23 [1.23, 1.23] |


### jsonscan

A JSON tokenizer over a 32 MB generated document, hashing every token, repeated rounds times (README.md). Arguments `{data} 52`, checksum `7010378328625811788`.

| Column | Class | Median | Min | MAD | RSD | Runs | Ratio to best [95%] | Ratio to best of its class [95%] |
|---|---|---|---|---|---|---|---|---|
| mk | bounds | 1.054 s | 1.051 s | 1.4 ms | 0.3% | 30 | 1.03 [1.03, 1.03] | 1.00 [1.00, 1.00] |
| c | unchecked | 1.135 s | 1.128 s | 3.1 ms | 0.4% | 30 | 1.11 [1.11, 1.11] | 1.11 [1.11, 1.11] |
| rust | bounds | 1.056 s | 1.049 s | 3.6 ms | 0.5% | 30 | 1.03 [1.03, 1.04] | 1.00 [1.00, 1.00] |
| rust-ovf | bounds | 1.214 s | 1.201 s | 4.7 ms | 0.5% | 30 | 1.19 [1.18, 1.19] | 1.15 [1.15, 1.15] |
| zig-fast | unchecked | 1.021 s | 1.017 s | 2.4 ms | 0.3% | 30 | 1.00 [1.00, 1.00] | 1.00 [1.00, 1.00] |
| zig-safe | bounds | 1.178 s | 1.171 s | 2.8 ms | 0.4% | 30 | 1.15 [1.15, 1.16] | 1.12 [1.12, 1.12] |
| go | bounds | 1.202 s | 1.195 s | 3.3 ms | 0.3% | 30 | 1.18 [1.17, 1.18] | 1.14 [1.14, 1.14] |


### perf-codegen/keywords

Classify n generated words (a quarter keywords, a quarter keywords with the last letter replaced, half random lowercase words of 1 to 8 letters), views of one buffer, by a chain of equality tests against 16 keyword literals, as a lexer's keyword match; rounds times, the wrapping sum of class times (i mod 1000 + 1) (README.md). Arguments `2000000 50 1`, checksum `110246408250`. The fastest cell ran under 1 s, below the contract's floor; the row's size is to be raised.

| Column | Class | Median | Min | MAD | RSD | Runs | Ratio to best [95%] | Ratio to best of its class [95%] |
|---|---|---|---|---|---|---|---|---|
| mk | bounds | 830.0 ms | 827.6 ms | 982 us | 0.2% | 30 | 1.01 [1.01, 1.01] | 1.00 [1.00, 1.00] |
| mk-wrap | bounds | 830.2 ms | 827.9 ms | 780 us | 0.2% | 30 | 1.01 [1.01, 1.01] | 1.00 [1.00, 1.00] |
| c | unchecked | 822.0 ms | 820.5 ms | 779 us | 0.1% | 30 | 1.00 [1.00, 1.00] | 1.00 [1.00, 1.00] |
| rust | bounds | 1.233 s | 1.233 s | 455 us | 0.3% | 30 | 1.50 [1.50, 1.50] | 1.49 [1.49, 1.49] |
| rust-ovf | bounds | 1.216 s | 1.214 s | 999 us | 0.4% | 30 | 1.48 [1.48, 1.48] | 1.47 [1.46, 1.47] |
| zig-fast | unchecked | 1.766 s | 1.750 s | 8.0 ms | 0.6% | 30 | 2.15 [2.14, 2.15] | 2.15 [2.14, 2.15] |
| zig-safe | bounds | 1.967 s | 1.963 s | 1.5 ms | 0.4% | 30 | 2.39 [2.39, 2.39] | 2.37 [2.37, 2.37] |
| go | bounds | 1.173 s | 1.169 s | 2.3 ms | 0.3% | 30 | 1.43 [1.43, 1.43] | 1.41 [1.41, 1.42] |

- Mk: Classify is 16 tests `w == "kw"` (str's ==); C: `n == len && memcmp(p, kw, len) == 0`, what a C lexer writes; Rust `w == "kw"` on &str, Zig `std.mem.eql(u8, w, "kw")`, Go `w == "kw"`. Each sees the literal's length on the compare's path.
- Per word in the timed loop, as written in the source (what each optimizer removes is not read): Mk checks both offsets' bounds, the range, the two UTF-8 boundary bytes, the overflow of i + 1 and of i % 1000 + 1, and the Cast[u64] range; mk-wrap (mkwrap/, B4) writes those three wrapping (+% and Trunc[u64]) and keeps the rest; Rust checks the bounds, the range and both char boundaries (rust-ovf also the two overflows); Zig ReleaseSafe the bounds, the range, the overflow of i + 1 and of @rem(i, 1000) + 1, and the range of its five @intCast; Go the bounds and the range; C and Zig ReleaseFast nothing (README.md, Cells).
- Written by the perf-codegen lane (status/perf-codegen.md, session 4) for the change of str == to a branch on the lengths; the Rust, Zig and Go columns and C's int64_t types and byte-loop copy in session 5 (review findings PCG-2, PCG-3).

### sortints

Go 1.27's pdqsortOrdered written out for int64 in every language, over four patterns of n keys per round (README.md). Arguments `1000000 16 1`, checksum `8192754217017821`.

| Column | Class | Median | Min | MAD | RSD | Runs | Ratio to best [95%] | Ratio to best of its class [95%] |
|---|---|---|---|---|---|---|---|---|
| mk | checked | 1.219 s | 1.212 s | 2.8 ms | 0.4% | 30 | 1.03 [1.03, 1.03] | 1.00 [1.00, 1.00] |
| c | unchecked | 1.187 s | 1.183 s | 1.4 ms | 0.2% | 30 | 1.00 [1.00, 1.00] | 1.00 [1.00, 1.00] |
| rust | bounds | 1.236 s | 1.233 s | 1.6 ms | 0.2% | 30 | 1.04 [1.04, 1.04] | 1.00 [1.00, 1.00] |
| rust-ovf | checked | 1.246 s | 1.243 s | 1.6 ms | 0.2% | 30 | 1.05 [1.05, 1.05] | 1.02 [1.02, 1.02] |
| zig-fast | unchecked | 1.188 s | 1.184 s | 1.2 ms | 0.3% | 30 | 1.00 [1.00, 1.00] | 1.00 [1.00, 1.00] |
| zig-safe | checked | 1.305 s | 1.303 s | 649 us | 0.1% | 30 | 1.10 [1.10, 1.10] | 1.07 [1.07, 1.07] |
| go | bounds | 1.273 s | 1.269 s | 1.7 ms | 0.2% | 30 | 1.07 [1.07, 1.07] | 1.03 [1.03, 1.03] |


### std-dyn/dispatch

n dynamic calls through a two-element array of {context pointer, inline function pointer} pairs (Mk: gen dyn.Of's StepDyn) over an Add and a Xor, the target picked by bit 0 of a splitmix64 stream and the argument its upper 63 bits; the results summed with wrapping arithmetic (README.md). Arguments `500000000 1`, checksum `7530917614485774527`.

| Column | Class | Median | Min | MAD | RSD | Runs | Ratio to best [95%] | Ratio to best of its class [95%] |
|---|---|---|---|---|---|---|---|---|
| mk | bounds | 2.680 s | 2.678 s | 1.2 ms | 0.1% | 30 | 1.02 [1.02, 1.02] | 1.00 [1.00, 1.00] |
| c | unchecked | 2.637 s | 2.634 s | 1.8 ms | 0.1% | 30 | 1.00 [1.00, 1.00] | 1.00 [1.00, 1.00] |
| rust | bounds | 2.771 s | 2.769 s | 1.3 ms | 0.2% | 30 | 1.05 [1.05, 1.05] | 1.03 [1.03, 1.03] |
| rust-ovf | bounds | 2.771 s | 2.769 s | 1.8 ms | 0.2% | 30 | 1.05 [1.05, 1.05] | 1.03 [1.03, 1.04] |
| zig-fast | unchecked | 2.792 s | 2.790 s | 957 us | 0.1% | 30 | 1.06 [1.06, 1.06] | 1.06 [1.06, 1.06] |
| zig-safe | bounds | 2.715 s | 2.713 s | 877 us | 0.2% | 30 | 1.03 [1.03, 1.03] | 1.01 [1.01, 1.01] |
| go | bounds | 2.855 s | 2.853 s | 875 us | 0.3% | 30 | 1.08 [1.08, 1.08] | 1.07 [1.07, 1.07] |

- Dispatch values, one data structure (C's {context pointer, function pointer} pair, the fn inline): C {void *self; fn pointer}; Zig {*anyopaque, *const fn}; Rust {&mut Acc, fn pointer} and Go {*Acc, func value}, both objects one Acc type since safe Rust and Go cannot erase a pointer's type; Mk gen dyn.Of(Step)'s StepDyn (a dyn.Ref, the address and the type key, and the fn inline; the adapter compares the key before the direct call). Go's func value points to a closure record, one dependent load more, which Go cannot avoid. Review finding DYN-6 moved Rust (&mut dyn), Zig (vtable pointer) and Go (interface) off their native vtable-pointer values, which are a different data structure.
- Written by the std-dyn lane (status/std-dyn.md). Checksums: from the earlier columns' verified runs (all columns agreed, 2026-11-07) and a Go reference of the pseudo-code; the reworked columns are reverified through tools/bench verify (status/std-dyn.md audit table).

### std-dyn/dispatchalt

dispatch's loop with the target alternating (objs[i & 1]), so the indirect branch is predicted and the per-call overhead shows; the same data structures, argument stream and wrapping arithmetic (README.md). Arguments `500000000 1`, checksum `5376492962469361624`. The fastest cell ran under 1 s, below the contract's floor; the row's size is to be raised.

| Column | Class | Median | Min | MAD | RSD | Runs | Ratio to best [95%] | Ratio to best of its class [95%] |
|---|---|---|---|---|---|---|---|---|
| mk | bounds | 711.4 ms | 710.6 ms | 540 us | 1.1% | 30 | 1.20 [1.20, 1.20] | 1.09 [1.09, 1.09] |
| c | unchecked | 591.7 ms | 591.2 ms | 309 us | 0.1% | 30 | 1.00 [1.00, 1.00] | 1.00 [1.00, 1.00] |
| rust | bounds | 652.1 ms | 651.3 ms | 358 us | 0.2% | 30 | 1.10 [1.10, 1.10] | 1.00 [1.00, 1.00] |
| rust-ovf | bounds | 652.0 ms | 651.3 ms | 255 us | 0.1% | 30 | 1.10 [1.10, 1.10] | 1.00 [1.00, 1.00] |
| zig-fast | unchecked | 651.5 ms | 650.5 ms | 342 us | 0.3% | 30 | 1.10 [1.10, 1.10] | 1.10 [1.10, 1.10] |
| zig-safe | bounds | 698.9 ms | 697.8 ms | 338 us | 0.4% | 30 | 1.18 [1.18, 1.18] | 1.07 [1.07, 1.07] |
| go | bounds | 653.5 ms | 652.8 ms | 429 us | 0.4% | 30 | 1.10 [1.10, 1.11] | 1.00 [1.00, 1.00] |

- The columns of ../dispatch (the same pairs and the same Mk StepDyn), with the index i & 1 (review finding DYN-7: the random target's mispredicts, about the same in every column, hide the adapter's key compare and frame).
- Written by the std-dyn lane (status/std-dyn.md). Checksums from a Go reference of the pseudo-code (seed 1), checked against every column by tools/bench verify.

### Mk against C (the codegen metric, same LLVM)

| Row | mk / c [95%] | Against 1.10 | mk-wrap / c [95%] |
|---|---|---|---|
| intsum | 1.00 [0.99, 1.01] | within | 1.00 [1.00, 1.01] |
| mandelbrot | 1.00 [1.00, 1.00] | within | - |
| matmul | 1.03 [1.02, 1.03] | within | - |
| nbody | 0.68 [0.68, 0.68] | within | - |
| sieve | 0.93 [0.93, 0.94] | within | 0.93 [0.93, 0.93] |
| spectralnorm | 0.98 [0.98, 0.98] | within | - |
| binarytrees | 1.23 [1.23, 1.23] | over | - |
| fannkuch | 0.98 [0.98, 0.98] | within | - |
| fmtints | 1.18 [1.18, 1.19] | over | - |
| jsonscan | 0.93 [0.93, 0.93] | within | - |
| perf-codegen/keywords | 1.01 [1.01, 1.01] | within | 1.01 [1.01, 1.01] |
| sortints | 1.03 [1.03, 1.03] | within | - |
| std-dyn/dispatch | 1.02 [1.02, 1.02] | within | - |
| std-dyn/dispatchalt | 1.20 [1.20, 1.20] | over | - |

Median of mk / c over these rows: 1.00 (14 rows).

## Table "startup"

Startup: process start and one line of output (B15). Reported, never subtracted from another row. Cells: median wall time, and the ratio of medians to the fastest cell.

| Row | Overflow class | mk | c | rust | rust-ovf | zig-fast | zig-safe | go |
|---|---|---|---|---|---|---|---|---|
| hello | never | 4.2 ms, x1.01 | 4.6 ms, x1.11, noisy | 4.7 ms, x1.14 | 4.8 ms, x1.16, noisy | 4.1 ms, x1.00 | 4.2 ms, x1.01 | 5.8 ms, x1.41 |

### hello

Process start, print one line, exit. Reported and never subtracted from another row (B15). Arguments ``, checksum `1`.

| Column | Class | Median | Min | MAD | RSD | Runs | Ratio to best [95%] | Ratio to best of its class [95%] |
|---|---|---|---|---|---|---|---|---|
| mk | checked | 4.2 ms | 4.0 ms | 78 us | 2.6% | 30 | 1.01 [0.99, 1.02] | 1.00 [0.98, 1.02] |
| c | unchecked | 4.6 ms | 4.4 ms | 115 us | 10.9% | 30 | 1.11 [1.09, 1.14] | 1.11 [1.09, 1.14] |
| rust | bounds | 4.7 ms | 4.6 ms | 53 us | 2.0% | 30 | 1.14 [1.13, 1.16] | 1.00 [0.99, 1.01] |
| rust-ovf | checked | 4.8 ms | 4.6 ms | 102 us | 3.2% | 30 | 1.16 [1.14, 1.18] | 1.15 [1.13, 1.17] |
| zig-fast | unchecked | 4.1 ms | 4.0 ms | 63 us | 2.2% | 30 | 1.00 [0.99, 1.01] | 1.00 [0.99, 1.01] |
| zig-safe | checked | 4.2 ms | 4.1 ms | 55 us | 3.0% | 30 | 1.01 [1.00, 1.02] | 1.00 [0.99, 1.02] |
| go | bounds | 5.8 ms | 5.7 ms | 78 us | 2.1% | 30 | 1.41 [1.39, 1.43] | 1.24 [1.22, 1.25] |

- Times of a few milliseconds are below the 1 s floor by nature: this row exists to show the startup cost each other row carries.

## Table "codegen"

*Carried from the publish of 2026-11-06: the codegen table was not rerun with 2026-11-09's mk, so its numbers are that run's, with the facts printed here.*

From `tests/bench/results/2026-11-06/codegen/results.json`. In-process kernels: Mk against C compiled by the same LLVM (clang version 21.1.8), the C side a freestanding native object linked into the Mk program (README "The codegen table"). Each side fills its own 64-byte-aligned buffer untimed and times only its `Run`; the order alternates by round (B10). Times are per `Run`: 3 processes of the kernel's rounds (at least 21 each), pooled. This table never feeds a cross-language ratio (B1).

Machine: AMD Ryzen 7 9800X3D 8-Core Processor; windows Microsoft Windows [Version 10.0.26300.9818]; power scheme Power Scheme GUID: 8c5e7fda-e8bf-4a96-9a85-a6e23a8c635c  (High performance); affinity 0x4 (one logical CPU, set on tools/bench and inherited). mk `C:\work\tmp\mk-head.exe` (sha256 `3dc1a9b0ced54129`), tree at `15735c75fb0af293cc33bd54bbd9f9afa0dfe042`; measured 2026-11-07 01:57. Other compilers and test binaries running at the start: none.

| Kernel | Args | mk median (min, MAD) | c median (min, MAD) | mk / c [95%] | Against 1.10 | Prices |
|---|---|---|---|---|---|---|
| fmtints | 1000000 1 | 13.4 ms (13.3 ms, 49 us) | 12.1 ms (12.0 ms, 34 us) | 1.11 [1.10, 1.11] | over | Mk's bounds checks on a byte buffer and checked + in a division-bound loop, against C with none |
| intsum | 1000000 200 1 | 37.6 ms (37.1 ms, 277 us) | 19.0 ms (18.7 ms, 42 us) | 1.98 [1.98, 1.99] | over | Mk's checked + and bounds checks against C's add nsw (budget: overflow checks at most 5 percent, design/budgets.md) |
| matmul | 512 1 | 52.0 ms (51.2 ms, 604 us) | 15.0 ms (14.9 ms, 74 us) | 3.47 [3.44, 3.48] | over | the inner loop's vectorisation under Mk's bounds checks and checked index arithmetic, against C with none (both no FMA) |
| nbody | 1000000 | 34.0 ms (33.8 ms, 97 us) | 38.7 ms (38.5 ms, 123 us) | 0.88 [0.88, 0.88] | within | scalar f64 code with a sqrt and a division per pair under Mk's bounds checks on fixed-size arrays, against C (both no FMA) |
| sieve | 10000000 | 11.6 ms (11.6 ms, 41 us) | 12.4 ms (12.2 ms, 146 us) | 0.94 [0.93, 0.95] | within | Mk's bounds checks and checked + and * on a store-bound loop with a run-time stride, against C with none |

Median of mk / c over these kernels: 1.11 (5 kernels).

# Linux (x86_64-linux in WSL 2)

*Carried from the publish of 2026-11-06: the Linux table was not rerun with 2026-11-09's mk, so its numbers are that run's, with the facts printed here.*

From `tests/bench/results/2026-11-06/linux/results.json`. Mk against C only (B22): the Mk programs are cross-built on Windows by the same mk (`--target x86_64-linux`), the C programs are built inside WSL by clang 21 against glibc and linked statically by lld, and everything is timed inside WSL. WSL 2 is a virtual machine: `taskset -c 2` pins a virtual CPU, not a physical one.

| Fact | Value |
|---|---|
| Commit | `15735c75fb0af293cc33bd54bbd9f9afa0dfe042` (working tree had uncommitted changes to tracked files) |
| mk | `C:\work\tmp\mk-head.exe`, sha256 `3dc1a9b0ced54129` |
| Machine | AMD Ryzen 7 9800X3D 8-Core Processor, 1 CPUs visible, Linux 6.6.87.2-microsoft-standard-WSL2, Ubuntu 24.04.3 LTS |
| Power scheme | n/a (Linux guest) |
| Defender real-time protection | n/a (Linux guest) |
| Affinity | taskset -c 2, set on tools/bench from outside and inherited (B8, B22) |
| GOGC | 100 (unset) |
| Timing | hyperfine `-N --warmup 3 --runs 10`, 3 sweeps in rotated column order, pooled; RSD above 3% reruns once, then the cell is marked noisy |
| Other compilers and test binaries running at the start | none |

| Fact | Value |
|---|---|
| Commit | `15735c75fb0af293cc33bd54bbd9f9afa0dfe042` (working tree had uncommitted changes to tracked files) |
| mk | `C:\work\tmp\mk-head.exe`, sha256 `3dc1a9b0ced54129` |
| Machine | AMD Ryzen 7 9800X3D 8-Core Processor, 12 CPUs in the VM (1 allowed by taskset), Linux 6.6.87.2-microsoft-standard-WSL2, Ubuntu 24.04.3 LTS |
| Power scheme | n/a (Linux guest) |
| Defender real-time protection | n/a (Linux guest) |
| Affinity | taskset -c 2, set on tools/bench from outside and inherited (B8, B22) |
| GOGC | 100 (unset) |
| Timing | hyperfine `-N --warmup 3 --runs 10`, 3 sweeps in rotated column order, pooled; RSD above 3% reruns once, then the cell is marked noisy |
| Other compilers and test binaries running at the start | none |

## Columns

| Column | Toolchain | Bounds | Overflow | Panic | LTO | Linker | CRT | std at x86-64-v2 |
|---|---|---|---|---|---|---|---|---|
| mk | mk at 15735c75fb0af293cc33bd54bbd9f9afa0dfe042, cross-built on Windows (--target x86_64-linux) by C:\work\tmp\mk-head.exe | checked | trap | abort (exit 101) | ThinLTO | ld.lld (LLVM 21 on Windows, static ELF) | none (Mk runtime, Linux system calls) | yes (built with the program) |
| mk-wrap | mk at 15735c75fb0af293cc33bd54bbd9f9afa0dfe042, cross-built on Windows (--target x86_64-linux) by C:\work\tmp\mk-head.exe | checked | wrap (`+%` operators) | abort (exit 101) | ThinLTO | ld.lld (LLVM 21 on Windows, static ELF) | none (Mk runtime, Linux system calls) | yes (built with the program) |
| c | Ubuntu clang version 21.1.8 (++20251221032922+2078da43e25a-1~exp1~20251221153059.70) | none | UB (signed), wrap (unsigned) | none | none (single TU) | ld.lld 21 (-fuse-ld=lld), static | static glibc 2.39 (-static, -lm) | no (prebuilt glibc) |

The build command of every cell is in `results.json` (`builds`). Only Mk and C share an LLVM (clang 21); against Rust (its own LLVM), Zig (its bundled LLVM) and Go (gc), a row compares whole toolchains and their std, not code generators alone (B7). On Windows, Mk's release path is ThinLTO through lld-link, which runs no SLP vectorizer today (STATUS.md, perf-codegen's item).

## Table "same"

Same algorithm: each row is one reference algorithm written in every language with the same data structures, loop structure and arithmetic types (its `README.md`). Whole programs, timed from outside. Cells: median wall time, and the ratio of medians to the fastest cell of the row with its 95% bootstrap interval.

| Row | Overflow class | mk | mk-wrap | c |
|---|---|---|---|---|
| binarytrees | never | 2.791 s, x1.38, noisy | not written: not needed for the budgets; written if the checked column shows an overflow-check cost | 2.018 s, x1.00 |
| fannkuch | never | 1.782 s, x1.00 | not written: not needed for the budgets; written if the checked column shows an overflow-check cost | 1.855 s, x1.04 |
| fmtints | never | 1.364 s, x1.26 | not written: not needed for the budgets; written if the checked column shows an overflow-check cost | 1.086 s, x1.00, noisy |
| intsum | never | 2.321 s, x2.03 | 1.161 s, x1.01 | 1.144 s, x1.00 |
| jsonscan | wraps | 1.216 s, x1.06 | not written: the hash already uses the wrapping operators in every column (overflow class wraps) | 1.149 s, x1.00 |
| mandelbrot | never | 1.237 s, x1.00 | not written: the hot loop is float; its only integer arithmetic is the iteration counter | 1.241 s, x1.00 |
| matmul | never | 4.028 s, x3.27 | not written: only index arithmetic is integer; a wrapping column is not needed for the budgets | 1.233 s, x1.00 |
| nbody | never | 1.976 s, x1.11 | not written: the hot loop has no integer arithmetic beyond loop counters, so a wrapping column would equal mk | 1.786 s, x1.00 |
| sieve | never | 1.152 s, x1.00, noisy | 1.201 s, x1.04 | 1.232 s, x1.07 |
| sortints | never | 1.268 s, x1.04 | not written: the sort does only index arithmetic; a wrapping column is not needed for the budgets | 1.215 s, x1.00 |
| spectralnorm | never | 1.129 s, x1.01 | not written: not needed for the budgets; A(i, j)'s integer arithmetic is checked in mk | 1.117 s, x1.00 |

### binarytrees

binary-trees with one bump-allocated buffer of u32-index nodes in every language (B9); the sum of every tree check (README.md). Arguments `22`, checksum `1361750702`.

| Column | Class | Median | Min | MAD | RSD | Runs | Ratio to best [95%] | Ratio to best of its class [95%] |
|---|---|---|---|---|---|---|---|---|
| mk | checked | 2.791 s | 2.714 s | 39.7 ms | 3.8% | 30 | 1.38 [1.37, 1.40] | 1.00 [0.99, 1.01] |
| c | unchecked | 2.018 s | 1.978 s | 25.1 ms | 1.8% | 30 | 1.00 [0.99, 1.01] | 1.00 [0.99, 1.01] |


### fannkuch

fannkuch-redux: every permutation of n, counting prefix reversals; checksum * 1000 + maxflips (README.md). Arguments `11`, checksum `556355051`.

| Column | Class | Median | Min | MAD | RSD | Runs | Ratio to best [95%] | Ratio to best of its class [95%] |
|---|---|---|---|---|---|---|---|---|
| mk | checked | 1.782 s | 1.770 s | 9.4 ms | 2.8% | 30 | 1.00 [0.99, 1.01] | 1.00 [0.99, 1.01] |
| c | unchecked | 1.855 s | 1.840 s | 6.7 ms | 1.2% | 30 | 1.04 [1.03, 1.04] | 1.00 [1.00, 1.00] |


### fmtints

Integers in [-1e9, 1e9] to decimal text with the textbook digit loop, then the byte sum, rounds times (README.md). Arguments `1000000 100 1`, checksum `49968808800`.

| Column | Class | Median | Min | MAD | RSD | Runs | Ratio to best [95%] | Ratio to best of its class [95%] |
|---|---|---|---|---|---|---|---|---|
| mk | checked | 1.364 s | 1.347 s | 8.7 ms | 1.8% | 30 | 1.26 [1.25, 1.26] | 1.00 [0.99, 1.01] |
| c | unchecked | 1.086 s | 1.073 s | 7.4 ms | 6.2% | 30 | 1.00 [0.99, 1.01] | 1.00 [0.99, 1.01] |


### intsum

Sum of n int64 values in 0..1023, repeated rounds times with a write-back between passes (README.md). Arguments `1000000 12000 1`, checksum `6136887200936`.

| Column | Class | Median | Min | MAD | RSD | Runs | Ratio to best [95%] | Ratio to best of its class [95%] |
|---|---|---|---|---|---|---|---|---|
| mk | checked | 2.321 s | 2.286 s | 21.8 ms | 1.3% | 30 | 2.03 [2.00, 2.04] | 1.00 [0.99, 1.01] |
| mk-wrap | bounds | 1.161 s | 1.150 s | 6.6 ms | 1.3% | 30 | 1.01 [1.00, 1.02] | 1.00 [0.99, 1.01] |
| c | unchecked | 1.144 s | 1.132 s | 7.8 ms | 2.2% | 30 | 1.00 [0.99, 1.01] | 1.00 [0.99, 1.01] |


### jsonscan

A JSON tokenizer over a 32 MB generated document, hashing every token, repeated rounds times (README.md). Arguments `{data} 52`, checksum `7010378328625811788`.

| Column | Class | Median | Min | MAD | RSD | Runs | Ratio to best [95%] | Ratio to best of its class [95%] |
|---|---|---|---|---|---|---|---|---|
| mk | bounds | 1.216 s | 1.198 s | 11.5 ms | 1.5% | 30 | 1.06 [1.05, 1.07] | 1.00 [0.99, 1.01] |
| c | unchecked | 1.149 s | 1.143 s | 2.9 ms | 0.8% | 30 | 1.00 [1.00, 1.00] | 1.00 [1.00, 1.00] |


### mandelbrot

Scalar Mandelbrot over an n by n grid, at most 50 iterations per point; the checksum is the total iteration count (README.md). Arguments `5600`, checksum `746474893`.

| Column | Class | Median | Min | MAD | RSD | Runs | Ratio to best [95%] | Ratio to best of its class [95%] |
|---|---|---|---|---|---|---|---|---|
| mk | checked | 1.237 s | 1.233 s | 2.7 ms | 0.3% | 30 | 1.00 [1.00, 1.00] | 1.00 [1.00, 1.00] |
| c | unchecked | 1.241 s | 1.234 s | 3.9 ms | 1.6% | 30 | 1.00 [1.00, 1.01] | 1.00 [1.00, 1.00] |


### matmul

Dense f64 matrix multiply, i-k-j loop over flat row-major arrays, repeated rounds times into one result; exact by construction (README.md). Arguments `512 76 1`, checksum `165617122137808`.

| Column | Class | Median | Min | MAD | RSD | Runs | Ratio to best [95%] | Ratio to best of its class [95%] |
|---|---|---|---|---|---|---|---|---|
| mk | checked | 4.028 s | 3.968 s | 31.2 ms | 2.8% | 30 | 3.27 [3.24, 3.28] | 1.00 [0.99, 1.01] |
| c | unchecked | 1.233 s | 1.220 s | 7.3 ms | 1.0% | 30 | 1.00 [1.00, 1.00] | 1.00 [1.00, 1.00] |


### nbody

Five-body leapfrog in f64, structure of arrays, steps of 0.0078125; the checksum is the final energy times 1e9 truncated to an integer (README.md). Arguments `50000000`, checksum `-110176510`.

| Column | Class | Median | Min | MAD | RSD | Runs | Ratio to best [95%] | Ratio to best of its class [95%] |
|---|---|---|---|---|---|---|---|---|
| mk | checked | 1.976 s | 1.964 s | 6.2 ms | 1.0% | 30 | 1.11 [1.10, 1.11] | 1.00 [1.00, 1.00] |
| c | unchecked | 1.786 s | 1.776 s | 4.6 ms | 1.0% | 30 | 1.00 [1.00, 1.00] | 1.00 [1.00, 1.00] |


### sieve

Sieve of Eratosthenes over n + 1 byte flags, rounds times; the checksum is rounds times the count of primes up to n (README.md). Arguments `20000000 46`, checksum `58447922`.

| Column | Class | Median | Min | MAD | RSD | Runs | Ratio to best [95%] | Ratio to best of its class [95%] |
|---|---|---|---|---|---|---|---|---|
| mk | checked | 1.152 s | 1.128 s | 11.5 ms | 6.9% | 30 | 1.00 [0.99, 1.01] | 1.00 [0.99, 1.01] |
| mk-wrap | bounds | 1.201 s | 1.174 s | 11.0 ms | 2.1% | 30 | 1.04 [1.03, 1.05] | 1.00 [0.99, 1.01] |
| c | unchecked | 1.232 s | 1.215 s | 5.8 ms | 1.1% | 30 | 1.07 [1.06, 1.08] | 1.00 [1.00, 1.00] |


### sortints

Go 1.27's pdqsortOrdered written out for int64 in every language, over four patterns of n keys per round (README.md). Arguments `1000000 16 1`, checksum `8192754217017821`.

| Column | Class | Median | Min | MAD | RSD | Runs | Ratio to best [95%] | Ratio to best of its class [95%] |
|---|---|---|---|---|---|---|---|---|
| mk | checked | 1.268 s | 1.257 s | 4.0 ms | 1.0% | 30 | 1.04 [1.04, 1.05] | 1.00 [1.00, 1.00] |
| c | unchecked | 1.215 s | 1.205 s | 6.0 ms | 0.8% | 30 | 1.00 [1.00, 1.01] | 1.00 [1.00, 1.01] |


### spectralnorm

Spectral norm of the infinite matrix A(i,j) = 1/((i+j)(i+j+1)/2+i+1) by 10 power iterations of AtA (the Benchmarks Game's spectral-norm, scalar) (README.md). Arguments `6000`, checksum `1274224152`.

| Column | Class | Median | Min | MAD | RSD | Runs | Ratio to best [95%] | Ratio to best of its class [95%] |
|---|---|---|---|---|---|---|---|---|
| mk | checked | 1.129 s | 1.117 s | 7.9 ms | 1.6% | 30 | 1.01 [1.01, 1.02] | 1.00 [0.99, 1.01] |
| c | unchecked | 1.117 s | 1.114 s | 1.2 ms | 0.4% | 30 | 1.00 [1.00, 1.00] | 1.00 [1.00, 1.00] |


### Mk against C (the codegen metric, same LLVM)

| Row | mk / c [95%] | Against 1.10 | mk-wrap / c [95%] |
|---|---|---|---|
| binarytrees | 1.38 [1.37, 1.40] | over | - |
| fannkuch | 0.96 [0.96, 0.97] | within | - |
| fmtints | 1.26 [1.25, 1.26] | over | - |
| intsum | 2.03 [2.00, 2.04] | over | 1.01 [1.00, 1.02] |
| jsonscan | 1.06 [1.05, 1.07] | within | - |
| mandelbrot | 1.00 [0.99, 1.00] | within | - |
| matmul | 3.27 [3.24, 3.28] | over | - |
| nbody | 1.11 [1.10, 1.11] | over | - |
| sieve | 0.93 [0.93, 0.94] | within | 0.97 [0.97, 0.98] |
| sortints | 1.04 [1.04, 1.05] | within | - |
| spectralnorm | 1.01 [1.01, 1.02] | within | - |

Median of mk / c over these rows: 1.06 (11 rows).

## Table "compile"

*Carried from the publish of 2026-11-07: the compile-speed table was not rerun with 2026-11-09's mk, so its numbers are that run's, with the facts printed here.*

From `tests/bench/results/2026-11-07/compile/results.json`. Compile speed, never mixed with run time (B25): one generated program of the same shape in every language, 50 modules of 8 structs and 40 functions (each function builds a struct, loops, branches, calls its module's helper and the same function of its parent module; main calls all 2000 functions and prints a checksum that every build is checked against, so a stale cache fails instead of timing). Clean is every cache and object of the language deleted; edit is one function body's constant changed, a new value each build. Wall time from the first compiler start to the executable, 8 jobs, 3 clean builds and 10 one-function-edit builds (after 2 untimed) per cell. Ratios are to the fastest cell of the same profile and kind.

Machine: AMD Ryzen 7 9800X3D 8-Core Processor; windows Microsoft Windows [Version 10.0.26300.9818]; power scheme Power Scheme GUID: 8c5e7fda-e8bf-4a96-9a85-a6e23a8c635c  (High performance); affinity 0x5555 (one logical CPU on each of 8 cores, set on tools/bench and inherited by every compiler); measured 2026-11-07 14:04. mk `C:\work\bench\mk-bs2.exe` (mk built from 15735c75fb0af293cc33bd54bbd9f9afa0dfe042 (2026-11-06T15:28:41Z) with uncommitted changes, LLVM 21.1.8 from C:\Program Files\LLVM\bin (default location), sha256 `cdab22e914fcaaac`). Other compilers and test binaries running at the start: none.

| Language | Version | Profiles and build |
|---|---|---|
| c | clang version 21.1.8 | dev: CMake's Debug (`-g`, -O0); release: CMake's Release (`-O3 -DNDEBUG`); both `-march=x86-64-v2`, one translation unit per module plus main, compiled 8 at a time by tools/bench (a make: a TU whose object is older than its source), linked by lld |
| rust | cargo 1.99.1 (5f94df478 2026-08-27); rustc 1.99.1 (58cf8f9c3 2026-10-13), LLVM 23.1.1 | `cargo build` (dev: opt-level 0, debug info, incremental) and `cargo build --release` (opt-level 3, 16 codegen units, no LTO, not incremental), `-j 8`, one crate of one module per generated module; `-C target-cpu=x86-64-v2` through RUSTFLAGS. std comes precompiled with the toolchain, so a clean Rust build compiles only the crate |
| zig | zig 0.17.0, clang version 22.1.8 | `zig build-exe` with `-O Debug` (dev) and `-O ReleaseFast` (release), `-j8`, `-target x86_64-windows-gnu -mcpu=x86_64_v2`, one file per module. Clean empties both caches, so it also rebuilds compiler_rt and the MinGW C runtime that Zig builds from source |
| go | go version go1.27.3 windows/amd64 | `go build -p 8` (Go has one build profile; the release row says so), GOAMD64=v2, one package per module. Clean is a fresh GOCACHE and `-a`, so it rebuilds the std packages the program imports |
| mk | mk built from 15735c75fb0af293cc33bd54bbd9f9afa0dfe042 (2026-11-06T15:28:41Z) with uncommitted changes, LLVM 21.1.8 from C:\Program Files\LLVM\bin (default location) | `mk build -j 8` (dev) and `mk build --release --pgo=off -j 8` (release), `--cpu x86-64-v2`, one module directory per module. Clean is a fresh MK_CACHE |

**dev profile.**

| Language | Lines | Clean median (min, MAD) | Clean / best | Edit median (min, MAD) | Edit / best |
|---|---|---|---|---|---|
| c | 38373 | 843.8 ms (752.2 ms, 5.7 ms) | 1.00 | 207.9 ms (203.2 ms, 3.1 ms) | 1.00 |
| rust | 36322 | 1.341 s (1.255 s, 86.4 ms) | 1.59 | 469.2 ms (436.9 ms, 12.4 ms) | 2.26 |
| zig | 38369 | 4.591 s (4.550 s, 41.4 ms) | 5.44 | 2.307 s (2.222 s, 58.4 ms) | 11.10 |
| go | 36523 | 2.955 s (2.827 s, 128.0 ms) | 3.50 | 294.8 ms (278.3 ms, 12.0 ms) | 1.42 |
| mk | 36426 | 1.887 s (1.783 s, 103.3 ms) | 2.24 | 399.5 ms (349.1 ms, 25.1 ms) | 1.92 |

**release profile.**

| Language | Lines | Clean median (min, MAD) | Clean / best | Edit median (min, MAD) | Edit / best |
|---|---|---|---|---|---|
| c | 38373 | 1.336 s (1.128 s, 20.5 ms) | 1.00 | 195.3 ms (190.1 ms, 1.2 ms) | 1.00 |
| rust | 36322 | 2.582 s (2.495 s, 82.9 ms) | 1.93 | 2.400 s (2.361 s, 35.1 ms) | 12.29 |
| zig | 38369 | 10.219 s (10.150 s, 68.6 ms) | 7.65 | 7.834 s (7.704 s, 86.3 ms) | 40.12 |
| go | | Go has one build profile: see the dev table | | | |
| mk | 36426 | 2.879 s (2.721 s, 3.1 ms) | 2.16 | 975.3 ms (943.4 ms, 28.3 ms) | 4.99 |

## Table "idiomatic", row http

*Carried from the publish of 2026-11-07: the http row was not rerun with 2026-11-09's mk, so its numbers are that run's, with the facts printed here.*

From `tests/bench/results/2026-11-07/http/results.json`. Serve `GET /` with `200 OK`, `Content-Type: text/plain` and the 14-byte body `Hello, World!
` (with Content-Length) over HTTP/1.1 keep-alive, with the language's std HTTP server. Each language's std HTTP server under tools/bench's load generator (B19): 1000 keep-alive connections, one request outstanding on each, 5 s of warm-up and 30 s measured, 3 sweeps in rotated order, each a fresh server; the server on affinity mask 0xff (CPUs 0-7), the generator on 0xff00 (CPUs 8-15), 8 threads each (GOMAXPROCS, MK_CARRIERS). Every response's status and body are checked. req/s is the median of the sweeps, p50 and p99 the medians of the sweeps' latencies (request written to response read), timed by Go's clock, whose step on this machine was 520 us, so each latency is known to that step; the mean latency is exact by Little's law (connections / req/s).

Machine: AMD Ryzen 7 9800X3D 8-Core Processor; windows Microsoft Windows [Version 10.0.26300.9818]; power scheme Power Scheme GUID: 8c5e7fda-e8bf-4a96-9a85-a6e23a8c635c  (High performance); affinity server 0xff, generator 0xff00 (set on tools/bench before each start; the server inherits it); measured 2026-11-07 14:08.

| Column | Model | req/s (sweeps) | Mean latency | p50 | p99 | req/s / best |
|---|---|---|---|---|---|---|
| zig-fast | OS threads (std.http.Server, one Io.Group.concurrent thread per connection) | 199040 (189634, 199040, 229186) | 5.0 ms | 2.0 ms | 26.6 ms | 1.00 |
| zig-safe | OS threads (std.http.Server, one Io.Group.concurrent thread per connection) | 174334 (140062, 176062, 174334) | 5.7 ms | 3.5 ms | 19.7 ms | 0.88 |
| go | goroutines (net/http, GOMAXPROCS=8) | 181940 (181940, 197630, 139807) | 5.5 ms | 4.3 ms | 14.0 ms | 0.91 |
| c | | no std HTTP (C's std has no sockets) | | | | |
| mk | | pending: std.http (the std-http lane); bench-final adds the Mk side | | | | |
| rust | | no std HTTP (std::net is TCP only; hyper and axum are crates) | | | | |

