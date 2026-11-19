# pack

An archiver with signing and sealing, in the manner of `tar` plus `age`. It packs a directory tree behind a manifest of BLAKE3 digests, signs the manifest with Ed25519, and can seal the whole archive to a recipient's X25519 key. It also reads tar archives (plain, zstd, gzip or zlib) and writes and reads zlib streams. It shows std's compression, hashing and cryptography, crash-safe file writing, and a memory budget enforced by an allocator.

## Running it

From the program's folder:

```
cd Dogfoods/pack
mk run -- list cases/data/plain.pk
```

```text
cases/data/plain.pk: 5 files, 361278 bytes, deflate, signed by 34750f98bd59fcfc, manifest a3be7ca88a1e260e
f         15  README.txt
d          -  docs/
f         49  docs/guide.txt
d          -  docs/notes/
f         17  docs/notes/todo.txt
d          -  src/
f     262893  src/big.txt
f      98304  src/noise.bin
```

The commands:

```
pack keygen alice                        # alice.key, alice.pub
pack create --sign alice.key --to bob.pub --out site.pk site/
pack create --to bob.pub --cipher aes-256-gcm --out site.pk site/
pack list --key bob.key site.pk
pack list --dups site.pk                 # files whose contents another repeats
pack verify --key bob.key --signer alice.pub site.pk
pack extract --key bob.key --signer alice.pub --dest restored site.pk
pack sum --sha512 plain.pk               # sha256sum-style lines
pack fingerprint alice.pub
pack untar release.tar.zst               # list
pack untar --dest out release.tar.zst    # extract, times and modes restored
pack zlib --out big.zz big.bin
pack unzlib --max 100000000 --out big.bin big.zz
```

`pack --help` lists the commands and `pack <command> --help` a command's options. Exit status: 0 success, 1 an archive that is damaged, forged or does not open, 2 a usage error, 3 a file system error, 4 the memory budget exhausted.

### Formats

- **Archive**: the magic `MKPK`, a version, flags, the manifest's length, the manifest, a signature block when signed (the signer's Ed25519 public key and the signature of the manifest), then one DEFLATE or gzip stream of every file's bytes in manifest order.
- **Manifest**: text. The codec, the total size, the compressed size and its BLAKE3, then one line per directory or file, sorted by path, each file with its size and BLAKE3. Paths that could leave the extraction directory are refused when the manifest is read.
- **Sealed archive**: the magic `MKPE`, the cipher (ChaCha20-Poly1305 or AES-256-GCM), an ephemeral X25519 point, the plain length and an HMAC-SHA-256 of the header, then the plain archive in 64 KiB chunks. Keys come from HKDF-SHA-256 over the shared secret; each chunk's nonce holds its index and a last-chunk flag. pack also reads version 1 archives, whose keys come from SHA-512.
- **Key files**: three lines of text: a header, the Ed25519 key and the X25519 key, in padded base64. pack also reads version 1 files, in hex. `keygen --seed` derives both keys from a seed, for tests.

## Design

- Every file pack writes is built in a temporary file beside its path (`fs.CreateTemp`, named `.pack-` and 16 hex digits), synced and renamed into place. An archive's header is written last, so a file cut short never reads as an archive.
- `--mem-limit N` runs a command on a `mem.Fixed` buffer under a `mem.Counting` allocator; `--stats` prints the counts. While packing, the compressor works in an arena made with `Arena.NewIn` over that allocator, so its memory counts too. `keygen` and `fingerprint` run with `mem.Null()`, which refuses every request.
- Entries are checked as they stream: each piece is hashed with BLAKE3 on its way out of the inflater, so no entry is held whole to verify it.
- Secrets (the shared secret, the ephemeral key, derived keys, AES key objects, a secret key file's bytes) are cleared with `crypto.Wipe` or the object's `Wipe` once used.
- `untar` reads the decoder's `Reader` as a stream, tells the framing from the first bytes, and refuses a zstd frame whose window is over `--window-limit` (8 MiB by default) before allocating for it. Links and special files are listed as skipped, never made; set-user-id, set-group-id and sticky bits are never restored.
- `list --dups` groups entries by their digests in a `Map` keyed with `HashKey.New`, from the manifest alone.

## Limits

- `create` reads files whole into memory before compressing, since the manifest records the compressed data's size and digest before the data. A sealed archive is opened whole.
- No key comes from a passphrase: a key is a file `keygen` makes.
- pack's own archives keep no symbolic links, permissions or times; `untar` restores times and permission bits from tar archives and skips links. On Windows only the owner's write bit is kept.
- Names must be UTF-8 without a newline, `\` or `:`.

## Tests

The cases in `cases/` run every command on its happy path and its errors: archives damaged or cut at each section, wrong keys and signers, unsafe paths, missing files, memory budgets that run out, and crashes and write faults under `std.sim`. Run them from the repository root with `go run ./tools/dogfood run -only pack`.

## Section 18 lines
The standard-library lines this program uses, as `doc/reference.md` lists them; `go run ./tools/dogfood cover` reads this block.

```
Hasher.Bytes(mut self, b: []u8)
// std.fs
error Os(code: i64)
error Unsupported
type Error = NotFound | Exists | Denied | IsDirectory | NotDirectory | NoSpace | InvalidPath | Utf8Error | Os | Canceled | Unsupported
fs.Rename(io: Io, src: str, dst: str): fs.Error!void
fs.Remove(io: Io, p: str): fs.Error!void
fs.ReadText(a: &Arena, io: Io, p: str): fs.Error|OutOfMemory!str from a
fs.ReadBytes(a: &Arena, io: Io, p: str): fs.Error|OutOfMemory![]u8 from a
fs.WriteAll(io: Io, p: str, data: []u8): fs.Error!void
fs.Open(io: Io, p: str): fs.Error!File
fs.Create(io: Io, p: str): fs.Error!File
File.Read(mut self, io: Io, buf: mut []u8): fs.Error!int
File.Write(mut self, io: Io, data: []u8): fs.Error!int
File.ReadAt(&self, io: Io, pos: i64, buf: mut []u8): fs.Error!int
File.WriteAt(&self, io: Io, pos: i64, data: []u8): fs.Error!int
File.WriteAll(mut self, io: Io, data: []u8): fs.Error!void
File.Sync(mut self, io: Io): fs.Error!void
File.Writer(mut self, io: Io, buf: mut []u8): Writer from self, buf
File.Reader(mut self, io: Io, buf: mut []u8): Reader from self, buf
File.Close(self, io: Io): fs.Error!void
File.Abandon(self)
fs.MakeDir(io: Io, p: str): fs.Error!void
fs.Stat(io: Io, p: str): fs.Error!Info
Dir.Open(io: Io, p: str): fs.Error!Dir
Dir.Next(mut self, a: &Arena, io: Io): fs.Error|OutOfMemory!?Entry from a
Dir.Close(self)
fs.CreateTemp(a: &Arena, io: Io, dir: str, prefix: str): fs.Error|OutOfMemory!(file: File, path: str) from a
fs.SetModified(io: Io, p: str, t: time.Time): fs.Error!void
fs.SetPermissions(io: Io, p: str, perm: Permissions): fs.Error!void
struct Permissions {
struct Entry {
struct Info {
enum Kind {
// std.mem
Counting.New(inner: &Alloc): Counting
Counting.Alloc(&self): &Alloc from self
Counting.Bytes(&self): int
Counting.Live(&self): int
Counting.Peak(&self): int
mem.Null(): &Alloc
// std.hash
hash.Sha256(data: []u8): [32]u8
hash.Sha512(data: []u8): [64]u8
hash.Blake3(data: []u8): [32]u8
Sha256State.New(): Sha256State
Sha256State.Bytes(mut self, data: []u8)
Sha256State.Sum(self): [32]u8
Sha512State.New(): Sha512State
Sha512State.Bytes(mut self, data: []u8)
Sha512State.Sum(self): [64]u8
Blake3State.New(): Blake3State
Blake3State.Bytes(mut self, data: []u8)
Blake3State.Sum(self): [32]u8
// std.crypto
error BadTag
error BadLength
crypto.Hmac256(key: []u8, data: []u8): [32]u8
crypto.X25519(secret: &[32]u8, point: &[32]u8): [32]u8
crypto.Random(io: Io, buf: mut []u8): Canceled!void
crypto.Equal(a: []u8, b: []u8): bool
ChaCha20Poly1305.Seal(key: &[32]u8, nonce: &[12]u8, aad: []u8, plain: []u8, out: mut []u8): crypto.Error!void
ChaCha20Poly1305.Open(key: &[32]u8, nonce: &[12]u8, aad: []u8, sealed: []u8, out: mut []u8): crypto.Error!void
Ed25519.Keypair(io: Io): Canceled![64]u8
Ed25519.FromSeed(seed: &[32]u8): [64]u8
Ed25519.Sign(secret: &[64]u8, msg: []u8): [64]u8
Ed25519.Verify(public: &[32]u8, msg: []u8, sig: &[64]u8): bool
crypto.HkdfExtract(salt: []u8, ikm: []u8, h: HashKind): Prk
crypto.HkdfExpand(prk: &Prk, info: []u8, out: mut []u8): crypto.Error!void
Prk.Wipe(mut self)
AesGcm.New(key: []u8): crypto.Error!AesGcm
AesGcm.Seal(&self, nonce: &[12]u8, aad: []u8, plain: []u8, out: mut []u8): crypto.Error!int
AesGcm.Open(&self, nonce: &[12]u8, aad: []u8, sealed: []u8, out: mut []u8): crypto.Error!int
AesGcm.Wipe(mut self)
crypto.Wipe(buf: mut []u8)
enum HashKind {
// std.compress
*
// std.encoding
encoding.Base64Encode(a: &Arena, data: []u8, alphabet: Alphabet): OutOfMemory!str from a
encoding.Base64EncodeTo(dst: mut []u8, data: []u8, alphabet: Alphabet): int
encoding.Base64DecodeTo(dst: mut []u8, s: str, alphabet: Alphabet): encoding.Error!int
encoding.Base64Len(n: int, alphabet: Alphabet): int
// std.cli
*
```
