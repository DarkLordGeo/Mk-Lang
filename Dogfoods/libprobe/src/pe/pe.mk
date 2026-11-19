// Why: the exports of a Windows DLL can be listed on any system, from the
// file's bytes alone.
// What: Table, Export, Describe and the errors NotPe, Truncated, NoExports
// and BadAddress.
// How: reads the export table of a PE file (DLL or EXE): names, ordinals and
// forwarders. Every offset is checked against the file before it is read.

/// Why a file is not a PE image libprobe can read.
export enum Why {
    NoDosHeader
    NoPeSignature
    BadOptionalHeader
    BadSection
}

/// The file is not a PE image, for the reason given.
export error NotPe(why: Why)

/// A structure runs past the end of the file at byte `offset`.
export error Truncated(offset: int)

/// The image has no export table.
export error NoExports

/// An address in the export table lies in no section of the file.
export error BadAddress(rva: int)

/// What the reader can fail with.
export type Error = NotPe | Truncated | NoExports | BadAddress | OutOfMemory

/// One exported function. `name` is "" for one exported by ordinal only,
/// and `forward` is the `LIBRARY.Name` it forwards to, or "".
export struct Export {
    export ordinal: int
    export name: str
    export forward: str
}

/// The export table of an image.
export struct Table {
    /// The library's own name, as its export directory records it.
    export dll: str
    /// The ordinal of the first function.
    export base: int
    /// Every function, by ordinal.
    export exports: List[Export]
    /// How many functions have a name.
    export named: int
}

// One section: where its addresses start and end, and where its bytes are.
struct Section {
    rva: int
    size: int
    raw: int
    raw_size: int
}

Why.Text(self): str {
    return match self {
        .NoDosHeader { "no MZ header" }
        .NoPeSignature { "no PE signature" }
        .BadOptionalHeader { "unknown optional header" }
        .BadSection { "a section lies outside the file" }
    }
}

/// A sentence for `e`, for messages.
export Describe(a: &Arena, e: Error): OutOfMemory!str from a {
    return match e {
        NotPe(why) { try a.Fmt("not a PE image ({w})", (w: why.Text())) }
        Truncated(offset) { try a.Fmt("truncated at byte {offset}", (offset)) }
        NoExports { "no export table" }
        BadAddress(rva) { try a.Fmt("export address {rva:x} lies in no section", (rva)) }
        OutOfMemory { "out of memory" }
    }
}

// The little-endian 16-bit value at `at`.
U16(b: []u8, at: int): Truncated!int {
    if at < 0 || at + 2 > b.Len() {
        fail Truncated(offset: at)
    }
    return int(b[at]) | int(b[at + 1]) << 8
}

// The little-endian 32-bit value at `at`.
U32(b: []u8, at: int): Truncated!int {
    if at < 0 || at + 4 > b.Len() {
        fail Truncated(offset: at)
    }
    return int(b[at]) | int(b[at + 1]) << 8 | int(b[at + 2]) << 16 | int(b[at + 3]) << 24
}

// The file offset of address `rva`, through the section that holds it.
Offset(sections: []Section, rva: int): BadAddress!int {
    for s in sections {
        if rva >= s.rva && rva < s.rva + s.size && rva - s.rva < s.raw_size {
            return s.raw + (rva - s.rva)
        }
    }
    fail BadAddress(rva: rva)
}

// The NUL-terminated ASCII string at file offset `at`, at most 512 bytes.
CString(a: &Arena, b: []u8, at: int): Truncated|OutOfMemory!str from a {
    var end := at
    for end < b.Len() && end - at < 512 {
        if b[end] == 0 {
            text := str.From(b[at..end]) else { fail Truncated(offset: at) }
            return try a.Fmt("{text}", (text))
        }
        end += 1
    }
    fail Truncated(offset: end)
}

// The sections of the image whose headers start at `at`.
Sections(a: &Arena, b: []u8, at: int, count: int): Error!List[Section] from a {
    var out := List[Section].New(a)
    var i := 0
    for i < count {
        h := at + i * 40
        s := Section(rva: try U32(b, h + 12), size: try U32(b, h + 8), raw: try U32(b, h + 20), raw_size: try U32(b, h + 16))
        if s.raw_size > 0 && (s.raw > b.Len() || s.raw_size > b.Len() - s.raw) {
            fail NotPe(why: Why.BadSection)
        }
        // A section's address range is its virtual size, or its file size
        // when a linker left the virtual size 0.
        var t := s
        if t.size == 0 {
            t.size = t.raw_size
        }
        try out.Push(t)
        i += 1
    }
    return out
}

/// Reads the export table of the PE image `b`. O(n) in the number of
/// exports; the table and its names are allocated in `a`.
export Read(a: &Arena, b: []u8): Error!Table from a {
    if b.Len() < 64 || b[0] != b'M' || b[1] != b'Z' {
        fail NotPe(why: Why.NoDosHeader)
    }
    pe := try U32(b, 0x3C)
    if pe + 24 > b.Len() || b[pe] != b'P' || b[pe + 1] != b'E' || b[pe + 2] != 0 || b[pe + 3] != 0 {
        fail NotPe(why: Why.NoPeSignature)
    }
    count := try U16(b, pe + 6)
    opt_size := try U16(b, pe + 20)
    opt := pe + 24
    magic := try U16(b, opt)
    // PE32 keeps its data directories at 96 bytes into the optional header,
    // PE32+ at 112; the count of directories sits just before them.
    dirs := match magic {
        0x10B { opt + 96 }
        0x20B { opt + 112 }
        _ { fail NotPe(why: Why.BadOptionalHeader) }
    }
    if try U32(b, dirs - 4) < 1 || dirs + 8 > opt + opt_size {
        fail NoExports
    }
    exp_rva := try U32(b, dirs)
    exp_size := try U32(b, dirs + 4)
    if exp_rva == 0 || exp_size == 0 {
        fail NoExports
    }
    sections := try Sections(a, b, opt + opt_size, count)
    d := try Offset(sections.Items(), exp_rva)
    dll := try CString(a, b, try Offset(sections.Items(), try U32(b, d + 12)))
    base := try U32(b, d + 16)
    nfun := try U32(b, d + 20)
    nnames := try U32(b, d + 24)
    if nfun > 65536 || nnames > nfun {
        fail Truncated(offset: d + 20)
    }
    funcs := try Offset(sections.Items(), try U32(b, d + 28))
    var t := Table(dll: dll, base: base, exports: List[Export].New(a), named: nnames)
    var i := 0
    for i < nfun {
        rva := try U32(b, funcs + 4 * i)
        // An address inside the export directory is a forwarder: the name
        // of the function in another library, not code.
        var forward := ""
        if rva >= exp_rva && rva < exp_rva + exp_size {
            forward = try CString(a, b, try Offset(sections.Items(), rva))
        }
        try t.exports.Push(Export(ordinal: base + i, name: "", forward: forward))
        i += 1
    }
    if nnames > 0 {
        names := try Offset(sections.Items(), try U32(b, d + 32))
        ords := try Offset(sections.Items(), try U32(b, d + 36))
        var j := 0
        for j < nnames {
            index := try U16(b, ords + 2 * j)
            if index >= nfun {
                fail Truncated(offset: ords + 2 * j)
            }
            name := try CString(a, b, try Offset(sections.Items(), try U32(b, names + 4 * j)))
            t.exports[index].name = name
            j += 1
        }
    }
    return t
}
