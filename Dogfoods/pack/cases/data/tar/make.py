# Why: pack's untar, gunzip, zlib and zstd readers need inputs that are
# whole, cut short and damaged at known bytes, made the same way each time.
#
# What: rebuilds the tar fixtures of pack's untar cases: site.tar and its
# gzip, zlib and zstd forms, cut and corrupted copies, a skippable zstd
# frame, pax and GNU long names, and incompressible noise for the zlib
# streams. Run from this directory: py -3 make.py
#
# How: tarfile writes USTAR, pax and GNU archives from fixed entries, and
# each damaged copy flips named bytes of a good one. It needs Windows' bsdtar
# (C:\Windows\System32\tar.exe, libarchive with libzstd) for the zstd
# frames; Python's own modules write the rest.
import gzip, io, os, subprocess, tarfile, zlib

def entry(name, kind, mode, mtime, data=b"", link=""):
    t = tarfile.TarInfo(name)
    t.type, t.mode, t.mtime = kind, mode, mtime
    t.uname = t.gname = "pack"
    t.uid = t.gid = 1000
    t.size = len(data)
    t.linkname = link
    return t, data

def tar(entries):
    out = io.BytesIO()
    with tarfile.open(fileobj=out, mode="w", format=tarfile.USTAR_FORMAT) as tf:
        for t, data in entries:
            tf.addfile(t, io.BytesIO(data) if data else None)
    return out.getvalue()

def write(name, data):
    with open(name, "wb") as f:
        f.write(data)

# a text larger than a 64 KiB buffer, so reads cross block and buffer edges
lines = [("line %05d of the long file, the quick brown fox\n" % i).encode() for i in range(2000)]
long = b"".join(lines)

good = [
    entry("site", tarfile.DIRTYPE, 0o750, 1614834367),
    entry("site/docs", tarfile.DIRTYPE, 0o755, 1614834000),
    entry("site/docs/readme.txt", tarfile.REGTYPE, 0o640, 1600000000, b"read me first\n"),
    entry("site/docs/long.txt", tarfile.REGTYPE, 0o644, 1600000123, long),
    entry("site/run.sh", tarfile.REGTYPE, 0o755, 1500000000, b"#!/bin/sh\necho hello\n"),
    entry("site/empty", tarfile.REGTYPE, 0o600, 1400000000),
    entry("site/latest", tarfile.SYMTYPE, 0o777, 1400000001, link="run.sh"),
]
plain = tar(good)
write("site.tar", plain)
write("site.tar.gz", gzip.compress(plain, mtime=0))
write("site.tar.zz", zlib.compress(plain, 9))
write("cut.tar.zz", zlib.compress(plain, 9)[:-7])
write("cut.tar", plain[:2048 + 300])

subprocess.run(["C:/Windows/System32/tar.exe", "--zstd", "-cf", "site.tar.zst", "@site.tar"], check=True)
zst = open("site.tar.zst", "rb").read()

# a skippable frame (RFC 8878 section 3.1.2) before the archive's frame
skippable = bytes([0x5a, 0x2a, 0x4d, 0x18]) + (6).to_bytes(4, "little") + b"pack!\n"
write("skippable.tar.zst", skippable + zst)
# cut inside the frame
write("cut.tar.zst", zst[: len(zst) // 2])
# one byte of the first block's compressed data changed
bad = bytearray(zst)
bad[len(zst) // 2] ^= 0x55
write("corrupt.tar.zst", bytes(bad))
# the content checksum, the frame's last four bytes, changed
bad = bytearray(zst)
bad[-1] ^= 0x01
write("checksum.tar.zst", bytes(bad))
# the first block's header (bytes 6 to 8, after the magic and the two
# descriptor bytes) given the reserved block type 3
bad = bytearray(zst)
bad[6] |= 0x06
write("block.tar.zst", bytes(bad))
# a frame that declares a window of 2^(10+17) bytes, 128 MiB: the frame
# header descriptor (byte 4) must have no single-segment bit, so byte 5 is
# the window descriptor
assert zst[4] & 0x20 == 0, "the frame is single-segment; patch its content size instead"
bad = bytearray(zst)
bad[5] = 17 << 3
write("window.tar.zst", bytes(bad))

# a header whose checksum is wrong
bad = bytearray(plain)
bad[0] ^= 0x01
write("checksum.tar", bytes(bad))
# a path that leaves the destination
write("escape.tar", tar([entry("../escape.txt", tarfile.REGTYPE, 0o644, 1500000000, b"out\n")]))

# a file that does not compress, so its zlib stream is over the 64 KiB that
# pack zlib and unzlib handle in one call, and is read through the streams
state = 12345
noise = bytearray()
for _ in range(100000):
    state = (state * 1103515245 + 12345) & 0x7fffffff
    noise.append(state >> 16 & 0xff)
noise = bytes(noise)
write("noise.bin", noise)
stream = zlib.compress(noise, 6)
write("noise.zz", stream)
write("cut-noise.zz", stream[: len(stream) - 1000])
# bytes after the stream's end: its own Adler-32 holds, the file's last
# four bytes do not
write("trailing.zz", stream + b"tail")
# a deflate block in the middle damaged
bad = bytearray(stream)
bad[len(stream) // 2] ^= 0xff
write("corrupt-noise.zz", bytes(bad))

# names longer than the 100 bytes of a header's name field: in a pax
# extended header (with a global header first, which a reader skips), and
# in GNU's long name header
deep = "site/" + "/".join(["directory-%02d" % i for i in range(8)]) + "/a-file-whose-path-is-long.txt"
def longtar(fmt, extra):
    out = io.BytesIO()
    with tarfile.open(fileobj=out, mode="w", format=fmt, **extra) as tf:
        t, data = entry(deep, tarfile.REGTYPE, 0o644, 1600000000, b"deep\n")
        tf.addfile(t, io.BytesIO(data))
        t, data = entry("site/short.txt", tarfile.REGTYPE, 0o600, 1600000001, b"short\n")
        tf.addfile(t, io.BytesIO(data))
    return out.getvalue()
pax = longtar(tarfile.PAX_FORMAT, {"pax_headers": {"comment": "made by make.py"}})
write("pax.tar", pax)
write("gnu.tar", longtar(tarfile.GNU_FORMAT, {}))
# the length of the extended header's path record made longer than the
# header's data: the record's first digit, at the start of its line
i = pax.index(b" path=")
start = max(pax.rfind(b"\n", 0, i), pax.rfind(b"\0", 0, i)) + 1
bad = bytearray(pax)
bad[start] = ord("9")
write("badpax.tar", bytes(bad))
