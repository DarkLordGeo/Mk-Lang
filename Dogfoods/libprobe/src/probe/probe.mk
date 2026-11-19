// Why: loading libraries at run time is what libprobe tests.
// What: Probe, Loads, ParseSearch, SearchText, Explain, Lookup and Report.
// How: through std.dl: open a library under a search, look up its symbols,
// call one through its address, and release it.

import std.dl
import std.raw

/// The kinds of function `Call` knows how to call.
export enum Shape {
    /// `fn(): u32`, such as GetTickCount.
    NoneToU32
    /// `fn(*u8): i32` given a NUL-terminated string, such as lstrlenA.
    TextToI32
}

/// What happened to one symbol.
export struct Lookup {
    export name: str
    export found: bool
}

/// What probing one library found: `err` is none when it loaded, and then
/// `symbols` holds one entry per symbol asked for.
export struct Report {
    export name: str
    export search: dl.Search
    export err: ?dl.Error
    export symbols: List[Lookup]
}

/// The symbol is not exported by the library.
export error NoSymbol

/// The text holds a NUL byte, or is longer than `Call` copies.
export error BadText

/// The search a word names: "system", "application" or "path".
export ParseSearch(word: str): ?dl.Search {
    return match word {
        "system" { some(dl.Search.System) }
        "application" { some(dl.Search.Application) }
        "path" { some(dl.Search.Path) }
        _ { none }
    }
}

/// The word for a search, as `ParseSearch` reads it.
export SearchText(s: dl.Search): str {
    return match s {
        .System { "system" }
        .Application { "application" }
        .Path { "path" }
    }
}

/// A short phrase for a loader error, for tables.
export Short(e: dl.Error): str {
    return match e {
        dl.NotFound { "not found" }
        dl.Invalid { "invalid" }
        dl.InitFailed { "init failed" }
        dl.BadName { "bad name" }
        dl.Unsupported { "unsupported" }
        dl.Os { "os error" }
        _ {
            Panic("dl.Error gained a member this match does not handle")
        }
    }
}

/// A sentence for a loader error, for messages.
export Explain(a: &Arena, e: dl.Error): OutOfMemory!str from a {
    return match e {
        dl.NotFound { "not found where the search looks" }
        dl.Invalid { "not a library this system can load" }
        dl.InitFailed { "its initializer failed" }
        dl.BadName { "the name does not fit the search (a bare name for system and application, an absolute path for path)" }
        dl.Unsupported { "this system cannot load libraries at run time" }
        dl.Os(code) { try a.Fmt("the loader failed with system error {code}", (code)) }
        _ {
            Panic("dl.Error gained a member this match does not handle")
        }
    }
}

/// Opens `name` under `search` and looks up each of `symbols`. The library
/// is closed again, or with `keep` detached, so it stays loaded for the
/// life of the process. The report is allocated in `a`.
export Probe(a: &Arena, io: Io, name: str, search: dl.Search, symbols: []str, keep: bool): OutOfMemory!Report from a, name {
    var r := Report(name: name, search: search, err: none, symbols: List[Lookup].New(a))
    lib := dl.Open(io, name, search) else e {
        r.err = some(e)
        return r
    }
    for s in symbols {
        found := !lib.Symbol(s).IsNone()
        // the name is copied into `a`, so the report holds only `a` and `name`
        // and a caller may keep it in the arena the symbols came from
        copy := a.Dup(s) else e {
            lib.Close(io)
            fail e
        }
        r.symbols.Push(Lookup(name: copy, found: found)) else e {
            lib.Close(io)
            fail e
        }
    }
    if keep {
        lib.Detach()
    } else {
        lib.Close(io)
    }
    return r
}

/// Whether `name` loads under `search`: none when it does, else the error.
export Loads(io: Io, name: str, search: dl.Search): ?dl.Error {
    lib := dl.Open(io, name, search) else e { return some(e) }
    lib.Close(io)
    return none
}

// The function at `p`, called with no arguments.
CallNone(p: *u8): u32 {
    var w := u64(raw.Addr(p))
    var r: u32 = 0
    unsafe "the caller named a function that takes nothing and returns a u32, and its library is open" {
        f := raw.Read(raw.PtrCast[u64, fn(): u32](raw.OfMut(mut w)))
        r = f()
    }
    return r
}

// The function at `p`, called with `text` NUL-terminated.
CallText(p: *u8, text: str): BadText!i32 {
    var buf: [1024]u8 = [0; 1024]
    bytes := text.AsBytes()
    if bytes.Len() >= buf.Len() {
        fail BadText
    }
    var i := 0
    for b in bytes {
        if b == 0 {
            fail BadText
        }
        buf[i] = b
        i += 1
    }
    var w := u64(raw.Addr(p))
    var r: i32 = 0
    unsafe "the caller named a function that takes a NUL-terminated string and returns an i32, buf holds one, and its library is open" {
        f := raw.Read(raw.PtrCast[u64, fn(*u8): i32](raw.OfMut(mut w)))
        r = f(raw.OfMut(mut buf[0]))
    }
    return r
}

/// Calls the function `lib` exports as `symbol`, as `shape` (with `text`
/// for `TextToI32`). The caller vouches for the shape: a function of
/// another shape is undefined behaviour.
export Call(lib: &dl.Library, symbol: str, shape: Shape, text: str): NoSymbol|BadText!i64 {
    p := lib.Symbol(symbol) else { fail NoSymbol }
    return match shape {
        .NoneToU32 { i64(CallNone(p)) }
        .TextToI32 { i64(try CallText(p, text)) }
    }
}

/// Whether a loader error says this system loads nothing at run time.
export IsUnsupported(e: dl.Error): bool {
    return match e {
        dl.Unsupported { true }
        _ { false }
    }
}
