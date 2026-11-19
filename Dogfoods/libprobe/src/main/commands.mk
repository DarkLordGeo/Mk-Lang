// Why: each command is one question about a library: does it load, what
// does it export, what version is it.
// What: OpenCommand, ScanCommand, CheckCommand, CallCommand, ExportsCommand
// and VersionCommand.
// How: each prints its report on standard output and its errors on standard
// error, and returns the exit status.

import std.dl
import std.fs
import plat
import src.pe
import src.probe
import src.reqs

// The search for `name`: the one given by --search, else path for a name
// with a separator, else system.
SearchFor(o: &Options, name: str): dl.Search {
    if s := probe.ParseSearch(o.search) {
        return s
    }
    if name.Has("/") || name.Has("\\") {
        return dl.Search.Path
    }
    return dl.Search.System
}

// The exit status for a load that failed with `e`.
LoadStatus(e: dl.Error): int {
    return match e {
        dl.Unsupported { ExitUnsupported }
        _ { ExitMissing }
    }
}

// Reports a library that did not load, and returns the exit status.
LoadFailed(sys: &System, name: str, search: dl.Search, e: dl.Error): WriteError!int {
    why := probe.Explain(sys.arena, e) else { "out of memory" }
    try sys.PrintErr("libprobe: {name}: cannot load under {s}: {why} ({e})\n", (name, s: probe.SearchText(search), why, e))
    return LoadStatus(e)
}

// The `open` command.
OpenCommand(sys: &System, o: &Options): WriteError!int {
    name := o.positional[0]
    search := SearchFor(o, name)
    symbols := o.positional[1..]
    r := probe.Probe(sys.arena, sys.io, name, search, symbols, o.keep) else {
        try sys.PrintErr("libprobe: out of memory\n")
        return ExitFile
    }
    if e := r.err {
        return try LoadFailed(sys, name, search, e)
    }
    try sys.Print("{name}: loaded ({s})\n", (name, s: probe.SearchText(search)))
    var missing := 0
    for l in r.symbols {
        if l.found {
            try sys.Print("  {n}: found\n", (n: l.name))
        } else {
            try sys.Print("  {n}: missing\n", (n: l.name))
            missing += 1
        }
    }
    if o.keep {
        try sys.Print("{name}: kept loaded\n", (name))
    } else {
        try sys.Print("{name}: closed\n", (name))
    }
    if missing > 0 {
        try sys.PrintErr("libprobe: {name}: {missing} of {n} symbols missing\n", (name, missing, n: r.symbols.Len()))
        return ExitMissing
    }
    return 0
}

// The searches `scan` tries, in its columns' order.
const Searches: [3]dl.Search = [dl.Search.System, dl.Search.Application, dl.Search.Path]

// The `scan` command: one row a library, one column a search.
ScanCommand(sys: &System, o: &Options): WriteError!int {
    try sys.Print("{l:<24} {s:<13} {a:<13} {p}\n", (l: "LIBRARY", s: "SYSTEM", a: "APPLICATION", p: "PATH"))
    var unloadable := 0
    var unsupported := false
    for name in o.positional {
        var cells: [3]str = ["", "", ""]
        var any := false
        var i := 0
        for s in Searches {
            cells[i] = match probe.Loads(sys.io, name, s) {
                some(e) {
                    if probe.IsUnsupported(e) {
                        unsupported = true
                    }
                    probe.Short(e)
                }
                none {
                    any = true
                    "loaded"
                }
            }
            i += 1
        }
        if !any {
            unloadable += 1
        }
        try sys.Print("{l:<24} {s:<13} {a:<13} {p}\n", (l: name, s: cells[0], a: cells[1], p: cells[2]))
    }
    if unsupported {
        try sys.PrintErr("libprobe: this system cannot load libraries at run time\n")
        return ExitUnsupported
    }
    if unloadable > 0 {
        try sys.PrintErr("libprobe: {n} of {m} libraries load under no search\n", (n: unloadable, m: o.positional.Len()))
        return ExitMissing
    }
    return 0
}

// The `check` command.
CheckCommand(sys: &System, o: &Options): WriteError!int {
    path := o.positional[0]
    text := fs.ReadText(sys.arena, sys.io, path) else e {
        try sys.PrintErr("libprobe: {path}: {e}\n", (path, e))
        return ExitFile
    }
    needs := reqs.Parse(sys.arena, text) else e {
        match e {
            reqs.Syntax(line, problem) {
                try sys.PrintErr("libprobe: {path}:{line}: {p}\n", (path, line, p: problem.Text()))
            }
            OutOfMemory {
                try sys.PrintErr("libprobe: out of memory\n")
            }
        }
        return ExitFile
    }
    var failed := 0
    var unsupported := false
    for need in &needs {
        search := probe.ParseSearch(need.search) else { dl.Search.System }
        r := probe.Probe(sys.arena, sys.io, need.name, search, need.symbols[..], false) else {
            try sys.PrintErr("libprobe: out of memory\n")
            return ExitFile
        }
        if e := r.err {
            if probe.IsUnsupported(e) {
                unsupported = true
            }
            try sys.Print("line {n}: {name}: cannot load ({w})\n", (n: need.line, name: need.name, w: probe.Short(e)))
            failed += 1
            continue
        }
        var missing := List[str].New(sys.arena)
        for l in r.symbols {
            if !l.found {
                missing.Push(l.name) else {
                    try sys.PrintErr("libprobe: out of memory\n")
                    return ExitFile
                }
            }
        }
        if missing.Len() == 0 {
            try sys.Print("line {n}: {name}: ok ({k} found)\n", (n: need.line, name: need.name, k: r.symbols.Len()))
        } else {
            try sys.Print("line {n}: {name}: missing", (n: need.line, name: need.name))
            for m in missing {
                try sys.Print(" {m}", (m))
            }
            try sys.Print("\n")
            failed += 1
        }
    }
    try sys.Print("{ok} of {all} libraries ready\n", (ok: needs.Len() - failed, all: needs.Len()))
    if unsupported {
        return ExitUnsupported
    }
    return if failed > 0 { ExitMissing } else { 0 }
}

// The `call` command.
CallCommand(sys: &System, o: &Options): WriteError!int {
    name := o.positional[0]
    symbol := o.positional[1]
    search := SearchFor(o, name)
    var shape := probe.Shape.NoneToU32
    var text := ""
    if o.positional.Len() == 3 {
        shape = probe.Shape.TextToI32
        text = o.positional[2]
    }
    lib := dl.Open(sys.io, name, search) else e { return try LoadFailed(sys, name, search, e) }
    r := probe.Call(&lib, symbol, shape, text) else e {
        lib.Close(sys.io)
        match e {
            probe.NoSymbol {
                try sys.PrintErr("libprobe: {name}: no symbol {symbol}\n", (name, symbol))
                return ExitMissing
            }
            probe.BadText {
                try sys.PrintErr("libprobe: the text holds a NUL or is over 1023 bytes\n")
                return ExitUsage
            }
        }
    }
    lib.Close(sys.io)
    try sys.Print("{symbol} returned {r}\n", (symbol, r))
    return 0
}

// The path a file argument names: as given, or with --system in the
// system's library directory.
FilePath(sys: &System, o: &Options, file: str): ?str from sys, file {
    if !o.system {
        return some(file)
    }
    dir := plat.SystemDir(sys) else { return none }
    joined := sys.arena.Fmt("{dir}/{file}", (dir, file)) else { return none }
    return some(joined)
}

// The `exports` command.
ExportsCommand(sys: &System, o: &Options): WriteError!int {
    file := o.positional[0]
    path := FilePath(sys, o, file) else {
        try sys.PrintErr("libprobe: --system: no system library directory on {s}\n", (s: plat.Name))
        return ExitUnsupported
    }
    bytes := fs.ReadBytes(sys.arena, sys.io, path) else e {
        try sys.PrintErr("libprobe: {file}: {e}\n", (file, e))
        return ExitFile
    }
    t := pe.Read(sys.arena, bytes) else e {
        why := pe.Describe(sys.arena, e) else { "out of memory" }
        try sys.PrintErr("libprobe: {file}: {why}\n", (file, why))
        return ExitFile
    }
    if o.summary {
        try sys.Print("{dll}: {n} functions, {k} named, ordinal base {b}\n", (dll: t.dll, n: t.exports.Len(), k: t.named, b: t.base))
    }
    var shown := 0
    for x in t.exports {
        if !x.name.StartsWith(o.filter) {
            continue
        }
        if x.name.Len() == 0 && o.filter.Len() > 0 {
            continue
        }
        if o.ordinals {
            try sys.Print("{x:>5}  ", (x: x.ordinal))
        }
        name := if x.name.Len() == 0 { "(by ordinal only)" } else { x.name }
        if x.forward.Len() > 0 {
            try sys.Print("{name} -> {f}\n", (name, f: x.forward))
        } else {
            try sys.Print("{name}\n", (name))
        }
        shown += 1
    }
    if shown == 0 && o.filter.Len() > 0 {
        try sys.PrintErr("libprobe: {file}: no export starts with {f}\n", (file, f: o.filter))
        return ExitMissing
    }
    return 0
}

// The `version` command.
VersionCommand(sys: &System, o: &Options): WriteError!int {
    var status := 0
    for file in o.positional {
        path := FilePath(sys, o, file) else {
            try sys.PrintErr("libprobe: --system: no system library directory on {s}\n", (s: plat.Name))
            return ExitUnsupported
        }
        v := plat.FileVersion(sys.arena, path) else e {
            match e {
                plat.NotHere {
                    try sys.PrintErr("libprobe: version resources are a Windows feature; not on {s}\n", (s: plat.Name))
                    return ExitUnsupported
                }
                plat.NoVersion {
                    try sys.PrintErr("libprobe: {file}: no version resource\n", (file))
                    status = ExitMissing
                }
                OutOfMemory {
                    try sys.PrintErr("libprobe: out of memory\n")
                    return ExitFile
                }
            }
            continue
        }
        if o.summary {
            try sys.Print("{file}: has a version resource\n", (file))
        } else {
            try sys.Print("{file}: {a}.{b}.{c}.{d}\n", (file, a: v[0], b: v[1], c: v[2], d: v[3]))
        }
    }
    return status
}
