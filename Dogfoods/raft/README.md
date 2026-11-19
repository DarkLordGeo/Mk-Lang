# raft

A Raft cluster of five nodes over UDP, run under Mk's deterministic simulator, with a linearizability checker over its clients' history. One seed decides every choice of a run, so a failing seed is a reproducible bug report. It shows `std.sim` (seeded faults, crashes, record and replay), tasks and channels, atomics, and a search over arenas.

## Running it

From the program's folder:

```
cd Dogfoods/raft
mk run -- sim --seeds 1..100 --nodes 3 --clients 2 --ops 20 --partitions --loss 5
```

```text
seeds 1..100: 100 of 100 linearizable, 100 replays identical, history digest 81ca894285ced86d
```

```
raft sim --seed 7 --verbose                         one seed, its history and statistics
raft sim --seeds 1..1000 --partitions --loss 5      a thousand seeds, each checked and replayed
raft sim --seeds 1..20 --partitions --tasks         nodes in tasks of their own
raft sim --seeds 1..300 --crash --partitions        every node's state on disk, one crash per seed
raft sim --seed 7 --record run.rec --history h.txt  keep the recording and the history
raft replay run.rec                                 the same history again, from the recording
raft check h.txt                                    is a history linearizable?
raft --help
```

Exit status: 0 linearizable, 1 not linearizable, 2 a usage error, 3 bad input (a file missing, unreadable or malformed), 4 out of memory (`--memory`), 5 a replay that left its recording, 6 a network error.

### The history format

One operation a line, times in nanoseconds of the simulated clock, `-` for an operation that was never answered:

```
# raft history v1: client call return op key args -> result
0 1000 280029000 get k2 -> 0
2 1000 280029000 cas k1 0 3000001 -> ok
1 300031000 340035000 put k2 2000002
1 360037000 - cas k3 0 2000003 -> ?
```

A recording is one text line of the settings that reproduce the run, then each phase's log as a line `log N` and N bytes (two phases when the cluster crashed).

## Design

- **The protocol** (`src/raft`): leader election with randomised timeouts, log replication, commit by majority, a no-op entry per new leader, and a register store with client sessions so a retried request is applied once. The node does no I/O: it handles a message or its timers and appends what it sends to an outbox.
- **The network** (`src/cluster`): every node and client binds a UDP socket. Each 10 ms step delivers datagrams, lets each endpoint act and sends what they produced. On the way the network partitions, drops, delays, duplicates and corrupts datagrams, and fails socket operations through `sim.Sim.FailNext`, each by a percentage drawn from the seed. Each message is one datagram with a CRC-32.
- **Crash and restart** (`--crash`): each node saves its term, vote and log before its messages go out, by writing a new file, syncing it and renaming it over the old one. The simulator crashes the cluster at a seeded point (`Sim.CrashAfterRandomOps`) and restarts it from its disk (`Sim.Restart`).
- **Nodes as tasks** (`--tasks`): each node runs in a task, waiting with `sync.Select` on its channels, started behind a `sync.Barrier`. Shared counters are `atomic.Atomic` words; the clients' leader hint sits behind a sequence lock with release and acquire fences.
- **Record and replay**: every run is recorded through `sim.Recorder` and replayed through `sim.Replayer`, and the replay must give the same history byte for byte.
- **The checker** (`src/check`): Wing and Gong's search with Lowe's cache of visited states, one register at a time. An operation with no answer may or may not have taken effect.

## Limits

- Membership is fixed: no joint consensus, no log compaction or snapshots. A node rewrites its whole state file on every change.
- `--crash` runs the nodes in the driving task, so `--crash --tasks` is a usage error.
- A run is at most 64 operations per register, the checker's limit (`--clients` times `--ops`).
- Reads go through the log; there is no lease or read-index fast path.
- The program runs only under the simulator. Its sockets are the simulator's, on loopback addresses.

## Tests

The cases in `cases/` cover thousands of seeds with partitions, loss and crashes, a deliberately broken build that the checker catches (`--stale`), malformed and damaged histories and recordings, a memory budget, and one and sixteen carriers. Run them from the repository root with `go run ./tools/dogfood run -only raft`.

## Section 18 lines

The standard-library lines this program uses, as `doc/reference.md` lists them; `go run ./tools/dogfood cover` reads this block.

```mk
// std.net. Listener, Stream and Error below are net.Listener, net.Stream and net.Error
net.ParseAddr(s: str): net.Error!Addr
net.Resolve(a: &Arena, io: Io, host: str): net.Error|OutOfMemory!List[Addr] from a
Addr.Format(&self, w: mut Writer): WriteError!void
net.Bind(io: Io, addr: str): net.Error!Datagram
Datagram.Send(&self, io: Io, data: []u8, to: Addr): net.Error!void
Datagram.Recv(&self, io: Io, buf: mut []u8): net.Error!(n: int, peer: Addr)
Datagram.Port(&self): int
Datagram.Close(self)
struct Addr {
// std.proc. Signal below is proc.Signal
proc.Exit(code: int): never
// std.sync. Shared below is sync.Shared
sync.Select[R: Fields[Selectable] + Plain](io: Io, chans: R): Canceled!int
Barrier.New(n: int): Barrier
Barrier.Wait(&self, io: Io): Canceled!bool
Once[T].New(): Once[T]
Once[T].Get(&self, io: Io, const init: fn(): T): Canceled!&T from self
Once[T].Into(self): ?T
// std.atomic
Atomic[T].New(x: T): Atomic[T]
Atomic[T].Load(&self, const o: Order): T
Atomic[T].Store(&self, x: T, const o: Order)
Atomic[T].Swap(&self, x: T, const o: Order): T
Atomic[T].CompareExchange(&self, old: T, new: T, const success: Order, const failure: Order): (ok: bool, value: T)
Atomic[T].CompareExchangeWeak(&self, old: T, new: T, const success: Order, const failure: Order): (ok: bool, value: T)
Atomic[T].FetchAdd(&self, x: T, const o: Order): T
Atomic[T].FetchSub(&self, x: T, const o: Order): T
Atomic[T].FetchAnd(&self, x: T, const o: Order): T
Atomic[T].FetchOr(&self, x: T, const o: Order): T
Atomic[T].FetchXor(&self, x: T, const o: Order): T
const Relaxed: Order = Order(v: 0)
const Acquire: Order = Order(v: 1)
const Release: Order = Order(v: 2)
const AcqRel: Order = Order(v: 3)
const SeqCst: Order = Order(v: 4)
Order.CanLoad(self): bool
Order.CanStore(self): bool
Order.CanFence(self): bool
atomic.Fence(const o: Order)
// std.time. Duration and Instant below are time.Duration and time.Instant
*
// std.math
Rng.New(seed: u64): Rng
Rng.FromIo(io: Io): Canceled!Rng
Rng.U64(mut self): u64
Rng.Below(mut self, n: u64): u64
Rng.Float(mut self): f64
Rng.Fill(mut self, buf: mut []u8)
// std.coll
HeapDeque[T].New(a: &Alloc): HeapDeque[T]
HeapDeque[T].Push(mut self, a: &Alloc, x: T into self): OutOfMemory!void
HeapDeque[T].PushFront(mut self, a: &Alloc, x: T into self): OutOfMemory!void
HeapDeque[T].Pop(mut self): ?T
HeapDeque[T].PopFront(mut self): ?T
HeapDeque[T].Get(&self, i: int): ?&T
HeapDeque[T].Len(&self): int
HeapDeque[T].Free(self, a: &Alloc)
HeapDeque[T].FreeEmpty(self, a: &Alloc)
HeapDeque[T].FreeEach(self, a: &Alloc, const release: fn(T))
PriorityQueue[T].New(a: &Arena): PriorityQueue[T]
PriorityQueue[T].Push(mut self, x: T into self): OutOfMemory!void
PriorityQueue[T].Pop(mut self): ?T
PriorityQueue[T].Peek(&self): ?&T
PriorityQueue[T].Len(&self): int
Bitset.New(a: &Arena, bits: int): OutOfMemory!Bitset
Bitset.Set(mut self, i: int)
Bitset.Remove(mut self, i: int)
Bitset.Has(&self, i: int): bool
Bitset.Count(&self): int
Bitset.Len(&self): int
// std.sim
*
```
