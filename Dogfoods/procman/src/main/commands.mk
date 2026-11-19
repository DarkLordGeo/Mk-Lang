// Why: the commands other than `start` each do one thing from the Procfile
// or the run directory.
//
// What: RunTask, Check, Status, Ps, ShowLogs and Clean.
//
// How: each reads what it needs and returns an exit code.

import std.fs
import std.log
import std.proc
import src.procfile
import src.rundir
import src.status

/// The most a `status` answer may hold.
const AnswerLimit: int = 1 << 20

// `procman run TASK [ARG...]`: runs a task (or a process) once, with the
// ARGs added, shows what it wrote and exits with its code.
RunTask(sys: &System, cfg: &Config): WriteError!int {
    var err := sys.Stderr()
    var logs := ConsoleLogs(mut err, cfg.level)
    if cfg.args.Len() == 0 {
        try sys.PrintErr("procman: run takes the name of a task\n")
        return 2
    }
    loaded := try LoadProcfile(sys, cfg, mut logs)
    entries := loaded else {
        return 2
    }
    name := cfg.args[0]
    e := procfile.Find(entries.Items(), name) else {
        try sys.PrintErr("procman: {path} has no entry `{name}`\n", (path: cfg.procfile, name))
        return 2
    }
    var argv := List[str].New(sys.arena)
    for w in e.argv {
        argv.Push(sys.arena.Dup(w) else { return 1 }) else { return 1 }
    }
    for w in cfg.args[1..] {
        argv.Push(sys.arena.Dup(w) else { return 1 }) else { return 1 }
    }
    logs.Debug("{name}: running with a limit of {n} bytes", (name, n: e.limit))
    r := proc.Run(sys.arena, sys.io, argv.Items(), b"", e.limit) else x {
        why := Why(sys.arena, x, &e)
        try sys.PrintErr("procman: {name}: {why}\n", (name, why))
        return match x {
            proc.NotFound { 127 }
            proc.Denied { 126 }
            _ { 125 }
        }
    }
    var out := sys.Stdout()
    try out.Bytes(r.stdout)
    try out.Flush()
    var errw := sys.Stderr()
    try errw.Bytes(r.stderr)
    level := if r.code == 0 { log.Level.Debug } else { log.Level.Warn }
    logs.At(level, "{name}: exited with code {code}", (name, code: r.code))
    return r.code
}

// `procman check`: reads the Procfile and lists what it declares.
Check(sys: &System, cfg: &Config): WriteError!int {
    var err := sys.Stderr()
    var logs := ConsoleLogs(mut err, cfg.level)
    loaded := try LoadProcfile(sys, cfg, mut logs)
    entries := loaded else {
        return 2
    }
    for e in entries {
        kind := if e.kind == procfile.Kind.Task { "task" } else { "process" }
        restart := match e.restart {
            .Never { "never" }
            .OnFailure { "on-failure" }
            .Always { "always" }
        }
        try sys.Print("{name:<12} {kind:<8} restart={restart} retries={r} backoff={b}ms limit={l}{m}\n", (name: e.name, kind, restart, r: e.retries, b: e.backoff_ms, l: e.limit, m: if e.main { " main" } else { "" }))
        try sys.Print("    {argv}\n", (argv: e.argv))
    }
    return 0
}

// `procman status [PATH...]`: asks the status port of a running procman.
Status(sys: &System, cfg: &Config): WriteError!int {
    addr := cfg.status else {
        dir := rundir.RunDir(path: cfg.run_dir)
        text := dir.Read(sys.arena, sys.io, "status.port") else e {
            try sys.PrintErr("procman: run directory {d}: {e}\n", (d: cfg.run_dir, e))
            return 3
        }
        t := text else {
            try sys.PrintErr("procman: no status port in {d}: start procman with --status\n", (d: cfg.run_dir))
            return 4
        }
        t.Trim()
    }
    var paths := cfg.args
    if paths.Len() == 0 {
        paths = DefaultPaths
    }
    var code := 0
    for p in paths {
        var scratch := Arena.Sub(sys.arena) else {
            return 1
        }
        defer scratch.Free()
        path := scratch.Fmt("/{p}", (p)) else {
            return 1
        }
        answer := status.Get(&scratch, sys.io, addr, path, AnswerLimit) else e {
            match e {
                status.NoReply {
                    try sys.Print("{p}: no reply\n", (p))
                    code = 4
                    continue
                }
                _ {
                    try sys.PrintErr("procman: status port {addr}: {e}\n", (addr, e))
                    return 4
                }
            }
        }
        try sys.Print("{p}: {c}\n", (p, c: answer.code))
        var out := sys.Stdout()
        try out.Str(answer.body)
    }
    return code
}

const DefaultPaths: []str = ["status"]

// `procman ps`: the state each process left in the run directory.
Ps(sys: &System, cfg: &Config): WriteError!int {
    dir := rundir.RunDir(path: cfg.run_dir)
    names := dir.List(sys.arena, sys.io, ".state") else e {
        match e {
            fs.NotFound { List[str].New(sys.arena) }
            _ {
                try sys.PrintErr("procman: run directory {d}: {e}\n", (d: cfg.run_dir, e))
                return 3
            }
        }
    }
    if names.Len() == 0 {
        try sys.Print("no processes have run here\n")
        return 0
    }
    var out := sys.Stdout()
    for name in names {
        text := dir.Read(sys.arena, sys.io, name) else e {
            try sys.PrintErr("procman: {name}: {e}\n", (name, e))
            return 3
        }
        if t := text {
            try out.Str(t)
        }
    }
    return 0
}

// `procman logs NAME`: the output of NAME's last run, as the run directory
// keeps it.
ShowLogs(sys: &System, cfg: &Config): WriteError!int {
    if cfg.args.Len() != 1 {
        try sys.PrintErr("procman: logs takes one process name\n")
        return 2
    }
    name := cfg.args[0]
    dir := rundir.RunDir(path: cfg.run_dir)
    file := sys.arena.Fmt("{name}.out", (name)) else {
        return 1
    }
    text := dir.Read(sys.arena, sys.io, file) else e {
        try sys.PrintErr("procman: {name}: {e}\n", (name, e))
        return 3
    }
    t := text else {
        try sys.PrintErr("procman: no output saved for {name}\n", (name))
        return 1
    }
    var out := sys.Stdout()
    try out.Str(t)
    return 0
}

// `procman clean`: removes what procman wrote in the run directory, and the
// directory when nothing else is left in it.
Clean(sys: &System, cfg: &Config): WriteError!int {
    info := fs.Stat(sys.io, cfg.run_dir) else e {
        match e {
            fs.NotFound {
                try sys.Print("nothing to clean\n")
                return 0
            }
            _ {
                try sys.PrintErr("procman: {d}: {e}\n", (d: cfg.run_dir, e))
                return 3
            }
        }
    }
    if info.kind != fs.Kind.Dir {
        try sys.PrintErr("procman: {d} is not a directory\n", (d: cfg.run_dir))
        return 3
    }
    dir := rundir.RunDir(path: cfg.run_dir)
    (removed, kept) := dir.Clean(sys.arena, sys.io) else e {
        try sys.PrintErr("procman: {d}: {e}\n", (d: cfg.run_dir, e))
        return 3
    }
    try sys.Print("removed {removed} files\n", (removed))
    if kept > 0 {
        try sys.PrintErr("procman: kept {d}, which holds files procman did not write ({kept})\n", (d: cfg.run_dir, kept))
    }
    return 0
}
