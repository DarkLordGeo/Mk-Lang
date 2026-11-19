# bigcalc

An arbitrary-precision RPN calculator with a scripting mode, and a toolbox for certificates, keys, signatures, authenticated encryption, tags and digests. It shows `std.big`, `std.crypto`, `std.x509` and `std.encoding`, arena-per-line evaluation, and memory and time limits.

## Running it

From the program's folder:

```
cd Dogfoods/bigcalc
mk run -- calc "123456789012345678901234567890 987654321098765432109876543210 * p" "-170141183460469231731687303715884105728 3 / p" "-7 2 / p -7 2 % p -7 2 mod p 7 -2 mod p" "2 200 ^ 3 divmod f clear" "99999999999999999999999 -1 + 1 - neg abs"
```

```text
121932631137021795226185032733622923332237463801111263526900
-56713727820156410577229101238628035242
-3
-1
1
1
121932631137021795226185032733622923332237463801111263526900
-56713727820156410577229101238628035242
-3
-1
1
1
535646014752996758513987364113720867507400997927597611767125
1
99999999999999999999997
```

`calc` evaluates each argument as one line and prints the top of the stack at the end; `f` prints the whole stack. The other commands:

```
bigcalc run script.rpn                   # a script, or - for standard input
bigcalc cert show chain.pem
bigcalc cert verify --roots root.pem --host example.test chain.pem
bigcalc cert der leaf.der                # the DER tree
bigcalc key show key.pem
bigcalc sign --key key.pem --format fixed message.txt
bigcalc verify --key cert.pem message.txt signature.bin
bigcalc enc seal --key-from text:secret --salt hex:00 --info demo plain.txt sealed.bin
bigcalc mac --key text:key --hash sha384 file.txt
bigcalc hash --alg all file.txt
bigcalc encode base64 file.bin
bigcalc --help
```

### The calculator

Words are separated by whitespace; `#` at the start of a word comments out the rest of the line. A number is decimal, `0x`, `0o` or `0b` with an optional sign, `B#digits` for a base B from 2 to 36, `bytes:HEX` or `b64:BASE64` (big-endian, non-negative).

| Words | Meaning |
|---|---|
| `+ - *` | Arithmetic |
| `/ %` `divmod` | Truncated quotient and remainder, as Go's `Quo` and `Rem`; `divmod` pushes both |
| `mod` | Euclidean remainder, never negative |
| `^` `modpow` `inv` `gcd` `sqrt` | Power, modular power (a negative exponent inverts first), modular inverse, greatest common divisor, floor square root |
| `<< >>` `bit` `bitlen` `sign` `neg` `abs` `cmp` `eq` `lt` `min` `max` | Shifts (`>>` rounds toward negative infinity), bit tests in two's complement, comparisons |
| `fact` `fib` | n! and F(n) |
| `dup drop swap over rot clear depth sort uniq` | The stack; `sort` orders it, `uniq` pushes the number of distinct values |
| `p . f stack` | Print the top, pop and print, print the whole stack |
| `i64 f64 bytes b64 words der` | The top as an i64, the nearest f64 with its bits, big-endian bytes, base64, 64-bit words, a DER INTEGER of its magnitude |
| `hex dec oct bin base` | The output base |
| `=name $name vars` | Store into and recall a variable, list them |
| `rep` | Pops n and runs the rest of the line n times |

A line that fails leaves the stack as it was before the line. Limits: `--max-digits` (longest literal, default 100,000), `--max-bits` (largest result, default 4,000,000), `--mem-limit BYTES` (the stack and variables live in a fixed buffer of that size), `--max-time MS` (`run` stops after that long).

### Certificates, keys and signatures

- `cert show FILE...`: subject, issuer, serial, validity, names, key, basic constraints and fingerprints of every certificate in PEM or DER files.
- `cert verify --roots FILE [--host NAME] [--at TIME] CHAIN`: verifies a chain (leaf first) against a pool built from the roots files, at `--at` (RFC 3339) or now.
- `cert der FILE`: the DER tree of a file.
- `key show FILE`, `key public FILE`: a key's kind and public half; the public key as PEM.
- `sign --key FILE | --key-scalar SPEC [--hash H] [--format der|fixed] [--out hex|base64] MESSAGE`: P-256 (RFC 6979 nonces) or Ed25519.
- `verify --key FILE [--hash H] [--pss] [--format der|fixed] [--sig-encoding raw|hex|base64] MESSAGE SIGNATURE`: RSA PKCS#1 v1.5 and PSS, ECDSA P-256 and P-384, Ed25519; the key may be a certificate.

### Encryption, tags and digests

- `enc seal|open`: AES-GCM with a key from HKDF-SHA256 (`--key-from SPEC` with `--salt` and `--info`, `--prk SPEC`, or a P-256 ECDH secret from `--ecdh-key` and `--ecdh-peer`). A sealed file is the nonce, the ciphertext and the tag; `--armor` writes base64. With `--ecdh-peer` alone, `seal` makes a P-256 key for that file only and writes its public half first, so only the peer opens it, with `--ecdh-key` alone (as ECIES does).
- `mac --key SPEC [--hash H] [--expect HEX] FILE`: HMAC-SHA256, -384 or -512.
- `hash [--alg sha1|sha256|sha384|sha512|all] [--base64] FILE...`: each file hashed in a task of its own.
- `encode` and `decode` `base64|base64url|hex FILE`.

A SPEC is `hex:...`, `b64:...`, `text:...` or `file:PATH`. Exit codes: 0 success, 1 error, 2 usage, 3 memory limit, 4 time limit, 5 a signature, tag or certificate that does not verify.

## Design

- Each line runs in an arena that is reset after it, against a copy of the kept stack; the line's result is kept only when the whole line succeeds.
- The stack and variables outlive the arena as 64-bit words in heap lists (`Int.Words`, `big.FromWords`, `Int.Clone`).
- Every result whose size grows with its inputs is checked against `--max-bits` before it is computed, so a huge power or shift fails at once with `ResultTooLarge` instead of exhausting memory.
- `--max-time` is a scope deadline around `run`; `hash` digests each file in a task of its own through the streaming SHA states, 64 KiB at a time.
- Files bigcalc writes go through a temporary file and a rename, so a crash leaves the old file or the new one.

## Limits

- RSA keys are verified only, and only between 2048 and 8192 bits with an odd exponent from 3 to 2^32 - 1 (std.crypto's bounds); P-384 keys verify only.
- `std.x509` is Experimental, and `mk check` says so.
- A key loaded from a certificate file (`verify --key cert.pem`) is read from the certificate's DER, since `Certificate.PublicKey`'s result borrows the parsed certificate; `cert show` uses `Certificate.PublicKey`.

## Tests

The cases in `cases/` cover every word and command, error paths, and memory and time limits; `sim_save` runs crash and fault tests of file saving under `std.sim`. The calculator's expected outputs come from Go's `math/big`, and the cryptography's from Go's crypto packages and RFC 6979 (the oracles are kept in `oracle/`). Run them from the repository root with `go run ./tools/dogfood run -only bigcalc`.

## Section 18 lines
The standard-library lines this program uses, as `doc/reference.md` lists them; `go run ./tools/dogfood cover` reads this block.

```
HeapList[T].Extend(mut self, a: &Alloc, xs: []T): OutOfMemory!void *xs into self
// std.big. Int and Error below are big.Int and big.Error
error TooLarge
type Error = ParseError | TooLarge | OutOfMemory
big.FromI64(a: &Arena, x: i64): OutOfMemory!Int from a
big.FromBytes(a: &Arena, b: []u8): OutOfMemory!Int from a
big.FromWords(words: []u64, negative: bool): Int from words
big.Parse(a: &Arena, s: str, base: int, max_digits: int): big.Error!Int from a
Int.Clone(&self, a: &Arena): OutOfMemory!Int from a
Int.Words(&self): []u64 from self
Int.Add(&self, a: &Arena, y: &Int): OutOfMemory!Int from a
Int.Sub(&self, a: &Arena, y: &Int): OutOfMemory!Int from a
Int.Mul(&self, a: &Arena, y: &Int): OutOfMemory!Int from a
Int.DivMod(&self, a: &Arena, y: &Int): OutOfMemory!(q: Int, r: Int) from a
Int.Mod(&self, a: &Arena, m: &Int): OutOfMemory!Int from a
Int.ModPow(&self, a: &Arena, e: &Int, m: &Int): OutOfMemory!Int from a
Int.ModInverse(&self, a: &Arena, m: &Int): OutOfMemory!?Int from a
Int.Gcd(&self, a: &Arena, y: &Int): OutOfMemory!Int from a
Int.Sqrt(&self, a: &Arena): OutOfMemory!Int from a
Int.Shl(&self, a: &Arena, n: int): OutOfMemory!Int from a
Int.Shr(&self, a: &Arena, n: int): OutOfMemory!Int from a
Int.Neg(&self, a: &Arena): OutOfMemory!Int from a
Int.Cmp(&self, y: &Int): int
Int.Sign(&self): int
Int.BitLen(&self): int
Int.Bit(&self, i: int): bool
Int.ToI64(&self): ?i64
Int.ToF64(&self): f64
Int.Bytes(&self, a: &Arena): OutOfMemory![]u8 from a
Int.Text(&self, a: &Arena, base: int): OutOfMemory!str from a
Int.Format(&self, w: mut Writer): WriteError!void

// std.hash
hash.Sha1(data: []u8): [20]u8
hash.Sha384(data: []u8): [48]u8
Sha1State.New(): Sha1State
Sha1State.Bytes(mut self, data: []u8)
Sha1State.Sum(self): [20]u8
Sha384State.New(): Sha384State
Sha384State.Bytes(mut self, data: []u8)
Sha384State.Sum(self): [48]u8

// std.crypto. Error below is crypto.Error
error BadTag
error BadLength
error BadKey
error Encrypted
error BadEncoding
type Error = BadTag | BadLength | BadKey | Encrypted | BadEncoding
crypto.Hmac384(key: []u8, data: []u8): [48]u8
crypto.Hmac512(key: []u8, data: []u8): [64]u8
crypto.HkdfExtract(salt: []u8, ikm: []u8, h: HashKind): Prk
crypto.HkdfExpand(prk: &Prk, info: []u8, out: mut []u8): crypto.Error!void
Prk.FromBytes(h: HashKind, key: []u8): crypto.Error!Prk
Prk.Wipe(mut self)
AesGcm.New(key: []u8): crypto.Error!AesGcm
AesGcm.Seal(&self, nonce: &[12]u8, aad: []u8, plain: []u8, out: mut []u8): crypto.Error!int
AesGcm.Open(&self, nonce: &[12]u8, aad: []u8, sealed: []u8, out: mut []u8): crypto.Error!int
AesGcm.Wipe(mut self)
EcSecret.Generate(io: Io, curve: Curve): Canceled|crypto.Error!EcSecret
EcSecret.FromBytes(curve: Curve, b: []u8): crypto.Error!EcSecret
EcSecret.Public(&self, out: mut []u8): crypto.Error!int
EcSecret.Wipe(mut self)
crypto.EcdhShared(curve: Curve, secret: &EcSecret, peer: []u8, out: mut []u8): crypto.Error!void
crypto.EcdsaSign(secret: &EcSecret, digest: []u8, format: SigFormat, out: mut []u8): crypto.Error!int
crypto.EcdsaVerify(public: []u8, curve: Curve, digest: []u8, format: SigFormat, sig: []u8): bool
RsaPublic.New(modulus: []u8, exponent: u64): crypto.Error!RsaPublic from modulus
crypto.RsaVerifyPkcs1(public: &RsaPublic, h: HashKind, digest: []u8, sig: []u8): bool
crypto.RsaVerifyPss(public: &RsaPublic, h: HashKind, digest: []u8, sig: []u8): bool
crypto.ParsePublicKey(der: []u8): crypto.Error!PublicKey from der
crypto.ParsePrivateKey(der: []u8): crypto.Error!PrivateKey
crypto.Wipe(buf: mut []u8)
enum Curve {
enum HashKind {
enum SigFormat {
enum PublicKey {
enum PrivateKey {

// std.encoding. Encoding, Decoder and Error below are encoding.Encoding, encoding.Decoder and encoding.Error
error BadInput(offset: int)
error TooLarge
type Error = BadInput | UnknownLabel | Unsupported | TooLarge
encoding.Base64Encode(a: &Arena, data: []u8, alphabet: Alphabet): OutOfMemory!str from a
encoding.Base64Decode(a: &Arena, s: str, alphabet: Alphabet): encoding.Error|OutOfMemory![]u8 from a
encoding.Base64EncodeTo(dst: mut []u8, data: []u8, alphabet: Alphabet): int
encoding.Base64DecodeTo(dst: mut []u8, s: str, alphabet: Alphabet): encoding.Error!int
encoding.Base64Len(n: int, alphabet: Alphabet): int
encoding.HexEncode(a: &Arena, data: []u8): OutOfMemory!str from a
encoding.HexDecode(a: &Arena, s: str): encoding.Error|OutOfMemory![]u8 from a
encoding.ParsePem(a: &Arena, text: str into a): encoding.Error|OutOfMemory!List[PemBlock] from a, text
encoding.FormatPem(w: mut Writer, label: str, der: []u8): WriteError!void
encoding.DerRead(data: []u8): encoding.Error!(value: DerValue, rest: []u8) from data
DerValue.Children(&self): DerIter from self.content
DerIter.Next(mut self): encoding.Error!?DerValue from self.data
DerValue.Integer(&self): encoding.Error![]u8 from self.content
DerValue.BitString(&self): encoding.Error![]u8 from self.content
DerValue.Boolean(&self): encoding.Error!bool
DerWriter.New(a: &Arena): DerWriter from a
DerWriter.Begin(mut self, tag: u32): OutOfMemory!void
DerWriter.End(mut self): encoding.Error!void
DerWriter.Primitive(mut self, tag: u32, content: []u8): OutOfMemory!void
DerWriter.Integer(mut self, magnitude: []u8): OutOfMemory!void
DerWriter.Bytes(self): encoding.Error![]u8
struct PemBlock {
struct DerValue {

// std.x509, Experimental. Certificate, Pool and Error below are x509.Certificate, x509.Pool and x509.Error
error Malformed(offset: int)
error UnknownAuthority
error Expired
error NameMismatch
error BadSignature
error BadUsage
error TooComplex
error Unsupported
type Error = Malformed | UnknownAuthority | Expired | NameMismatch | BadSignature | BadUsage | TooComplex | Unsupported | Canceled | OutOfMemory
x509.Parse(a: &Arena, der: []u8): x509.Error!Certificate from a, der
x509.ParsePem(a: &Arena, pem: str): x509.Error!List[Certificate] from a
x509.Verify(a: &Arena, io: Io, chain: []Certificate, roots: &Pool, now: time.Time, host: str): x509.Error!void
Pool.New(certs: []Certificate): Pool from certs
Certificate.Subject(&self, a: &Arena): OutOfMemory!str from a
Certificate.Issuer(&self, a: &Arena): OutOfMemory!str from a
Certificate.Names(&self, a: &Arena): OutOfMemory!List[str] from a, self *self into a
Certificate.NotBefore(&self): time.Time
Certificate.NotAfter(&self): time.Time
Certificate.Der(&self): []u8 from self
Certificate.PublicKey(&self): crypto.Error!crypto.PublicKey from self
struct Certificate {
struct Pool {
```
