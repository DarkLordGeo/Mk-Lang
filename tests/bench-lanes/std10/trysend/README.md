# trysend (idiomatic table)

One row under tests/bench/README.md's contract for the idiomatic table: a bounded channel's send that never waits, each language through its standard library. Only `tools/bench` publishes a number from it.

## Task

Usage `trysend <reps>`. One task or thread and one channel of i64 with capacity 1,024. Each round tries 1,040 sends of the values 1, 2, 3, ... (the count runs on across rounds): the first 1,024 go in, the last 16 find the channel full and come back to the sender, who adds them to a wrapping sum. Then 1,024 receives drain the channel, each value added to the sum. So the checksum for R rounds is the sum of 1 to 1,040 R. No task ever waits for another: the row prices the channel's lock and ring, not a handoff (the `chan` row, tests/bench/idiomatic/chan, prices that).

## Columns

- **Mk**: `Chan[i64].TrySend` (std/core/sync_chan.mk: the queue lock without an Io, as Close takes it, then the ring) and `Recv`.
- **Go**: `select { case c <- v: default: }` on a buffered `chan int64`, and `<-c`.
- **Rust**: `std::sync::mpsc::SyncSender::try_send`, whose `TrySendError::Full` gives the value back, and `recv`.
- **C, Zig**: no channel in std (skipped).

The columns differ in work beyond the task: Mk's Recv checks for cancellation first (a cancellation point), Go's receive and select run the runtime's channel code, and Rust's sync_channel is std's array channel with its own waker bookkeeping.

## Checksums

Built and run on 2026-11-18 (checksums only, no timing): Mk, Go and Rust give 54085200 at `10` and 5408052000 at `100`.
