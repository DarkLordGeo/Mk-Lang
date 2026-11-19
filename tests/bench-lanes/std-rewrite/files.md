# std-rewrite: the write set, file by file (rewrite bar)

Generated from the tree by tests/bench-lanes/std-rewrite/files_table.py.txt, which also regenerates lines-after.txt; regenerate the counts before relying on them. Columns: lines before (lines-before.txt; blank: the file is newer) and now (`wc -l`); R1 `const` fn parameters declared (rule 1 can apply only at their call sites; none of std's call sites carries a capture-only context struct); R2 exported `...With(` declarations other than StartsWith, EndsWith and ScopeWith (0 everywhere; internal helpers such as std/sys's `FailWith` do not count); R3 `", ())` (the short Print form; the 2 in tests_fmt.mk test the explicit empty record on purpose); C: atomics, locks or waits in the file (the async bar applies); read: who read the file line by line under the async bar (blank: not yet).

| File | Before | Now | R1 | R2 | R3 | C | Test | Read under the async bar |
|---|---|---|---|---|---|---|---|---|
| std/atomic/atomic.mk | 100 | 100 |  |  |  | C |  | rerun 2 s1 (no defect; notes in status) |
| std/atomic/atomic_t.mk |  | 103 |  |  |  | C |  | rerun 2 s2 (no defect; declarations and dispatch, notify N1 to N4) |
| std/atomic/order.mk |  | 58 |  |  |  |  |  | rerun 2 s3b (no defect) |
| std/atomic/spin.mk | 62 | 62 |  |  |  | C |  | rerun 2 s1 (no defect; notes in status) |
| std/atomic/tests_atomic.mk | 71 | 71 |  |  |  |  | t |  |
| std/atomic/tests_atomic_t.mk |  | 396 |  |  |  |  | t |  |
| std/atomic/tests_spin.mk | 65 | 65 |  |  |  |  | t |  |
| std/cli/args.mk | 454 | 454 |  |  |  |  |  | rerun 2 s3b (no atomics; no defect; ports checked against Go's and glibc's) |
| std/coll/bitset.mk | 87 | 87 |  |  |  |  |  | rerun 2 s3b (no atomics; no defect) |
| std/coll/btree.mk | 916 | 916 |  |  |  |  |  | rerun 2 s3b (no atomics; no defect) |
| std/coll/deque.mk | 237 | 237 |  |  |  |  |  | rerun 2 s3b (no atomics; no defect) |
| std/coll/pqueue.mk | 125 | 125 |  |  |  |  |  | rerun 2 s3b (no atomics; no defect) |
| std/coll/slots.mk | 187 | 187 |  |  |  |  |  | rerun 2 s3 (no atomics; no defect) |
| std/core/coll_heap.mk | 426 | 426 |  |  |  |  |  | rerun 2 s3b (no atomics; no defect) |
| std/core/coll_list.mk | 101 | 101 |  |  |  |  |  | rerun 2 s3b (no atomics; no defect) |
| std/core/coll_map.mk | 175 | 175 |  |  |  |  |  | rerun 2 s3b (no atomics; no defect) |
| std/core/coll_table.mk | 600 | 600 |  |  |  |  |  | rerun 2 s3b (no atomics; no defect) |
| std/core/core_cell.mk | 25 | 25 |  |  |  |  |  | rerun 2 s3b (no atomics; no defect) |
| std/core/core_duration.mk | 19 | 19 |  |  |  |  |  | rerun 2 s3b (declarations and small bodies; no defect) |
| std/core/core_errors.mk | 10 | 10 |  |  |  |  |  | rerun 2 s3b (declarations and small bodies; no defect) |
| std/core/core_fmt.mk | 943 | 943 |  |  |  |  |  | rerun 2 s3b (no defect; uscale and ftoa against Go 1.27; the generated table not re-derived) |
| std/core/core_gen.mk | 108 | 108 |  |  |  |  |  | rerun 2 s3b (declarations; no defect) |
| std/core/core_hash.mk | 513 | 513 |  |  |  |  |  | rerun 2 s3b (no atomics; no defect; ports checked against Go's and glibc's) |
| std/core/core_iface.mk | 23 | 23 |  |  |  |  |  | rerun 2 s3b (declarations and small bodies; no defect) |
| std/core/core_impls.mk | 417 | 417 |  |  |  |  |  | rerun 2 s3b (no defect) |
| std/core/core_iodecl.mk | 295 | 328 |  |  |  | C |  | rerun 2 s2 (no defect; declarations and dispatch, notify N1 to N4) |
| std/core/core_iter.mk | 267 | 267 |  |  |  |  |  | rerun 2 s3b (no defect; the SWAR masks checked) |
| std/core/core_layout.mk | 16 | 16 |  |  |  |  |  | rerun 2 s3b (declarations and small bodies; no defect) |
| std/core/core_parse.mk | 699 | 699 |  |  |  |  |  | rerun 2 s3b (no atomics; no defect) |
| std/core/core_path.mk | 328 | 344 |  |  |  | C |  | rerun 2 s1-s3, whole (no defect) |
| std/core/core_rw.mk | 262 | 335 |  |  |  |  |  | rerun 2 s3 (no atomics; no defect) |
| std/core/core_slice.mk | 110 | 110 |  |  |  |  |  | rerun 2 s3b (no defect; Index's comment corrected) |
| std/core/core_sort.mk | 824 | 470 | 14 |  |  |  |  | rerun 2 s3b (no atomics; no defect; ports checked against Go's and glibc's) |
| std/core/core_str.mk | 293 | 293 |  |  |  |  |  | rerun 2 s3b (no atomics; no defect; ports checked against Go's and glibc's) |
| std/core/core_tables.mk | 26 | 26 |  |  |  |  |  | rerun 2 s3b (declarations and small bodies; no defect) |
| std/core/core_target.mk | 26 | 26 |  |  |  |  |  | rerun 2 s3b (declarations and small bodies; no defect) |
| std/core/core_utf8.mk | 95 | 95 |  |  |  |  |  | rerun 2 s3b (no atomics; no defect) |
| std/core/io_file.mk | 218 | 251 |  |  |  |  |  | rerun 2 s3b (no atomics; no defect) |
| std/core/io_internal.mk | 192 | 192 |  |  |  | C |  | rerun 2 s2 (no defect; declarations and dispatch, notify N1 to N4) |
| std/core/io_io.mk | 58 | 58 |  |  |  |  |  | rerun 2 s3b (no defect) |
| std/core/io_stream.mk | 266 | 266 |  |  |  | C |  | rerun 2 s1-s3, whole (no defect) |
| std/core/io_system.mk | 137 | 137 |  |  |  | C |  | rerun 2 s2 (no defect; declarations and dispatch, notify N1 to N4) |
| std/core/mem_alloc.mk | 43 | 43 |  |  |  |  |  | rerun 2 s3b (no defect) |
| std/core/mem_arena.mk | 453 | 695 |  |  |  |  |  | rerun s3 (S-1) |
| std/core/mem_box.mk | 69 | 69 |  |  |  |  |  | rerun 2 s3b (no defect) |
| std/core/mem_chan.mk | 447 | 483 |  |  |  | C |  | s1-s3, fix round; rerun 2 s1 (S bit) |
| std/core/mem_internal.mk | 63 | 63 |  |  |  | C |  | rerun 2 s2 (no defect; declarations and dispatch, notify N1 to N4) |
| std/core/mem_path.mk |  | 85 |  |  |  |  |  | rerun 2 s3b (no defect) |
| std/core/mem_slots.mk | 242 | 242 |  |  |  |  |  | rerun 2 s3 (no atomics; no defect) |
| std/core/mem_table.mk | 66 | 66 |  |  |  |  |  | rerun 2 s3b (no atomics; no defect) |
| std/core/sync_chan.mk | 684 | 725 |  |  |  | C |  | s1-s3, fix round; rerun 2 s1 (S bit) |
| std/core/sync_mutex.mk | 495 | 584 |  |  |  | C |  | s1-s3, fix round; rerun 2 s1 (S bit) |
| std/core/tests_arena.mk | 229 | 459 |  |  |  |  | t |  |
| std/core/tests_fmt.mk | 163 | 163 |  |  | 2 |  | t |  |
| std/core/tests_heap.mk | 252 | 252 |  |  |  |  | t |  |
| std/core/tests_io.mk | 299 | 299 |  |  |  | C | t |  |
| std/core/tests_list.mk | 469 | 453 |  |  |  |  | t |  |
| std/core/tests_map.mk | 768 | 768 |  |  |  |  | t |  |
| std/core/tests_parse.mk | 166 | 166 |  |  |  |  | t |  |
| std/core/tests_path.mk | 155 | 155 |  |  |  |  | t |  |
| std/core/tests_rw.mk | 110 | 110 |  |  |  |  | t |  |
| std/core/tests_str.mk | 262 | 262 |  |  |  |  | t |  |
| std/core/tests_sync.mk | 513 | 615 |  |  |  | C | t |  |
| std/core/text_case.mk | 474 | 474 |  |  |  |  |  | rerun 2 s3b (no defect; against Go's unicode.to) |
| std/core/text_fmt.mk | 13 | 13 |  |  |  |  |  | rerun 2 s3b (declarations and small bodies; no defect) |
| std/core/text_text.mk | 269 | 269 |  |  |  |  |  | rerun 2 s3b (no atomics; no defect; ports checked against Go's and glibc's) |
| std/corpus/testprelude.mk | 17 | 17 |  |  |  |  |  | rerun 2 s3b (declarations and small bodies; no defect) |
| std/derive/derive.mk | 299 | 299 |  |  |  |  |  | rerun 2 s3b (no defect; binding names checked for capture) |
| std/fs/dir.mk | 98 | 98 |  |  |  |  |  | rerun 2 s3b (no atomics; no defect) |
| std/fs/errors.mk | 13 | 13 |  |  |  |  |  | rerun 2 s3b (declarations and small bodies; no defect) |
| std/fs/file.mk | 114 | 124 |  |  |  |  |  | rerun 2 s3b (no atomics; no defect) |
| std/fs/fs.mk | 164 | 166 |  |  |  |  |  | rerun 2 s3b (no atomics; no defect) |
| std/fs/tests_dir.mk | 121 | 121 |  |  |  |  | t |  |
| std/fs/tests_fs.mk | 360 | 510 |  |  |  |  | t |  |
| std/hash/blake3.mk | 270 | 270 |  |  |  |  |  | rerun 2 s3b (no defect; the schedule and the subtree split against reference_impl.rs) |
| std/hash/bytes.mk | 99 | 99 |  |  |  |  |  | rerun 2 s3b (no defect) |
| std/hash/crc32.mk | 117 | 117 |  |  |  |  |  | rerun 2 s3b (no atomics; no defect; against the published references) |
| std/hash/sha256.mk | 148 | 148 |  |  |  |  |  | rerun 2 s3b (no defect; against FIPS 180-4) |
| std/hash/sha512.mk | 161 | 161 |  |  |  |  |  | rerun 2 s3b (no defect; against FIPS 180-4) |
| std/hash/sip.mk | 121 | 121 |  |  |  |  |  | rerun 2 s3b (no defect; SipHash-1-3 against the reference) |
| std/hash/xx.mk | 261 | 261 |  |  |  |  |  | rerun 2 s3b (no defect; every secret offset against xxhash.h) |
| std/log/log.mk | 151 | 151 |  |  |  |  |  | rerun s2 (no change) |
| std/log/tests_log.mk | 30 | 30 |  |  |  |  | t |  |
| std/math/io_rng.mk | 14 | 14 |  |  |  |  |  | rerun 2 s3b (no atomics; no defect; against the published references) |
| std/math/math.mk | 144 | 144 |  |  |  |  |  | rerun 2 s3b (no atomics; no defect; against the published references) |
| std/math/rng.mk | 140 | 140 |  |  |  |  |  | rerun 2 s3b (no atomics; no defect; against the published references) |
| std/math/tests_math.mk | 76 | 76 |  |  |  |  | t |  |
| std/math/tests_rng.mk | 202 | 202 |  |  |  |  | t |  |
| std/mem/counting.mk | 95 | 95 |  |  |  |  |  | rerun 2 s3b (no atomics; no defect) |
| std/mem/mem.mk | 320 | 320 |  |  |  |  |  | rerun 2 s3b (no atomics; no defect) |
| std/mem/pool.mk |  | 246 |  |  |  | C |  | rerun 2 s2 (no defect; notes in status) |
| std/mem/tests_mem.mk | 363 | 363 |  |  |  |  | t |  |
| std/net/addr.mk | 399 | 399 |  |  |  |  |  | rerun 2 s3b (no atomics; no defect) |
| std/net/errors.mk | 11 | 11 |  |  |  |  |  | rerun 2 s3b (declarations and small bodies; no defect) |
| std/net/net.mk | 220 | 220 |  |  |  |  |  | rerun 2 s3b (no atomics; no defect) |
| std/net/tests_net.mk | 281 | 336 |  |  |  |  | t |  |
| std/net/tests_udp.mk | 1077 | 1077 |  |  |  | C | t |  |
| std/net/udp.mk | 207 | 207 |  |  |  |  |  | rerun 2 s3b (no atomics; no defect) |
| std/proc/proc.mk | 57 | 57 |  |  |  |  |  | rerun 2 s3b (no defect) |
| std/proc/run.mk | 361 | 361 |  |  |  | C |  | rerun 2 s1-s3, whole (no defect) |
| std/proc/supervise.mk | 172 | 179 | 2 |  |  |  |  | rerun 2 s3b (no atomics; no defect) |
| std/proc/tests_proc.mk | 23 | 23 |  |  |  |  | t |  |
| std/proc/tests_proc2.mk | 642 | 642 |  |  |  | C | t |  |
| std/proc/tests_supervise.mk | 720 | 783 |  |  |  |  | t |  |
| std/rt/decode.mk | 117 | 117 |  |  |  |  |  | rerun 2 s3b (no atomics; no defect) |
| std/rt/directio.mk | 358 | 358 |  |  |  |  |  | rerun 2 s3 (no atomics; no defect) |
| std/rt/fault.mk | 106 | 106 |  |  |  |  |  | rerun 2 s3b (no atomics; no defect) |
| std/rt/finish.mk | 54 | 54 |  |  |  |  |  | rerun 2 s3b (no defect) |
| std/rt/heap.mk | 623 | 790 |  |  |  | C |  | rerun 2 s1-s3, whole (no defect) |
| std/rt/heap_table.mk | 59 | 59 |  |  |  |  |  | rerun 2 s3b (no defect) |
| std/rt/intrinsics.mk | 47 | 55 |  |  |  |  |  | rerun 2 s3b (no defect) |
| std/rt/leak.mk | 24 | 24 |  |  |  |  |  | rerun 2 s3b (declarations and small bodies; no defect) |
| std/rt/lock.mk | 37 | 46 |  |  |  | C |  | rerun 2 s1; s2 (release Unlock, its callers audited) |
| std/rt/panic.mk | 254 | 254 |  |  |  | C |  | rerun 2 s1-s3, whole (no defect) |
| std/rt/profile.mk | 203 | 203 |  |  |  |  |  | rerun 2 s3b (no atomics; no defect) |
| std/rt/region.mk | 282 | 549 |  |  |  | C |  | rerun 2 s1-s3, whole (no defect) |
| std/rt/rt.mk | 171 | 171 |  |  |  | C |  | rerun 2 s2 (no defect; notes in status) |
| std/rt/slot.mk | 94 | 97 |  |  |  | C |  | rerun 2 s2 (no defect; notes in status) |
| std/rt/str.mk |  | 24 |  |  |  |  |  | rerun 2 s3b (declarations and small bodies; no defect) |
| std/rt/tests_direct.mk | 66 | 66 |  |  |  | C | t |  |
| std/rt/tests_fault.mk | 51 | 51 |  |  |  |  | t |  |
| std/rt/tests_guard.mk | 131 | 131 |  |  |  |  | t |  |
| std/rt/tests_heap.mk | 226 | 227 |  |  |  | C | t |  |
| std/rt/tests_panic.mk | 57 | 57 |  |  |  |  | t |  |
| std/rt/trace.mk | 135 | 143 |  |  |  | C |  | rerun 2 s2 (TraceOn: a load before the store) |
| std/sync/barrier.mk |  | 104 |  |  |  | C |  | rerun 2 s2 (no defect) |
| std/sync/mem_shared.mk | 132 | 132 |  |  |  | C |  | rerun 2 s2 (no defect) |
| std/sync/mem_wait.mk |  | 285 |  |  |  | C |  | rerun 2 s2 (no defect; notes in status) |
| std/sync/notify.mk |  | 156 |  |  |  | C |  | rerun 2 s2 (no defect; declarations and dispatch, notify N1 to N4) |
| std/sync/once.mk |  | 118 | 2 |  |  | C |  | rerun 2 s2 (no defect; notes in status) |
| std/sync/rwlock.mk | 198 | 198 |  |  |  | C |  | rerun 2 s2 (no defect; notes in status) |
| std/sync/select.mk | 84 | 172 |  |  |  | C |  | s1-s3, fix round; rerun 2 s1 (S bit) |
| std/sync/semaphore.mk |  | 446 |  |  |  | C |  | rerun 2 s3 (no defect) |
| std/sync/tests_rwlock.mk | 303 | 303 |  |  |  |  | t |  |
| std/sync/tests_select.mk | 305 | 662 |  |  |  |  | t |  |
| std/sync/tests_shared.mk | 47 | 47 |  |  |  |  | t |  |
| std/sync/tests_sync2.mk |  | 1122 |  |  |  |  | t |  |
| std/sys/linux/args.mk | 261 | 261 |  |  |  |  |  | rerun 2 s3b (no atomics; no defect) |
| std/sys/linux/clock.mk | 12 | 45 |  |  |  |  |  | rerun 2 s3b (no atomics; no defect) |
| std/sys/linux/dir.mk | 275 | 275 |  |  |  |  |  | rerun 2 s3b (no atomics; no defect) |
| std/sys/linux/fault.mk | 103 | 103 |  |  |  | C |  | rerun 2 s2 (no defect) |
| std/sys/linux/file.mk | 327 | 440 |  |  |  |  |  | rerun 2 s3 (no atomics; no defect) |
| std/sys/linux/futex.mk | 150 | 150 |  |  |  | C |  | rerun 2 s2 (no defect; notes in status) |
| std/sys/linux/mem.mk | 125 | 151 |  |  |  |  |  | rerun 2 s3b (no atomics; no defect) |
| std/sys/linux/net.mk | 195 | 214 |  |  |  |  |  | rerun 2 s3b (no atomics; the resolve comments fixed) |
| std/sys/linux/nr.mk | 106 | 108 |  |  |  |  |  | rerun 2 s3b (every x86_64 and arm64 number and every signature checked; no defect) |
| std/sys/linux/poll.mk | 590 | 602 |  |  |  | C |  | rerun 2 s2; s3 (release SpinUnlock, its callers audited) |
| std/sys/linux/poll_epoll.mk | 644 | 644 |  |  |  | C |  | rerun 2 s1-s3, whole (no defect) |
| std/sys/linux/poll_uring.mk | 752 | 752 |  |  |  | C |  | rerun 2 s1-s3, whole (no defect) |
| std/sys/linux/proc.mk | 649 | 649 |  |  |  |  |  | rerun 2 s3 (no atomics; no defect) |
| std/sys/linux/random.mk | 31 | 31 |  |  |  |  |  | rerun 2 s3b (declarations and small bodies; no defect) |
| std/sys/linux/signal.mk | 145 | 145 |  |  |  | C |  | rerun 2 s2 (no defect) |
| std/sys/linux/sys.mk | 297 | 297 |  |  |  | C |  | rerun 2 s2 (no defect; declarations and dispatch, notify N1 to N4) |
| std/sys/linux/syscall.mk | 14 | 14 |  |  |  |  |  | rerun 2 s3b (declarations and small bodies; no defect) |
| std/sys/linux/testhook.mk | 118 | 118 |  |  |  | C |  | rerun 2 s2 (no defect) |
| std/sys/linux/thread.mk | 209 | 209 |  |  |  | C |  | rerun 2 s2 (no defect; notes in status) |
| std/sys/linux/word.mk | 114 | 114 |  |  |  |  |  | rerun 2 s3b (no atomics; no defect) |
| std/sys/windows/args.mk | 386 | 386 |  |  |  |  |  | rerun 2 s3b (no atomics; no defect) |
| std/sys/windows/clock.mk | 32 | 62 |  |  |  | C |  | rerun 2 s2 (no defect) |
| std/sys/windows/console.mk | 230 | 230 |  |  |  |  |  | rerun 2 s3b (no atomics; no defect) |
| std/sys/windows/dir.mk | 330 | 330 |  |  |  |  |  | rerun 2 s3b (no atomics; no defect) |
| std/sys/windows/fault.mk | 146 | 146 |  |  |  | C |  | rerun 2 s2 (no defect) |
| std/sys/windows/file.mk | 862 | 986 |  |  |  | C |  | rerun 2 s1-s3, whole (no defect) |
| std/sys/windows/hrtimer.mk | 229 | 229 |  |  |  | C |  | rerun 2 s2 (no defect; notes in status) |
| std/sys/windows/kernel32.mk | 86 | 90 |  |  |  |  |  | rerun 2 s3b (every x86_64 and arm64 number and every signature checked; no defect) |
| std/sys/windows/mem.mk | 86 | 96 |  |  |  |  |  | rerun 2 s3b (no atomics; no defect) |
| std/sys/windows/net.mk | 309 | 309 |  |  |  |  |  | rerun 2 s3b (no atomics; the resolve comments fixed) |
| std/sys/windows/poll_iocp.mk | 990 | 990 |  |  |  | C |  | rerun s2-s3 (rt-io2 closures) |
| std/sys/windows/proc.mk | 901 | 901 |  |  |  | C |  | rerun 2 s3 (no defect) |
| std/sys/windows/random.mk | 59 | 64 |  |  |  | C |  | rerun 2 s3 (comment fixed: the cache Store is the edge to callers) |
| std/sys/windows/resolve.mk | 203 | 540 |  |  |  | C |  | rerun s2-s3 (rt-io2 closures) |
| std/sys/windows/signal.mk | 150 | 150 |  |  |  | C |  | rerun 2 s2 (no defect; notes in status) |
| std/sys/windows/sys.mk | 253 | 253 |  |  |  | C |  | rerun 2 s2 (no defect; declarations and dispatch, notify N1 to N4) |
| std/sys/windows/teb.mk | 34 | 34 |  |  |  |  |  | rerun 2 s3b (no atomics; no defect) |
| std/sys/windows/testhook.mk | 118 | 118 |  |  |  | C |  | rerun 2 s2 (no defect) |
| std/sys/windows/thread.mk | 182 | 182 |  |  |  |  |  | rerun 2 s3b (no atomics; no defect) |
| std/sys/windows/word.mk | 114 | 114 |  |  |  |  |  | rerun 2 s3b (no atomics; no defect) |
| std/sys/windows/ws2_32.mk | 365 | 365 |  |  |  | C |  | rerun 2 s1-s3, whole (no defect) |
| std/task/carrier.mk | 201 | 201 |  |  |  | C |  | rerun 2 s1 (no defect; notes in status) |
| std/task/context.mk | 14 | 14 |  |  |  |  |  | rerun 2 s3b (declarations and small bodies; no defect) |
| std/task/cstack.mk | 38 | 38 |  |  |  |  |  | rerun 2 s3b (no atomics; no defect) |
| std/task/hooks.mk | 269 | 271 |  |  |  | C |  | rerun s2-s3 (rt-io2 closures) |
| std/task/io_dns.mk | 976 | 976 |  |  |  | C |  | rerun 2 s1-s3, whole (no defect) |
| std/task/io_file.mk | 373 | 376 |  |  |  | C |  | rerun s2-s3 (rt-io2 closures) |
| std/task/io_m6.mk | 744 | 744 |  |  |  | C |  | rerun 2 s2 (no defect; notes in status) |
| std/task/io_misc.mk | 215 | 246 |  |  |  | C |  | rerun 2 s1 (no defect; notes in status) |
| std/task/io_net.mk | 547 | 547 |  |  |  | C |  | rerun 2 s1 (no defect) |
| std/task/iotable.mk | 48 | 53 |  |  |  |  |  | rerun 2 s1 (R-9 paths) |
| std/task/jitter.mk | 58 | 58 |  |  |  |  |  | rerun 2 s3b (no defect) |
| std/task/poll.mk | 112 | 112 |  |  |  | C |  | rerun 2 s1 (no defect; notes in status) |
| std/task/pool.mk | 641 | 647 |  |  |  | C |  | rerun s2-s3 (rt-io2 closures) |
| std/task/runq.mk | 321 | 321 |  |  |  | C |  | rerun 2 s1 (R-9 paths) |
| std/task/sched.mk | 943 | 967 |  |  |  | C |  | rerun 2 s1 (no defect; notes in status) |
| std/task/scope.mk | 511 | 531 |  |  |  | C |  | rerun 2 s1 (R-9 paths) |
| std/task/spawn.mk | 256 | 256 |  |  |  | C |  | rerun 2 s1 (no defect; notes in status) |
| std/task/start.mk | 228 | 296 |  |  |  | C |  | rerun 2 s1 (R-9 paths) |
| std/task/task.mk | 111 | 111 |  |  |  | C |  | rerun 2 s1 (no defect; notes in status) |
| std/task/tests_bench.mk | 307 | 307 |  |  |  | C | t |  |
| std/task/tests_cancel.mk | 459 | 609 |  |  |  | C | t |  |
| std/task/tests_dns.mk | 677 | 677 |  |  |  | C | t |  |
| std/task/tests_extern.mk | 316 | 316 |  |  |  | C | t |  |
| std/task/tests_f13.mk | 469 | 469 |  |  |  | C | t |  |
| std/task/tests_fault.mk | 44 | 44 |  |  |  | C | t |  |
| std/task/tests_file.mk | 732 | 732 |  |  |  | C | t |  |
| std/task/tests_heap_mn.mk | 134 | 134 |  |  |  | C | t |  |
| std/task/tests_mn.mk | 695 | 695 |  |  |  | C | t |  |
| std/task/tests_net.mk | 542 | 542 |  |  |  | C | t |  |
| std/task/tests_pool.mk | 216 | 216 |  |  |  | C | t |  |
| std/task/tests_proc.mk | 550 | 550 |  |  |  | C | t |  |
| std/task/tests_proc2.mk | 323 | 323 |  |  |  | C | t |  |
| std/task/tests_review.mk | 1102 | 1102 |  |  |  | C | t |  |
| std/task/tests_rtio2s4.mk | 289 | 352 |  |  |  | C | t |  |
| std/task/tests_rtio2s5.mk | 416 | 416 |  |  |  | C | t |  |
| std/task/tests_rtio2s7.mk | 120 | 120 |  |  |  | C | t |  |
| std/task/tests_rtio2s9.mk | 140 | 140 |  |  |  | C | t |  |
| std/task/tests_sched.mk | 229 | 229 |  |  |  | C | t |  |
| std/task/tests_signal.mk | 295 | 295 |  |  |  | C | t |  |
| std/task/tests_spawn.mk | 240 | 240 |  |  |  | C | t |  |
| std/task/tests_sysstack.mk | 99 | 99 |  |  |  | C | t |  |
| std/task/tests_trim.mk |  | 345 |  |  |  | C | t |  |
| std/task/tests_udp.mk | 417 | 417 |  |  |  | C | t |  |
| std/task/timer.mk | 368 | 368 |  |  |  | C |  | rerun 2 s1 (no defect; notes in status) |
| std/task/wait.mk | 203 | 203 |  |  |  | C |  | rerun 2 s1 (R-9 paths) |
| std/time/tests_time.mk | 46 | 46 |  |  |  |  | t |  |
| std/time/time.mk | 46 | 46 |  |  |  |  |  | rerun s2 (no change) |

225 files, 62269 lines now (55974 in the files of lines-before.txt). Read line by line under the async bar: 166 files; non-test files with atomics, locks or waits: 67, of them read: 67.
