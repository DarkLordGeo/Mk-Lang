// Why: Verify needs a set of trusted roots, either the program's own or the
// system's, read the way Go reads them so both agree.
//
// What: Pool with Pool.New, and SystemRoots, the system's pool, with the
// file readers AddFile, AddDir, ReadCapped and ListDir.
//
// How: on Linux the system's pool is the bundle Go's crypto/x509 reads
// (root_linux.go, root_unix.go); on Windows it lists nothing and stands for
// the platform's own chain check (platform.mk). Files are read through
// std.fs, so a simulated Io reads its own disk. Details follow.

// SSL_CERT_FILE alone when it is set, else the first of the usual bundle
// files that exists; then every file of SSL_CERT_DIR's directories
// (colon-separated) when it is set, else of the usual directories, in name
// order (Go's os.ReadDir), but a link to a file of its own directory (Go's
// readUniqueDirectoryEntries). A certificate that does not parse is
// skipped, as Go's AppendCertsFromPEM skips it, and one whose DER an earlier
// one has is dropped, as Go's CertPool keeps one of each. Each file is read
// through one handle and given up past 64 MiB, its size first, then its
// bytes. A simulated Io's environment is not the process's.

import std.core
import std.fs
import std.rt

/// A set of trusted certificates: the roots Verify may end a path at. A
/// plain view of a slice of certificates, Send and Share. Only New and
/// SystemRoots make one. A pool from New is checked by std.x509 alone; the
/// system's pool on Windows hands the chain to the platform.
export struct Pool {
    /// The certificates, as New was given them.
    export certs: []Certificate
    // system is the Windows pool that hands the chain to the platform.
    system: bool
}

/// A pool of certs, as a view of them. O(1); allocates nothing.
export Pool.New(certs: []Certificate): Pool from certs {
    return Pool(certs: certs, system: false)
}

// BundleFiles are the usual bundle paths, in Go's order.
const BundleFiles: [6]str = [
    "/etc/ssl/certs/ca-certificates.crt",
    "/etc/pki/tls/certs/ca-bundle.crt",
    "/etc/ssl/ca-bundle.pem",
    "/etc/pki/tls/cacert.pem",
    "/etc/pki/ca-trust/extracted/pem/tls-ca-bundle.pem",
    "/etc/ssl/cert.pem",
]

// BundleDirs are the usual certificate directories, in Go's order.
const BundleDirs: [3]str = ["/etc/ssl/certs", "/etc/pki/tls/certs", "/system/etc/security/cacerts"]

// MaxBundle bounds one file read for the system's pool (64 MiB).
const MaxBundle: i64 = 67_108_864

/// Loads the system's trusted roots into a pool. Each call reads the store
/// again; SystemPool loads it once per process. O(bundle bytes + d log d
/// name comparisons for a directory of d entries + n log n DER comparisons
/// for n certificates); allocates the certificates' DER and the pool in a,
/// and one directory's names and one file's bytes at a time in sub-arenas
/// freed after.
///
/// On Linux it reads the file SSL_CERT_FILE names, else the first of the
/// usual bundle files that exists, then the files of the directories in
/// SSL_CERT_DIR (colon-separated), else of the usual directories, each
/// directory's files in name order. It skips what does not parse, a file
/// past 64 MiB and a link to a file of the same directory, and keeps each
/// certificate once, first seen first. A missing file or directory, and one
/// that cannot be listed to its end, is skipped, so a system with none
/// gives an empty pool. Files are read through io, so a simulated Io reads
/// its own disk and environment. macOS reads the same way: its usual
/// bundle is /etc/ssl/cert.pem, the roots the system's LibreSSL and curl
/// use; the Keychain is not read.
///
/// On Windows the pool lists nothing and stands for the system: Verify
/// hands the chain to the platform's check.
///
/// Fails with Unsupported on a target with neither, with Canceled (any
/// std.fs wait; nothing is consumed, so a second call reads the same), and
/// with OutOfMemory.
export SystemRoots(a: &Arena, io: Io): Error!Pool from a {
    if Target.os == core.Os.Windows {
        return Pool(certs: List[Certificate].New(a).Done()[..], system: true)
    }
    if Target.os != core.Os.Linux && Target.os != core.Os.Macos {
        fail Unsupported
    }
    var out := List[Certificate].New(a)
    file := try EnvOf(a, io, "SSL_CERT_FILE")
    if file.Len() > 0 {
        _ = try AddFile(a, io, file, mut out)
    } else {
        for f in BundleFiles {
            if try AddFile(a, io, f, mut out) {
                break
            }
        }
    }
    dirs := try EnvOf(a, io, "SSL_CERT_DIR")
    if dirs.Len() > 0 {
        for d in dirs.Split(":") {
            try AddDir(a, io, d, mut out)
        }
    } else {
        for d in BundleDirs {
            try AddDir(a, io, d, mut out)
        }
    }
    all := out.Done()
    k := try Unique(a, all)
    return Pool(certs: all[0..k], system: false)
}

// Unique moves to the front of certs, in their order, every certificate
// whose DER no earlier one has, as Go's CertPool keeps one of each (the
// bundle and the directory list most roots twice or more), and is how many
// it kept. In place, so a list in a holds no loan of a. O(n log n)
// DER comparisons; allocates an index and the flags in a.
Unique(a: &Arena, certs: mut []Certificate): OutOfMemory!int {
    n := certs.Len()
    idx := try a.Slice[int](n)
    for i in 0..n {
        idx[i] = i
    }
    idx.Sort(fn(p: &int, q: &int): bool {
        c := ByteCmp(certs[*p].data, certs[*q].data)
        return c < 0 || (c == 0 && *p < *q)
    })
    keep := try a.Slice[bool](n)
    for r in 0..n {
        keep[idx[r]] = r == 0 || ByteCmp(certs[idx[r - 1]].data, certs[idx[r]].data) != 0
    }
    var w := 0
    for i in 0..n {
        if keep[i] {
            certs[w] = certs[i]
            w += 1
        }
    }
    return w
}

// AddFile adds the certificates of the PEM file p to out, and is whether
// the file could be read. The file is read in a sub-arena freed on
// return; the certificates' DER is copied into a.
AddFile(a: &Arena into out, io: Io, p: str, out: mut List[Certificate]): Canceled|OutOfMemory!bool {
    var scratch := try a.Sub()
    defer scratch.Free()
    got := try ReadCapped(&scratch, io, p, MaxBundle)
    raw := got else {
        return false
    }
    text := try Ascii(&scratch, raw)
    PemCertificates(a, text, false, out) else e {
        match e {
            OutOfMemory {
                fail OutOfMemory
            }
            _ {}
        }
    }
    return true
}

// ReadCapped is the bytes of file p, read through one handle into a, or
// none when it cannot be opened or read, or holds more than limit bytes:
// its size is checked first, and the read stops once it passes limit, so
// a file whose size reads 0 (a FIFO, a device, a procfs file) or that
// grows while read costs at most about 2 x limit bytes. Reads may be
// partial; each is one std.fs call. Fails with Canceled (from the open,
// the size or a read) and OutOfMemory. O(file); allocates the buffer in
// a, doubling from the size plus one.
ReadCapped(a: &Arena, io: Io, p: str, limit: i64): Canceled|OutOfMemory!?[]u8 from a {
    var f := fs.Open(io, p) else e {
        _ = try Skip(e)
        return none
    }
    defer f.Abandon()
    size := f.Size(io) else e {
        _ = try Skip(e)
        return none
    }
    if size < 0 || size > limit {
        return none
    }
    var room := size + 1
    if room < 4096 {
        room = 4096
    }
    if room > limit + 1 {
        room = limit + 1
    }
    var buf := try a.Slice[u8](int(room))
    var got := 0
    for {
        if got == buf.Len() {
            if i64(got) > limit {
                return none
            }
            var more := i64(got) * 2
            if more > limit + 1 {
                more = limit + 1
            }
            grown := try a.Slice[u8](int(more))
            (mut grown[0..got]).CopyFrom(buf[0..got])
            buf = grown
        }
        k := f.Read(io, mut buf[got..]) else e {
            _ = try Skip(e)
            return none
        }
        if k == 0 {
            break
        }
        got += k
    }
    if i64(got) > limit {
        return none
    }
    return some(buf[0..got])
}

// Skip is false for a file or directory that cannot be read, and fails
// with Canceled when the scope was cancelled.
Skip(e: fs.Error): Canceled!bool {
    match e {
        Canceled {
            fail Canceled
        }
        _ {
            return false
        }
    }
}

// Ascii is raw as text: itself when it is UTF-8, else a copy in a with
// every byte past 0x7F written '?', since a bundle's comments may be in
// another encoding and PEM is ASCII (Go reads the bytes).
Ascii(a: &Arena, raw: []u8): OutOfMemory!str from a, raw {
    text := str.From(raw) else {
        out := try a.Slice[u8](raw.Len())
        for i in 0..raw.Len() {
            out[i] = if raw[i] < 0x80 { raw[i] } else { b'?' }
        }
        return str.From(out[..]).Must()
    }
    return text
}

// AddDir adds the certificates of every file of directory d to out, the
// files in bytewise order of their names, as Go's os.ReadDir sorts them,
// so the pool's order depends on the names, not on the file system's
// listing order (ext4's hash seed, tmpfs's creation order). A directory
// that cannot be listed to its end adds nothing, as Go skips it. The
// names and paths live in a sub-arena freed on return.
AddDir(a: &Arena into out, io: Io, d: str, out: mut List[Certificate]): Canceled|OutOfMemory!void {
    if d.Len() == 0 {
        return
    }
    var scratch := try a.Sub()
    defer scratch.Free()
    got := try ListDir(&scratch, io, d)
    names := got else {
        return
    }
    for name in names {
        p := try scratch.Fmt("{d}/{name}", (d, name))
        if try SameDirLink(&scratch, io, p) {
            continue
        }
        _ = try AddFile(a, io, p, out)
    }
}

// ListDir is the names of directory d's entries that are not directories
// and are UTF-8, sorted bytewise, or none when d cannot be opened or
// listed to its end. Fails with Canceled (the open or any step) and
// OutOfMemory. O(entries log entries) name compares (pdqsort); allocates
// the names and their slice in a.
ListDir(a: &Arena, io: Io, d: str): Canceled|OutOfMemory!?[]str from a {
    var dir := fs.Dir.Open(io, d) else e {
        _ = try Skip(e)
        return none
    }
    defer dir.Close()
    var list := List[str].New(a)
    for {
        got := dir.Next(a, io) else e {
            match e {
                OutOfMemory {
                    fail OutOfMemory
                }
                Canceled {
                    fail Canceled
                }
                _ {
                    return none
                }
            }
        }
        entry := got else {
            break
        }
        if entry.is_dir {
            continue
        }
        name := entry.name.Str() else {
            continue
        }
        try list.Push(name)
    }
    names := list.Done()
    names.Sort(fn(p: &str, q: &str): bool {
        return ByteCmp(p.AsBytes(), q.AsBytes()) < 0
    })
    return some(names[..])
}

// SameDirLink is whether p is a symbolic link to a file of its own
// directory (a target with no '/'), as Go's readUniqueDirectoryEntries
// skips it: /etc/ssl/certs holds a hash-named link to each root beside
// the root. Fails with Canceled; anything else (not a link, no links
// on this file system) is false. One std.fs call.
SameDirLink(a: &Arena, io: Io, p: str): Canceled|OutOfMemory!bool {
    var scratch := try a.Sub()
    defer scratch.Free()
    target := fs.ReadLink(&scratch, io, p) else e {
        match e {
            OutOfMemory {
                fail OutOfMemory
            }
            Canceled {
                fail Canceled
            }
            _ {
                return false
            }
        }
    }
    return target.Len() > 0 && !HasByte(target.AsBytes(), b'/')
}

// EnvOf is the process's environment variable name copied into a, "" when
// it is unset or io is simulated, as std.time reads TZ.
EnvOf(a: &Arena, io: Io, name: str): OutOfMemory!str from a {
    if (!core.IsNative(io) && (core.IoFlags(io) & SimBits) != 0) {
        return ""
    }
    sys := core.NewSystem(rt.Args(), rt.NativeIo(), a, rt.ProcessHeap(), rt.Proc())
    v := sys.Env(name) else {
        return ""
    }
    return try a.Dup(v)
}

// SimBits are the Io flags' token bits (1 to 31) and slot bits (48 to 63),
// set only in a simulated Io (std.time's load.mk reads them the same way).
const SimBits: uint = 0xFFFF_0000_FFFF_FFFE
