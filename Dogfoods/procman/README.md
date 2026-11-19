# procman

A process manager and task runner in the manner of foreman. It reads a Procfile, starts every process in it, shows their output, restarts them under a policy with an exponential back-off, and stops them all when one marked `main` ends, when a signal comes or when `--stop-after` runs out. It shows `std.proc`, tasks that report over a channel, atomics, crash-safe files, and a pool of worker processes under `proc.Supervise`.

## Running it

From the program's folder (the Procfile reads `$PORT` and `$INTERVAL` from the environment):

```
cd Dogfoods/procman
PORT=8080 INTERVAL=5 mk run -- --procfile cases/data/check.Procfile check
```

```text
web          process  restart=never retries=3 backoff=100ms limit=65536 main
    [./bin/server, --port, 8080, --name, my app]
worker       process  restart=on-failure retries=5 backoff=250ms limit=1048576
    [./bin/worker, --queue, jobs high]
clock        process  restart=always retries=3 backoff=2000ms limit=1048576
    [./bin/clock, 5s, $HOME]
migrate      task     restart=never retries=3 backoff=100ms limit=1048576
    [./bin/migrate, up]
```

`check` parses the Procfile and prints each entry with its options and its command split into words. The commands:

| Command | What it does |
|---|---|
| `procman check` | Parse the Procfile and show each entry |
| `procman start [NAME...] [--summary]` | Run the processes (all, or those named) until they end |
| `procman start --status 127.0.0.1:0 --workers 4` | The same, serving a status port from worker processes |
| `procman status health status` | Ask a running procman over its status port |
| `procman run TASK [ARGS...]` | Run a one-off task once |
| `procman ps`, `procman logs NAME` | Show the run directory's state and last output |
| `procman clean` | Remove what procman wrote in the run directory |
| `procman demo help` | List the demo programs Procfiles can run as children |

`procman --help` lists every option. Each option's default comes from an environment variable: `PROCMAN_PROCFILE` (else `Procfile`), `PROCMAN_RUN_DIR` (else `.procman`), `PROCMAN_LOG` (`debug`, `info`, `warn` or `error`; else `info`) and `PROCMAN_STATUS` (the status address).

Exit codes: 0 done; 1 a process failed (its last run exited non-zero, could not run, or procman gave up on it); 2 bad usage or a bad Procfile; 3 the run directory failed; 4 the status port did not answer; `run` gives the task's own code, or 125 (procman failed), 126 (permission denied) or 127 (no such program), as `env` does; 130, 143 and 129 when Ctrl-C, a termination request or a hang-up stopped `start`.

### The Procfile

One entry a line; `#` at the start of a word begins a comment.

```
# name [options]: command
web [main]: ./server --port $PORT
worker [restart=on-failure retries=5 backoff=200ms]: ./worker
clock [restart=always limit=64k]: ./clock
# task name [options]: command, run once by `procman run`
task migrate [limit=1m]: ./migrate up
```

| Option | Meaning | Default |
|---|---|---|
| `restart=never\|on-failure\|always` | Run it again when it ends: never, after a non-zero exit or a failure to run, or always | `never` |
| `retries=N` | Restarts in a row before procman gives up; a run of 10 seconds or more starts the count over | 3 |
| `backoff=D` | The first wait before a restart (`250ms`, `2s`, `1m`), doubled for each restart in a row, at most 30 seconds | `100ms` |
| `limit=SIZE` | The most a run may write, stdout and stderr together (`4096`, `64k`, `1m`); past it the run is killed and fails | `1m` |
| `main` | When this process ends for good, stop the others | off |

The command is split into words as a shell splits a simple command: single quotes keep everything, double quotes keep blanks, a backslash keeps the next byte, and `$NAME` or `${NAME}` is the environment variable's value (`$$` is a dollar). `$PROCMAN` is procman's own path, so `$PROCMAN demo ...` runs one of the demo programs on any OS. There is no shell: no pipes, globs or redirection; write `sh -c '...'` for those.

### The run directory and the status port

The run directory holds `<name>.state` (one line: state, runs, restarts, last exit), `<name>.out` and `<name>.out.1` (the output of the last two runs), `status.port` and `procman.log` (the manager's log, each line timed). `procman clean`, or `start --clean`, removes what procman wrote, and the directory when nothing else is in it.

`start --status ADDR` listens at `ADDR` (`127.0.0.1:0` picks a free port, written to `status.port`) and runs `--workers` copies of procman, each answering HTTP/1.0 `GET /health` and `GET /status` from the run directory. `--status-debug` adds `GET /panic`, which makes the worker that takes it panic.

## Design

- One task per process runs it, restarts it under its policy and reports each step as an `Event` on one channel; only the main task prints, since a `Writer` stays on its task.
- The shared state of a run (how many processes still run, why procman stopped) is atomics only, so it is `Share` and every task holds it.
- Every run-directory file is written whole to a `.tmp` name and renamed over the old one, so a reader sees the old content or the new, never a part.
- The status port runs in worker processes under `proc.Supervise`: a worker that dies takes one connection with it and is restarted while the others keep answering.
- The last outputs of failed processes are kept within `--tail-memory`, a `mem.Fixed` budget counted by `mem.Counting`, so a chatty process cannot grow procman.

## Limits

- `proc.Run` gives a run's output only when it ends, so procman shows each run's output after the run, not line by line as foreman does; with `--status` it shows everything when it stops, since the status workers hold the main task meanwhile.
- Nothing in std sends a signal to a process, so procman cannot stop a child gently first: a stopped run is killed. Its case for a signal delivered to procman runs on Linux only; on Windows a console control event reaches every process on the console.
- Output that is not UTF-8 is shown as a note, not as bytes.

## Tests

Every case runs procman in an empty directory, with its own demo programs as the children, so the cases need no shell and give the same output on Windows and Linux. They cover bad command lines and Procfiles, restarts and give-ups, output floods, crashes, cancellation, memory budgets, the status pool losing a worker, and the run directory under `std.sim` faults. Run them from the repository root with `go run ./tools/dogfood run -only procman`.

## Section 18 lines
The standard-library lines this program uses, as `doc/reference.md` lists them; `go run ./tools/dogfood cover` reads this block.

```text
struct System {
System.Stderr(&self): Writer
System.PrintErr[A: Fields[Format] + Plain](&self, const f: str, args: A): WriteError!void
System.Env(&self, name: str): ?str from self
// std.fs. File and Error below are fs.File and fs.Error
fs.Rename(io: Io, src: str, dst: str): fs.Error!void
fs.Remove(io: Io, p: str): fs.Error!void
fs.MakeDir(io: Io, p: str): fs.Error!void
fs.Stat(io: Io, p: str): fs.Error!Info
Dir.Open(io: Io, p: str): fs.Error!Dir
Dir.Next(mut self, a: &Arena, io: Io): fs.Error|OutOfMemory!?Entry from a
Dir.Close(self)
struct Entry {
struct Info {
// std.proc. Signal below is proc.Signal
*
// std.time. Duration and Instant below are time.Duration and time.Instant
*
// std.cli. Error below is cli.Error
*
// std.log
*
```
