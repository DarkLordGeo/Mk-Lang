// Why: each command has its own flags, and the help text must match them.
//
// What: one options struct per command, from CreateOptions to UnzlibOptions.
//
// How: each struct is parsed by `gen cli.Args`, which also writes its help.

import std.cli

/// pack create [options] --out FILE DIR
struct CreateOptions {
    /// Write the archive to this file
    export out: str
    /// Sign the manifest with this secret key file
    export sign: ?str
    /// Seal the archive to this public key file
    export to: ?str
    /// Seal with chacha20-poly1305 (the default) or aes-256-gcm
    export cipher: ?str
    /// Compress with gzip framing instead of raw DEFLATE
    export gzip: bool
    /// Fail rather than hold more than this many bytes of file data
    export mem_limit: ?int
    /// Print memory use on stderr
    export stats: bool
    /// The directory to pack
    export dirs: []str
}

/// pack list [options] ARCHIVE
struct ListOptions {
    /// Open a sealed archive with this secret key file
    export key: ?str
    /// Print each file's BLAKE3 digest too
    export long: bool
    /// Print only the files whose contents another file repeats, in sets
    export dups: bool
    /// Fail rather than hold more than this many bytes of archive data
    export mem_limit: ?int
    /// Print memory use on stderr
    export stats: bool
    /// The archive to list
    export archives: []str
}

/// pack verify [options] ARCHIVE
struct VerifyOptions {
    /// Open a sealed archive with this secret key file
    export key: ?str
    /// Require a signature by this public key file
    export signer: ?str
    /// Fail rather than hold more than this many bytes of archive data
    export mem_limit: ?int
    /// Print memory use on stderr
    export stats: bool
    /// The archive to verify
    export archives: []str
}

/// pack extract [options] ARCHIVE
struct ExtractOptions {
    /// Open a sealed archive with this secret key file
    export key: ?str
    /// Require a signature by this public key file
    export signer: ?str
    /// Extract into this directory, made if missing (default: .)
    export dest: ?str
    /// Fail rather than hold more than this many bytes of archive data
    export mem_limit: ?int
    /// Print memory use on stderr
    export stats: bool
    /// The archive to extract
    export archives: []str
}

/// pack sum [options] ARCHIVE
struct SumOptions {
    /// Open a sealed archive with this secret key file
    export key: ?str
    /// Print SHA-512 sums instead of SHA-256
    export sha512: bool
    /// Fail rather than hold more than this many bytes of archive data
    export mem_limit: ?int
    /// Print memory use on stderr
    export stats: bool
    /// The archive to sum
    export archives: []str
}

/// pack keygen [options] NAME
struct KeygenOptions {
    /// Derive the keys from this 64-digit hex seed instead of the OS
    export seed: ?str
    /// Write NAME.key and NAME.pub
    export names: []str
}

/// pack fingerprint KEYFILE
struct FingerprintOptions {
    /// The public or secret key file
    export keys: []str
}

/// pack untar [options] ARCHIVE
struct UntarOptions {
    /// Extract into this directory, made if missing, instead of listing
    export dest: ?str
    /// Refuse a zstd window over this many bytes (default 8 MiB)
    export window_limit: ?int
    /// The tar archive: plain, or compressed with zstd, gzip or zlib
    export archives: []str
}

/// pack zlib [options] --out FILE INPUT...
struct ZlibOptions {
    /// Write the zlib stream to this file
    export out: str
    /// Compression level, 0 (store) to 9; giving one streams the file
    export level: ?int
    /// End a block with a sync flush every this many input bytes
    export sync_every: ?int
    /// The files to compress, one after another into one stream
    export inputs: []str
}

/// pack unzlib [options] --out FILE INPUT
struct UnzlibOptions {
    /// Write the decompressed data to this file
    export out: str
    /// Fail rather than write more than this many bytes (default 1 GiB)
    export max: ?int
    /// The zlib stream to decompress
    export inputs: []str
}

gen cli.Args(CreateOptions)
gen cli.Args(ListOptions)
gen cli.Args(VerifyOptions)
gen cli.Args(ExtractOptions)
gen cli.Args(SumOptions)
gen cli.Args(KeygenOptions)
gen cli.Args(FingerprintOptions)
gen cli.Args(UntarOptions)
gen cli.Args(ZlibOptions)
gen cli.Args(UnzlibOptions)
