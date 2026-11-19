// Why: other procman commands and the status workers read what `start`
// leaves on disk, and must never see a half-written file.
//
// What: RunDir with Open, File, Replace, SaveOutput, List, Read and Clean.
//
// How: every file is written whole to a `.tmp` name and renamed over the
// old one; the layout is below.

// The run directory: what procman leaves for other procman commands and for
// its status workers to read. Every file is written whole to a `.tmp` name and
// then renamed over the old one, so a reader sees the old content or the new,
// never a part. Layout:
//
//   <name>.state     one line per process: its state, runs, restarts, last code
//   <name>.out       the output of the process's last run
//   <name>.out.1     the run before it
//   status.port      the port the status workers listen on
//   procman.log      the manager's log, each line timed

import std.fs

/// A file stands where the run directory should be.
export error NotADirectory
/// What a run directory operation can fail with.
export type Error = NotADirectory | fs.Error | OutOfMemory

/// A run directory that exists. Plain, so every task may hold one.
export struct RunDir {
    export path: str
}

/// Opens the run directory at `path`, making it when it does not exist.
/// Fails with `NotADirectory` when a file is in the way. One `Stat`, and one
/// `MakeDir` the first time; allocates nothing.
export Open(io: Io, path: str): Error!RunDir from path {
    info := fs.Stat(io, path) else e {
        match e {
            fs.NotFound {
                try fs.MakeDir(io, path)
                return RunDir(path: path)
            }
            _ { fail e }
        }
    }
    if info.kind != fs.Kind.Dir {
        fail NotADirectory
    }
    return RunDir(path: path)
}

/// The path of `file` inside the run directory, in `a`.
export RunDir.File(&self, a: &Arena, file: str): OutOfMemory!str from a {
    return try a.Fmt("{dir}/{file}", (dir: self.path, file))
}

/// Writes `data` as the whole content of `file`: to `file.tmp`, synced, then
/// renamed over `file`. A failure, or a crash at any point, leaves the old
/// content or the new, and a failure removes the temporary file. One sync
/// and one rename beyond the write; allocates both paths in a sub-arena of `a`.
export RunDir.Replace(&self, a: &Arena, io: Io, file: str, data: []u8): Error!void {
    var scratch := try a.Sub()
    defer scratch.Free()
    dst := try self.File(&scratch, file)
    tmp := try scratch.Fmt("{dst}.tmp", (dst))
    WriteSynced(io, tmp, data) else e {
        fs.Remove(io.NoCancel(), tmp) else {}
        fail e
    }
    fs.Rename(io, tmp, dst) else e {
        fs.Remove(io.NoCancel(), tmp) else {}
        fail e
    }
}

WriteSynced(io: Io, path: str, data: []u8): fs.Error!void {
    var f := try fs.Create(io, path)
    onerr f.Abandon()
    try f.WriteAll(io, data)
    try f.Sync(io)
    try f.Close(io)
}

/// Keeps the last two outputs of a process: `<name>.out` moves to
/// `<name>.out.1`, replacing it, and `data` becomes `<name>.out`.
export RunDir.SaveOutput(&self, a: &Arena, io: Io, name: str, data: []u8): Error!void {
    var scratch := try a.Sub()
    defer scratch.Free()
    cur := try scratch.Fmt("{name}.out", (name))
    path := try self.File(&scratch, cur)
    old := try scratch.Fmt("{path}.1", (path))
    fs.Rename(io, path, old) else e {
        match e {
            fs.NotFound {}
            _ { fail e }
        }
    }
    try self.Replace(&scratch, io, cur, data)
}

/// The file names in the run directory that end with `suffix`, sorted, in
/// `a`. O(n log n) in the entries; a name that is not UTF-8 is skipped, since
/// procman never writes one.
export RunDir.List(&self, a: &Arena, io: Io, suffix: str): Error!List[str] from a {
    var names := List[str].New(a)
    var d := try fs.Dir.Open(io, self.path)
    defer d.Close()
    for {
        entry := try d.Next(a, io) else {
            break
        }
        if entry.is_dir {
            continue
        }
        name := entry.name.Str() else {
            continue
        }
        if name.EndsWith(suffix) {
            try names.Push(name)
        }
    }
    names.Sort(fn(x: &str, y: &str): bool { return *x < *y })
    return names
}

/// Reads `file` from the run directory into `a`; `none` when it does not
/// exist.
export RunDir.Read(&self, a: &Arena, io: Io, file: str): Error!?str from a {
    path := try self.File(a, file)
    text := fs.ReadText(a, io, path) else e {
        match e {
            fs.NotFound { return none }
            _ { fail e }
        }
    }
    return some(text)
}

/// Removes every file procman wrote and then the directory itself, and
/// returns how many files went. A file procman did not write is left, and so
/// is the directory then, so a mistaken `--run-dir` cannot empty a tree.
export RunDir.Clean(&self, a: &Arena, io: Io): Error!(removed: int, kept: int) {
    var scratch := try a.Sub()
    defer scratch.Free()
    var removed := 0
    var kept := 0
    var d := try fs.Dir.Open(io, self.path)
    defer d.Close()
    for {
        entry := try d.Next(&scratch, io) else {
            break
        }
        name := entry.name.Str() else {
            kept += 1
            continue
        }
        if entry.is_dir || !IsOurs(name) {
            kept += 1
            continue
        }
        try fs.Remove(io, try self.File(&scratch, name))
        removed += 1
    }
    if kept == 0 {
        try fs.Remove(io, self.path)
    }
    return (removed, kept)
}

IsOurs(name: str): bool {
    return name.EndsWith(".state") || name.EndsWith(".out") || name.EndsWith(".out.1") || name.EndsWith(".tmp") || name == "status.port" || name == "procman.log"
}
