# cron

A scheduler daemon in the manner of cronie, with a time zone per entry. It reads a crontab, runs each job as the leader of its own process group when it falls due, logs the job's output as it arrives, kills a job's whole group when its timeout runs out, and refuses to run twice on one lock file. It shows `std.time`'s zones, `std.proc` process groups, tasks and scopes with deadlines, and `std.sim`'s simulated clock.

## Running it

From the program's folder:

```
cd Dogfoods/cron
mk run --bin cron . -- next cases/data/zones.tab --from 2026-03-28T00:00:00Z --count 3
```

```text
nightly (30 2 * * *, Europe/Berlin):
  2026-03-28 02:30:00 +01:00 (2026-03-28T01:30:00Z)
  2026-03-29 03:00:00 +02:00 (2026-03-29T01:00:00Z), skipped time run at the transition
  2026-03-30 02:30:00 +02:00 (2026-03-30T00:30:00Z)
hourly (0 * 29 3 *, Europe/Berlin):
  2026-03-29 00:00:00 +01:00 (2026-03-28T23:00:00Z)
  2026-03-29 01:00:00 +01:00 (2026-03-29T00:00:00Z)
  2026-03-29 03:00:00 +02:00 (2026-03-29T01:00:00Z), skipped time run at the transition
early (30 1 * * sun, America/New_York):
  2026-03-29 01:30:00 -04:00 (2026-03-29T05:30:00Z)
  2026-04-05 01:30:00 -04:00 (2026-04-05T05:30:00Z)
  2026-04-12 01:30:00 -04:00 (2026-04-12T05:30:00Z)
howe-back (45 1 * * *, Australia/Lord_Howe):
  2026-03-29 01:45:00 +11:00 (2026-03-28T14:45:00Z)
  2026-03-30 01:45:00 +11:00 (2026-03-29T14:45:00Z)
  2026-03-31 01:45:00 +11:00 (2026-03-30T14:45:00Z)
howe-fwd (15 2 * * *, Australia/Lord_Howe):
  2026-03-29 02:15:00 +11:00 (2026-03-28T15:15:00Z)
  2026-03-30 02:15:00 +11:00 (2026-03-29T15:15:00Z)
  2026-03-31 02:15:00 +11:00 (2026-03-30T15:15:00Z)
```

The commands:

| Command | What it does |
|---|---|
| `cron check cases/data/zones.tab` | List the entries and their zones, or every problem with its line |
| `cron next TAB --from TIME --count N` | The next N fires of each entry |
| `cron zone Europe/Berlin --year 2026` | A zone's transitions in a year |
| `cron run TAB --echo` | The daemon, until Ctrl+C (or `--stop-after 90s`) |
| `cron sim TAB` | A year of the crontab under the simulated clock, in a fraction of a second |

The package has a second bin, `jobkit`: small jobs (print, sleep, exit with a code, copy standard input, leave a child behind, wait for an interrupt) that the sample crontabs run, so a job behaves the same on Windows and Linux with no shell.

`cron --help` lists the options. Exit codes: 0 done; 1 an I/O failure; 2 a bad command line or crontab; 3 another instance holds the lock; 4 the memory budget (`--max-memory`) was exceeded.

### The crontab

```
# comment
NAME=value                   a variable: in every job's environment, and $NAME or ${NAME} in commands
CRON_TZ=Europe/Berlin        the zone of the entries that follow, from the system's zone database
                             or the one compiled into cron (std.time.tzdata)
CRON_TZ=local                the host's zone; also the zone of entries before any CRON_TZ line
CRON_TZFILE=zones/x.tzif     a zone from a TZif file, relative to the crontab
<schedule> <name> [options] -- <command> [| <command>]
```

A schedule is the five fields of crontab(5) (minute, hour, day of month, month, day of week; lists, ranges, steps, month and day names; when both day fields are restricted a day matching either runs), or `@yearly`, `@annually`, `@monthly`, `@weekly`, `@daily`, `@midnight`, `@hourly`, `@reboot` (once, when the daemon starts) or `@every <duration>` (1 s or more).

| Option | Meaning |
|---|---|
| `timeout=<duration>` | Kill the job's group when it runs longer |
| `detach` | The job writes straight to the log and is left running when the daemon stops |
| `input=<text>` | Written to the job's standard input; `\n` is a line break |

A command is words, with double quotes keeping spaces, and no shell; one `|` makes a pipeline of two stages. `$CRON_DIR` is the directory of the cron executable, `$CRON_LOCK` the lock file and `$CRON_TAB` the crontab.

The daemon takes the lock file (`--lock`, default `cron.lock`) at once or after `--lock-wait`, logs to `--log` (default `cron.log`, opened for appending; `--echo` copies each record to standard error without its time and process id), runs at most `--max-jobs` jobs at once and never a job beside its own last run. On an interrupt or at `--stop-after` it forwards an interrupt to each running job's group (on Linux; on Windows a group cannot be interrupted, so it is killed), gives it 5 seconds, then kills it; detached jobs are left running. It prints a line of counts per job when it stops.

## Design

- Daylight-saving transitions follow cronie and Vixie cron: a local time a transition skips runs once, at the transition; a local time it repeats runs once, at its first occurrence. An entry fires at most once at any instant.
- Zones come from the system's database, then from `std.time.tzdata` compiled into the binary, so a crontab means the same instants on Windows, Linux and under simulation.
- Each fire runs in a task of the loop's scope. A job's timeout is a deadline on the scope around `Wait`; on expiry the group is killed and reaped under a fresh scope from `io.NoCancel()`, and detached if even that fails, so the daemon never hangs on a child.
- One daemon per crontab: an exclusive advisory lock on the lock file, held for the daemon's life.
- The job table lives on the allocator the daemon is given, so `--max-memory` bounds it.

## Limits

- A missed fire (the machine slept) is not run late: the next fire is counted from the wake.
- Under `std.sim` nothing is spawned: `sim` records each fire with its local time, since `proc.Spawn` is `Unsupported` there.
- A job's output line over 4 KiB, or output that is not UTF-8, is counted, not logged.

## Tests

The cases in `cases/` cover schedules and zones across transitions (`next-*`, `zone-*`, `sim-*`), the lock, timeouts, interrupts, detached jobs, memory budgets and log faults under `std.sim`. Run them from the repository root with `go run ./tools/dogfood run -only cron`.

## Section 18 lines
The standard-library lines this program uses, as `doc/reference.md` lists them; `go run ./tools/dogfood cover` reads this block.

```
Io.ScopeWith(self, l: Limits): Scope
Io.NoCancel(self): Io
Io.Sleep(self, d: time.Duration): Canceled!void
Scope.Cancel(&self)
Scope.CancelAfter(&self, d: time.Duration)
Scope.Expired(&self): bool
struct Limits {
Reader.Line(mut self, a: &Arena): ReadError|OutOfMemory!?str from a
Writer.Str(mut self, s: str): WriteError!void
Writer.Flush(mut self): WriteError!void
HeapList[T].New(a: &Alloc): HeapList[T]
HeapList[T].Push(mut self, a: &Alloc, x: T into self): OutOfMemory!void
HeapList[T].Free(self, a: &Alloc)
// std.fs. File and Error below are fs.File and fs.Error
fs.ReadText(a: &Arena, io: Io, p: str): fs.Error|OutOfMemory!str from a
fs.ReadBytes(a: &Arena, io: Io, p: str): fs.Error|OutOfMemory![]u8 from a
fs.WriteAll(io: Io, p: str, data: []u8): fs.Error!void
fs.Stat(io: Io, p: str): fs.Error!Info
File.Close(self, io: Io): fs.Error!void
File.Abandon(self)
fs.OpenWith(io: Io, p: str, o: Options): fs.Error!File
File.Lock(&self, io: Io, mode: LockMode): fs.Error!void
File.TryLock(&self, io: Io, mode: LockMode): fs.Error!bool
File.Unlock(&self, io: Io): fs.Error!void
struct Options {
enum LockMode {
// std.proc. Signal below is proc.Signal
proc.WaitSignal(io: Io, s: Signal): Canceled!void
enum Signal {
proc.Pipe(io: Io): proc.Error!(r: PipeReader, w: PipeWriter)
PipeReader.Read(&self, io: Io, buf: mut []u8): proc.Error!int
PipeReader.Reader(&self, io: Io, buf: mut []u8): Reader from self, buf
PipeReader.Close(self)
PipeWriter.WriteAll(&self, io: Io, data: []u8): proc.Error!void
PipeWriter.Writer(&self, io: Io, buf: mut []u8): Writer from self, buf
PipeWriter.Close(self)
proc.Spawn(a: &Arena, io: Io, c: &Command): proc.Error!(child: Child, stdin: ?PipeWriter, stdout: ?PipeReader, stderr: ?PipeReader)
Child.Wait(self, io: Io): proc.Error!Status
Child.Signal(&self, s: Signal): proc.Error!void
Child.Kill(&self): proc.Error!void
Child.Pid(&self): i64
Child.Detach(self)
struct Command {
enum Env {
enum Stdio {
struct Status {
// std.sync. Shared below is sync.Shared
Mutex[T].New(x: T): Mutex[T]
Mutex[T].Lock(&self, io: Io): Canceled!Guard[T]
Mutex[T].Into(self): T
Guard[T].Mut(mut self): mut T
Guard[T].Unlock(self)
// std.time. Duration and Instant below are time.Duration and time.Instant
time.Seconds(n: i64): Duration
time.Millis(n: i64): Duration
time.Minutes(n: i64): Duration
time.Hours(n: i64): Duration
time.Nanos(n: i64): Duration
Duration.Nanos(self): i64
Duration.Format(&self, w: mut Writer): WriteError!void
time.ParseDuration(s: str): ParseError!Duration
time.Wall(io: Io): Time
time.FromUnix(seconds: i64, nanos: i64): Time
Time.UnixNanos(self): i64
Time.Add(self, d: Duration): Time
Time.Sub(self, t: Time): Duration
Time.In(self, z: &Zone): Date
Time.Format(&self, w: mut Writer): WriteError!void
Date.Time(self): Time
time.FromDate(year: i32, month: int, day: int, hour: int, minute: int, second: int, nanos: int, z: &Zone): time.Error!Time
Zone.Utc(): Zone
Zone.FromTzif(a: &Arena, name: str, data: []u8): time.Error|OutOfMemory!Zone from a
Zone.Load(a: &Arena, io: Io, name: str, embedded: ?Database): time.Error|OutOfMemory!Zone from a
Zone.Local(a: &Arena, io: Io): time.Error|OutOfMemory!Zone from a
time.LocalName(a: &Arena, io: Io): ?str from a
time.ParseRfc3339(s: str): ParseError!Time
error BadName
error BadData
error NotFound
error NoDatabase
error Skipped
error Repeated
error OutOfRange
struct Date {
struct Database {
// std.mem
Fixed.New(buf: mut []u8): Fixed
Fixed.Alloc(&self): &Alloc from self
// std.cli. Error below is cli.Error
cli.Args(g: mut Gen, t: Type)
// std.log
log.Stderr(level: Level): Sink
log.ToFile(f: &Mutex[fs.File], level: Level): Sink from f
struct Sink {
Sink.Info[A: Fields[Format] + Plain](&self, io: Io, const f: str, args: A): WriteError!void
Sink.Warn[A: Fields[Format] + Plain](&self, io: Io, const f: str, args: A): WriteError!void
Sink.Error[A: Fields[Format] + Plain](&self, io: Io, const f: str, args: A): WriteError!void
enum Level {
// std.sim
Sim.New(a: &Arena, seed: u64): OutOfMemory!Sim
Sim.Io(&self): Io
Sim.FailNext(mut self, op: Op, f: Fault)
Sim.Free(self)
enum Op {
enum Fault {
```
