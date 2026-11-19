# std.encoding

`std.encoding` turns bytes into text and text into bytes. It covers base64, hex, percent-encoding, PEM and DER, and the text encodings of the WHATWG Encoding Standard (UTF-8, UTF-16 and the legacy single-byte encodings). Its submodule `std.encoding.cjk` adds the Chinese, Japanese and Korean multi-byte encodings. Reach for it to carry bytes inside text (keys, tokens, URLs, certificates) or to read text that is not UTF-8. For URLs as a whole, use [`std.url`](url.md); for Unicode operations on text that is already UTF-8, [`std.text`](text.md).

Read the [library guide](README.md) first.

```mk
// verdict: parse
import std.encoding
import std.encoding.cjk
```

## Contents

- [The main pieces](#the-main-pieces)
- [Base64 and hex](#base64-and-hex)
- [Percent-encoding](#percent-encoding)
- [Text encodings](#text-encodings)
- [std.encoding.cjk](#stdencodingcjk)
- [PEM and DER](#pem-and-der)
- [Your own multi-byte encoding](#your-own-multi-byte-encoding)
- [Errors](#errors)
- [Memory and cost](#memory-and-cost)
- [Examples](#examples): [base64 and hex](#encode-bytes-as-text), [percent-encoding](#escape-for-a-url), [legacy text](#decode-legacy-text), [a stream in chunks](#decode-a-stream-in-chunks), [DER and PEM](#build-and-read-der)
- [API](#api)

## The main pieces

| Task | Use |
|---|---|
| Base64 | `Base64Encode`, `Base64Decode`, with an `Alphabet`: `Base64`, `Base64Url`, `Base64Raw`, `Base64UrlRaw`, `Base64Forgiving` |
| Base64 into a buffer you own | `Base64Len`, `Base64EncodeTo`, `Base64DecodeTo` |
| Hex | `HexEncode`, `HexDecode` |
| Escape bytes for a URL or a form | `PercentEncode` with a `PercentSet`, `PercentDecode`, `PercentSet.Has` |
| Find an encoding by its label | `Lookup` (`cjk.Lookup` for every label) |
| Decode text | `Encoding.Decode`, `Encoding.DecodeStrict`, `Encoding.DecodeKeepBom` |
| Decode a stream in chunks | `Encoding.Decoder`, then `Decoder.Push` |
| Encode text | `Encoding.Encode` |
| Name an encoding | `Utf8`, `Utf16Le`, `Utf16Be`, `Windows1252` and the other constants; `Encoding.Name` |
| Read PEM | `ParsePem`, which returns `PemBlock`s |
| Write PEM | `FormatPem` |
| Read DER | `DerRead`, `DerValue.Children`, `DerIter.Next`, `DerValue.Integer`, `DerValue.BitString`, `DerValue.Boolean` |
| Write DER | `DerWriter.New`, `Begin`, `End`, `Primitive`, `Integer`, `Bytes` |
| Add a multi-byte encoding | `Codec` and `Encoding.Of` (provisional) |

No function here takes an `Io`. Every decoder accepts any bytes: malformed input is an error or U+FFFD, never a panic, and a decoder's memory and stack are bounded by its input.

## Base64 and hex

An `Alphabet` names the characters and the padding rule. `Base64` and `Base64Url` are RFC 4648's two alphabets, padded with `=`; `Base64Raw` and `Base64UrlRaw` are the same without padding.

Decoding is strict, so each byte string has exactly one accepted text:

- every character is in the alphabet: no whitespace and no line breaks;
- padding is exactly what the length needs, and none for the unpadded alphabets;
- the unused bits of the last character are zero.

`Base64Forgiving` encodes as `Base64` and decodes by the WHATWG Infra Standard's forgiving-base64 decode: it skips ASCII whitespace, lets padding be left out and ignores the unused bits. No other alphabet decodes this way.

`HexEncode` writes lower case; `HexDecode` takes either case.

## Percent-encoding

A `PercentSet` is one of the WHATWG URL Standard's percent-encode sets: `C0Control`, `Fragment`, `Query`, `SpecialQuery`, `Path`, `Userinfo`, `Component` and `FormUrlencoded`. `PercentEncode` writes each byte of the set as `%` and two upper-case hex digits, and copies the rest.

Every byte below 0x20 or above 0x7E is in every set, so the output is always ASCII. Each set adds bytes to another:

| Set | Escapes |
|---|---|
| `C0Control` | the bytes below 0x20, 0x7F and above |
| `Fragment` | `C0Control`, and space `"` `<` `>` `` ` `` |
| `Query` | `C0Control`, and space `"` `#` `<` `>` |
| `SpecialQuery` | `Query`, and `'` |
| `Path` | `Query`, and `?` `^` `` ` `` `{` `}` |
| `Userinfo` | `Path`, and `/` `:` `;` `=` `@` `[` `\` `]` `\|` |
| `Component` | `Userinfo`, and `$` `%` `&` `+` `,` |
| `FormUrlencoded` | `Component`, and `!` `'` `(` `)` `~` |

- `C0Control` leaves a space as it is. `FormUrlencoded` writes it as `+`, as a form body does, and the other sets as `%20`.
- `PercentSet.Has(b)` is whether the set writes `b` escaped.

`PercentDecode` replaces each `%` followed by two hex digits with the byte they spell. Any other `%` stays as it is, and `+` stays `+`. It returns bytes, since the result need not be UTF-8.

## Text encodings

An `Encoding` is one encoding of the Encoding Standard. Each is a named constant (`Utf8`, `Utf16Le`, `Utf16Be`, `Windows1252`, `Koi8R`, `Iso88592` and the rest), so a program links only the tables of the encodings it names. A constant carries the standard's name in PascalCase with no separator, since a top-level name has no `_` (E0207): `Iso88598I` is ISO-8859-8-I and `XMacCyrillic` is x-mac-cyrillic. `Lookup` turns a label into an `Encoding` as a browser does: it ignores surrounding ASCII whitespace and ASCII case, and takes every label of the standard, so `"latin1"` is `Windows1252`. Because `Lookup` can return any of this module's encodings, calling it links all of their tables.

| Function | On malformed input | Byte order mark |
|---|---|---|
| `Decode` | each malformed unit becomes U+FFFD | its own encoding's mark is removed |
| `DecodeStrict` | fails with `BadInput` at the unit | its own encoding's mark is removed |
| `DecodeKeepBom` | as `Decode` | kept, as U+FEFF |
| `Decoder.Push` | as `Decode` | removed at the stream's start |

- "Its own encoding's mark" is UTF-8's for `Utf8`, UTF-16LE's for `Utf16Le` and UTF-16BE's for `Utf16Be`. A mark of another encoding is text, and nothing switches encoding.
- `Utf8.DecodeKeepBom` is the standard's "UTF-8 decode without BOM", which a form body asks for.
- `Encode` writes no byte order mark. It writes UTF-16LE and UTF-16BE too, although the standard itself has no UTF-16 encoder.
- `Encode` fails with `BadInput` at the first character the encoding has no bytes for.
- `Replacement` is the standard's replacement encoding, which the labels of ISO-2022-KR, ISO-2022-CN and HZ name. It decodes any non-empty input to one U+FFFD, and `Encode` fails with `Unsupported`.
- `XUserDefined` maps bytes 0x80 to 0xFF to U+F780 to U+F7FF. Neither it nor `Replacement` needs a table.

A `Decoder` decodes one stream that arrives in pieces. What its `Push` calls return, joined, is what `Decode` returns for the joined input, however the input is split. Pass `last` with the final chunk: a unit the stream ends inside of becomes U+FFFD, and the decoder starts a new stream.

## std.encoding.cjk

`std.encoding.cjk` holds the seven multi-byte encodings of the standard: `Gb18030`, `Gbk`, `Big5`, `EucJp`, `Iso2022Jp`, `ShiftJis` and `EucKr`. Their index tables are large, so they live in their own module and are linked only into a program that names one of them. They are ordinary `Encoding` values: `Decode`, `DecodeStrict`, `Decoder` and `Encode` work on them as on the others, except that no byte order mark is removed.

`encoding.Lookup` knows their labels but fails with `Unsupported` for them, so a program that calls only `encoding.Lookup` links none of their tables. `cjk.Lookup` takes every label of the standard, these included, and links every table of both modules.

The decoders and encoders follow the standard step by step:

- A byte the standard puts back is read again after the U+FFFD of the unit before it.
- A stream that ends inside a unit ends in one U+FFFD, then the bytes the standard puts back. In ISO-2022-JP, `ESC $` at the end decodes to U+FFFD and `$`, and `ESC $ B ESC $` to two U+FFFD.
- An `Iso2022Jp` `Decoder` keeps its mode between chunks, and `Encode` ends its output back in ASCII.
- `EucJp` decodes JIS X 0212 but does not encode it.
- `Gbk` is `Gb18030`'s decoder with an encoder limited to GBK: U+20AC is the byte 0x80, and a character with no two-byte form has no bytes. `Gb18030` encodes every scalar but U+E5E5.
- `Encode` fails with `BadInput` at the first character the encoding has no bytes for. In ISO-2022-JP that includes the control characters SO, SI and ESC.

## PEM and DER

`std.crypto` and `std.x509` read keys and certificates with these.

`ParsePem` returns every block of a text, each a `PemBlock` with its `label` (`"CERTIFICATE"`, `"PRIVATE KEY"`) and its `der` bytes. It skips the text around the blocks and allows whitespace inside a block's base64. The block itself is strict:

- the label must be RFC 7468's;
- the end line must name the same label, with nothing after its dashes;
- the base64 must be canonical and padded;
- a header line, a line with a `:` such as `Proc-Type: 4,ENCRYPTED`, is refused, so an encrypted key never reads as a plain one.

`FormatPem` writes one block in lines of 64 characters.

`DerRead` reads one element and returns it with the bytes after it. A `DerValue` is a view of its input. `tag` is the tag number alone and `class` its class (0 universal, 1 application, 2 context-specific, 3 private). `constructed` says whether it holds elements, and `content` is the content bytes.

- The reader takes definite lengths only, refuses any encoding that is not minimal, and bounds nesting at 32 levels and every length at its input.
- `Integer` returns the minimal two's complement content, most significant byte first. `BitString` returns the bytes after the unused-bit count, with the unused bits checked to be zero. `Boolean` takes only 0x00 and 0xFF.
- A `BadInput` offset counts from the start of the slice that reader was given: `DerRead`'s data, or the parent's content for `DerIter.Next`. `Integer`, `BitString` and `Boolean` fail at 0.

`DerWriter` builds an encoding in an arena. Its tag word holds the class in bits 31 and 30, the constructed bit in bit 29, and the tag number in bits 28 to 0. So `0x10` is SEQUENCE and `0x80000000` is the context-specific `[0]`. `Begin` opens a constructed element and sets the constructed bit; `End` closes it and writes its length.

## Your own multi-byte encoding

`Encoding.Of` makes an `Encoding` of a `Codec`, two functions in the standard's model of an encoding. `std.encoding.cjk` is built this way; you need it only for an encoding the standard does not have. `Codec` and `Encoding.Of` are provisional: their limits may change in a minor release.

- `decode(b, i, last, state)` reads the unit at byte offset `i` in a decoder state. It returns the unit's length, up to two scalars (`0xFFFFFFFF` for none) and the next state. A length of 0 asks for more input.
- `encode(c, state, out)` writes the bytes of one scalar, at most 8. It returns how many and the next state, or -1 when the encoding has no bytes for the scalar. After the last scalar it is called once more with `0xFFFFFFFF`, to return the stream to its first state.
- std calls each function twice for a unit, once to size the result and once to write it, so both calls must answer the same.
- A value that is not a scalar becomes U+FFFD, and a length is cut to the input. So no `Codec` can make a `str` that is not UTF-8. A `Codec` whose two calls answer differently can make a call panic on a bounds check, or return text padded with NUL bytes, but never write outside the result.
- The limits: one `u8` of state, units of at most four bytes and two scalars, eight bytes for a scalar.

## Errors

Every function fails with members of one error set, `encoding.Error`:

| Member | Meaning |
|---|---|
| `BadInput(offset)` | The input is not in the format. `offset` is the first byte that cannot be there, or the input's length when it ends too early |
| `UnknownLabel` | `Lookup` was given a label the Encoding Standard does not have |
| `Unsupported` | An encoding this module does not build (the CJK set, from `encoding.Lookup`), or an operation an encoding lacks (`Replacement.Encode`) |
| `TooLarge` | Output that does not fit where it must go (`Base64DecodeTo`'s `dst`), DER nested past 32 levels, or a DER element of 4 GiB or more |

Functions that allocate add `OutOfMemory`. `FormatPem` fails with the `Writer`'s `WriteError`; if it fails or its task is cancelled part way, the writer may hold part of the block. `encoding.Error` is open, so a `match` on it outside std keeps a `_` arm.

## Memory and cost

Functions that return new bytes or text take an `a: &Arena` and put the result there, sized exactly; the doc text of each says how much. `Base64Decode` and `HexDecode` allocate the result before they check the input, so a failed decode still uses its arena space. `Base64EncodeTo` and `Base64DecodeTo` write into a buffer you pass and allocate nothing. `DerRead`, `DerIter` and the `DerValue` readers return views of their input and allocate nothing. `ParsePem`'s labels are views of the text, so the text must outlive the list.

Every function runs in time linear in its input, except `Encode` to a single-byte or CJK encoding. That searches the encoding's index for each character: O(n log m) for an index of m entries.

## Examples

Each program below runs as written with `mk run`.

### Encode bytes as text

```mk
// verdict: ok
import std.encoding

export Main(sys: &System): encoding.Error|OutOfMemory|WriteError!void {
    data := b"Mk\x00\xff" // four bytes, two of them not text
    b64 := try encoding.Base64Encode(sys.arena, data, encoding.Base64)
    url := try encoding.Base64Encode(sys.arena, data, encoding.Base64UrlRaw)
    hex := try encoding.HexEncode(sys.arena, data)
    try sys.Print("base64 {b64}\nurl    {url}\nhex    {hex}\n", (b64, url, hex))

    back := try encoding.HexDecode(sys.arena, hex)
    try sys.Print("hex round trip: {n} bytes\n", (n: back.Len()))

    // A decode error names the first byte that cannot be there.
    _ = encoding.Base64Decode(sys.arena, "TWs*", encoding.Base64) else e {
        match e {
            encoding.BadInput(offset) { try sys.Print("bad base64 at byte {offset}\n", (offset)) }
            _ { try sys.Print("{e}\n", (e)) }
        }
        return
    }
}
```

```text
$ mk run b64.mk
base64 TWsA/w==
url    TWsA_w
hex    4d6b00ff
hex round trip: 4 bytes
bad base64 at byte 3
```

- `Base64UrlRaw` uses `-` and `_` and drops the padding, for tokens inside URLs.
- `Base64Decode` refuses whitespace too. Use `Base64Forgiving` for text a person typed or wrapped.

### Escape for a URL

```mk
// verdict: ok
import std.encoding

export Main(sys: &System): OutOfMemory|Utf8Error|WriteError!void {
    s := "a b/c?é".AsBytes()
    part := try encoding.PercentEncode(sys.arena, s, encoding.PercentSet.Component) // one URL component
    form := try encoding.PercentEncode(sys.arena, s, encoding.PercentSet.FormUrlencoded) // a form body
    try sys.Print("{part}\n{form}\n", (part, form))

    // Decoding leaves a % without two hex digits as it is, and + as +.
    back := try encoding.PercentDecode(sys.arena, "50%25+off%zz")
    try sys.Print("{n} bytes: {text}\n", (n: back.Len(), text: try str.From(back)))
}
```

```text
$ mk run pct.mk
a%20b%2Fc%3F%C3%A9
a+b%2Fc%3F%C3%A9
10 bytes: 50%+off%zz
```

- `é` is two bytes of UTF-8, so it becomes two escapes.
- `PercentDecode` returns bytes; `str.From` checks that they are UTF-8.

### Decode legacy text

```mk
// verdict: ok
import std.encoding

export Main(sys: &System): encoding.Error|OutOfMemory|WriteError!void {
    // Labels are matched as the Encoding Standard does: "latin1" names windows-1252.
    enc := try encoding.Lookup(" Latin1 ")
    bytes := b"caf\xe9 \x80 10" // é and € in windows-1252
    text := try enc.Decode(sys.arena, bytes)
    try sys.Print("{name}: {text}\n", (name: enc.Name(), text))

    // Bytes that are not UTF-8 become U+FFFD in Decode, an error in DecodeStrict.
    bad := b"ok\xff"
    loose := try encoding.Utf8.Decode(sys.arena, bad)
    try sys.Print("lossy: {loose}\n", (loose))
    _ = encoding.Utf8.DecodeStrict(sys.arena, bad) else e {
        try sys.Print("strict: {e}\n", (e))
    }

    // Encode refuses a character the encoding has no bytes for.
    out := try enc.Encode(sys.arena, "naïve")
    try sys.Print("{n} bytes\n", (n: out.Len()))
    _ = enc.Encode(sys.arena, "日本") else e {
        try sys.Print("encode: {e}\n", (e))
    }
}
```

```text
$ mk run text.mk
windows-1252: café € 10
lossy: ok�
strict: BadInput(offset: 2)
5 bytes
encode: BadInput(offset: 0)
```

- `Lookup` names every encoding of this module, so this program links all the single-byte tables. Write `encoding.Windows1252` instead to link one.
- `Encode`'s offset is a byte offset into the UTF-8 input.

### Decode a stream in chunks

```mk
// verdict: ok
import std.encoding
import std.encoding.cjk

export Main(sys: &System): encoding.Error|OutOfMemory|WriteError!void {
    sjis := try cjk.Lookup("shift_jis")
    // "日本語" in Shift_JIS, arriving in three chunks that split characters.
    chunks := [b"\x93\xfa\x96", b"\x7b\x8c", b"\xea"]
    var d := sjis.Decoder()
    for i in 0..chunks.Len() {
        part := try d.Push(sys.arena, chunks[i], i == chunks.Len() - 1)
        try sys.Print("chunk {i}: \"{part}\"\n", (i, part))
    }

    // std.encoding's own Lookup knows the label but does not build the encoding.
    _ = encoding.Lookup("shift_jis") else e {
        try sys.Print("encoding.Lookup: {e}\n", (e))
    }
}
```

```text
$ mk run stream.mk
chunk 0: "日"
chunk 1: "本"
chunk 2: "語"
encoding.Lookup: Unsupported
```

- The decoder holds the unfinished bytes of a character between `Push` calls: at most three bytes, and ISO-2022-JP's mode.
- `cjk.ShiftJis` names the encoding directly and links only its tables.

### Build and read DER

```mk
// verdict: ok
import std.encoding

export Main(sys: &System): encoding.Error|OutOfMemory|WriteError!void {
    // SEQUENCE { INTEGER 300, [0] { BOOLEAN true } }
    var w := encoding.DerWriter.New(sys.arena)
    try w.Begin(0x10)              // SEQUENCE
    try w.Integer(b"\x01\x2c")     // 300, most significant byte first
    try w.Begin(0x80000000)        // context-specific [0]
    try w.Primitive(0x01, b"\xff") // BOOLEAN true
    try w.End()
    try w.End()
    der := try w.Bytes()

    // Write it as PEM; ParsePem reads the same text back.
    var out := sys.Stdout()
    try encoding.FormatPem(mut out, "EXAMPLE", der)
    pem := "text around a block is skipped\n-----BEGIN EXAMPLE-----\nMAkCAgEsoAMBAf8=\n-----END EXAMPLE-----\n"
    blocks := try encoding.ParsePem(sys.arena, pem)
    block := blocks[0]
    try sys.Print("{label}, {n} bytes\n", (label: block.label, n: block.der.Len()))

    // Walk the elements.
    top := try encoding.DerRead(block.der)
    var it := top.value.Children()
    for child := try it.Next() {
        try sys.Print("tag {tag} class {class} constructed {c}, {n} content bytes\n",
            (tag: child.tag, class: child.class, c: child.constructed, n: child.content.Len()))
    }
}
```

```text
$ mk run der.mk
-----BEGIN EXAMPLE-----
MAkCAgEsoAMBAf8=
-----END EXAMPLE-----
EXAMPLE, 11 bytes
tag 2 class 0 constructed false, 2 content bytes
tag 0 class 2 constructed true, 3 content bytes
```

- `Bytes` fails while an element is still open, so a missing `End` is caught.
- `Children` walks one level; call `Children` on a constructed child to go deeper.

## API

<!-- api:begin -->
Stability: Stable.

### `Alphabet`

```
export struct Alphabet
```

Alphabet is a base64 alphabet and its padding rule: Base64, Base64Url,
Base64Raw, Base64UrlRaw or Base64Forgiving.

### `BadInput`

```
export error BadInput(offset: int)
```

BadInput is input that is not in the format, found wrong at byte offset:
the first byte that cannot be there, or the input's length when it ends
too early; each function's line says which byte it names.

### `Base64`

```
export const Base64: Alphabet
```

Base64 is RFC 4648 section 4: `+` and `/`, padded with `=`.

### `Base64Decode`

```
export Base64Decode(a: &Arena, s: str, alphabet: Alphabet): Error|OutOfMemory![]u8 from a
```

- Flows: The result borrows from a.
- Errors: Error|OutOfMemory (OutOfMemory, BadInput(offset: int), TooLarge, UnknownLabel, Unsupported)

Base64Decode is the bytes s encodes under alphabet, in a. Fails with
BadInput as Base64DecodeTo does. O(n); allocates the decoded length in a
(at most 3 bytes for every 4 of s), also when the decode then fails.

### `Base64DecodeTo`

```
export Base64DecodeTo(dst: mut []u8, s: str, alphabet: Alphabet): Error!int
```

- Errors: Error (BadInput(offset: int), TooLarge, UnknownLabel, Unsupported)

Base64DecodeTo decodes s under alphabet into the front of dst and
returns the number of bytes written. Fails with TooLarge when dst is
shorter than the decoded length, which is checked first, and with
BadInput at the first byte outside the alphabet, at s.Len() when the
length is not one an encoding has (padding missing, or one character
left over), or at the last character when its unused bits are not zero;
Base64Forgiving skips whitespace and checks neither padding nor unused
bits. On an error dst's contents are unspecified. O(n); allocates
nothing.

### `Base64Encode`

```
export Base64Encode(a: &Arena, data: []u8, alphabet: Alphabet): OutOfMemory!str from a
```

- Flows: The result borrows from a.
- Errors: OutOfMemory

Base64Encode is the encoding of data under alphabet, in a. O(n);
allocates Base64Len(data.Len(), alphabet) bytes in a.

### `Base64EncodeTo`

```
export Base64EncodeTo(dst: mut []u8, data: []u8, alphabet: Alphabet): int
```

Base64EncodeTo writes the encoding of data to the front of dst and
returns its length, Base64Len(data.Len(), alphabet). O(n); allocates
nothing. A dst shorter than that panics: its length is the caller's.

### `Base64Forgiving`

```
export const Base64Forgiving: Alphabet
```

Base64Forgiving encodes as Base64 and decodes by the WHATWG Infra
Standard's forgiving-base64 decode: ASCII whitespace is skipped, padding
may be left out, and the unused bits of the last character are ignored.

### `Base64Len`

```
export Base64Len(n: int, alphabet: Alphabet): int
```

Base64Len is the length of the encoding of n bytes under alphabet:
4 * ceil(n / 3) when padded, ceil(4 * n / 3) when not. O(1); allocates
nothing. n below 0 panics, and so does an n whose encoding's length
overflows int.

### `Base64Raw`

```
export const Base64Raw: Alphabet
```

Base64Raw is Base64 with no padding.

### `Base64Url`

```
export const Base64Url: Alphabet
```

Base64Url is RFC 4648 section 5: `-` and `_`, padded with `=`.

### `Base64UrlRaw`

```
export const Base64UrlRaw: Alphabet
```

Base64UrlRaw is Base64Url with no padding.

### `Codec`

```
export struct Codec
```

Codec is the decoder and encoder of a multi-byte encoding, which
Encoding.Of turns into an Encoding. std.encoding.cjk's encodings are
made this way; a program needs its own Codec only for an encoding the
Encoding Standard does not have.

Each function must give the same result for the same arguments:
std.encoding calls it once to size the result and again to write it. A
Codec that breaks this rule can make Decode, DecodeStrict, Push and
Encode panic on a bounds check, or return text padded with NUL bytes;
it cannot make them write outside their result or return a str that is
not UTF-8.

Provisional: the limits (one u8 of state, units of at most four bytes
and two scalars, eight bytes for a scalar) may change in a minor
release.

Fields:

- `name: fn(): str`: The encoding's name, which Encoding.Name returns.
- `decode: fn([]u8, int, bool, u8): (n: int, c: u32, c2: u32, state: u8)`: One unit of input: decode(b, i, last, state) reads the unit at b[i]
  in decoder state state (0 at a stream's start) and returns its
  length n, its scalar c, a second scalar c2 that some units decode
  to (Big5's four pairs), and the state after it. c and c2 are
  0xFFFFFFFF for no scalar (an escape sequence decodes to none, and
  most units have no c2); any other value that is not a scalar makes
  the unit malformed, one U+FFFD. An n of 0 says that b ends inside
  the unit and more input may follow; it is read as one malformed
  unit when last is true or four bytes or more are left, so a unit is
  at most four bytes. n is at least 1 otherwise.
- `encode: fn(u32, u8, mut []u8): (n: int, state: u8)`: One scalar of output: encode(c, state, out) writes the bytes of c in
  encoder state state (0 at the start) to out, which holds 8, and
  returns how many and the state after; n of -1 says that the encoding
  has no bytes for c. After the last scalar it is called once with c
  0xFFFFFFFF, to write what returns the stream to state 0
  (ISO-2022-JP's escape to ASCII).

### `Decoder`

```
export struct Decoder
```

Decoder decodes one stream in chunks: the concatenation of what Push
returns is what Decode returns for the concatenation of the chunks,
however they are split. It holds at most three bytes of an unfinished
unit between calls, and a multi-byte decoder's state (ISO-2022-JP's
mode), and starts a new stream after a Push with last.

### `Decoder.Push`

```
export Decoder.Push(mut self, a: &Arena, chunk: []u8, last: bool): OutOfMemory!str from a
```

- Flows: The result borrows from a.
- Errors: OutOfMemory

Push decodes the next chunk of the stream and returns the text it
completes, as Decode does; with last, a unit the stream ends inside of
is U+FFFD, and the decoder then starts a new stream. O(n) in the chunk;
allocates the returned text's length in a.

### `DerIter`

```
export view struct DerIter
```

DerIter yields the children of a constructed DerValue in order.

### `DerIter.Next`

```
export DerIter.Next(mut self): Error!?DerValue from self.data
```

- Flows: The result borrows what self.data refers to; self is free after the call.
- Errors: Error (BadInput(offset: int), TooLarge, UnknownLabel, Unsupported)

Next is the next child, or none after the last. Fails with BadInput as
DerRead does, at an offset into the parent's content, and with
TooLarge for a child nested deeper than 32. O(1); allocates nothing.

### `DerRead`

```
export DerRead(data: []u8): Error!(value: DerValue, rest: []u8) from data
```

- Flows: The result borrows from data.
- Errors: Error (BadInput(offset: int), TooLarge, UnknownLabel, Unsupported)

DerRead reads the DER element at the front of data and returns it and
the bytes after it. Fails with BadInput at the first byte of an
identifier or length that is not DER (an indefinite or non-minimal
length, a non-minimal tag), or at data.Len() when data ends inside the
element. O(1); allocates nothing.

### `DerValue`

```
export struct DerValue
```

DerValue is one DER element, a view of its input: its tag number, class
(0 universal, 1 application, 2 context-specific, 3 private), whether it
is constructed, and its content octets.

Fields:

- `tag: u32`: The tag number.
- `class: u8`: The tag class: 0 universal, 1 application, 2 context-specific, 3
  private.
- `constructed: bool`: Whether the element is constructed.
- `content: []u8`: The content octets, a view of the input.

### `DerValue.BitString`

```
export DerValue.BitString(&self): Error![]u8 from self.content
```

- Flows: The result borrows what self.content refers to; self is free after the call.
- Errors: Error (BadInput(offset: int), TooLarge, UnknownLabel, Unsupported)

BitString is the bits of a BIT STRING, first bit in the top of the
first byte; the bits past its length are zero, as DER makes them
(X.690 11.2.1), so a caller that needs only whole bytes or bit
positions (key usage) can read them as they are. Fails with BadInput at
0 when this is not a primitive universal BIT STRING, its unused-bit
count is above 7, or the unused bits are not zero. O(1); allocates
nothing.

### `DerValue.Boolean`

```
export DerValue.Boolean(&self): Error!bool
```

- Errors: Error (BadInput(offset: int), TooLarge, UnknownLabel, Unsupported)

Boolean is the value of a BOOLEAN. Fails with BadInput at 0 when this
is not a primitive universal BOOLEAN of one byte, 0x00 or 0xFF (X.690
11.1). O(1); allocates nothing.

### `DerValue.Children`

```
export DerValue.Children(&self): DerIter from self.content
```

- Flows: The result borrows what self.content refers to; self is free after the call.

Children is an iterator over the elements of this value's content. It
fails at its first step, and every later one, when the value is
primitive, its content empty included. O(1); allocates nothing.

### `DerValue.Integer`

```
export DerValue.Integer(&self): Error![]u8 from self.content
```

- Flows: The result borrows what self.content refers to; self is free after the call.
- Errors: Error (BadInput(offset: int), TooLarge, UnknownLabel, Unsupported)

Integer is the content of an INTEGER: its two's complement value, most
significant byte first. Fails with BadInput at 0 when this is not a
primitive universal INTEGER, or its encoding is empty or not minimal
(X.690 8.3.2). O(1); allocates nothing.

### `DerWriter`

```
export view struct DerWriter
```

DerWriter builds one DER encoding in an arena. A tag word holds the
class in bits 31 and 30, the constructed bit in bit 29, and the tag
number in bits 28 to 0: 0x10 is SEQUENCE (Begin sets the constructed
bit) and 0x80000000 is the context-specific [0].

### `DerWriter.Begin`

```
export DerWriter.Begin(mut self, tag: u32): OutOfMemory!void
```

- Errors: OutOfMemory

Begin opens a constructed element with tag word tag; its content is what
is written until the matching End. A Begin past depth 32 makes End and
Bytes fail with TooLarge. O(1); allocates up to ten bytes in the arena.

### `DerWriter.Bytes`

```
export DerWriter.Bytes(self): Error![]u8
```

- Flows: The result borrows from self.
- Errors: Error (BadInput(offset: int), TooLarge, UnknownLabel, Unsupported)

Bytes ends the writer and is its encoding, in the writer's arena. Fails
with BadInput at the writer's length while an element is open or after a
Primitive with the constructed bit, and with TooLarge while a Begin past
depth 32 is open or after an End failed with TooLarge; on an error the
writer stays with the caller. O(1); allocates nothing.

### `DerWriter.End`

```
export DerWriter.End(mut self): Error!void
```

- Errors: Error (BadInput(offset: int), TooLarge, UnknownLabel, Unsupported)

End closes the element the last open Begin opened, writing its length.
Fails with BadInput at the writer's length when no element is open,
and with TooLarge for an element opened past depth 32 or with 4 GiB of
content or more. O(n) in the element's content, which moves back over
the length bytes it does not need; allocates nothing.

### `DerWriter.Integer`

```
export DerWriter.Integer(mut self, magnitude: []u8): OutOfMemory!void
```

- Errors: OutOfMemory

Integer writes the INTEGER whose value is magnitude, an unsigned number
most significant byte first, in its minimal form: leading zero bytes
dropped and one added when the top bit is set, so it reads back as
positive; an empty magnitude is zero. O(n); allocates the element's
length in the arena.

### `DerWriter.New`

```
export DerWriter.New(a: &Arena): DerWriter from a
```

- Flows: The result borrows from a.

New is an empty writer whose bytes live in a. O(1); allocates nothing
until the first write.

### `DerWriter.Primitive`

```
export DerWriter.Primitive(mut self, tag: u32, content: []u8): OutOfMemory!void
```

- Errors: OutOfMemory

Primitive writes a primitive element with tag word tag (its constructed
bit clear) and content. A tag word with the constructed bit set makes
Bytes fail with BadInput. O(n); allocates the element's length in the
arena.

### `Encoding`

```
export struct Encoding
```

Encoding is one encoding of the WHATWG Encoding Standard: Utf8,
Utf16Le, Utf16Be, Replacement, XUserDefined, or one of the legacy
single-byte encodings (Windows1252 and the rest); or a multi-byte
encoding that Encoding.Of makes from a Codec, as std.encoding.cjk's are.
A program links only the tables and the decoder and encoder code of the
encodings it names; Lookup names all of this module's.

### `Encoding.Decode`

```
export Encoding.Decode(self, a: &Arena, bytes: []u8): OutOfMemory!str from a
```

- Flows: The result borrows from a.
- Errors: OutOfMemory

Decode is bytes decoded to text, each malformed unit replaced by
U+FFFD, and a leading byte order mark of this encoding removed. O(n);
allocates the text's length in a, at most 3 bytes for each byte of input.

### `Encoding.DecodeKeepBom`

```
export Encoding.DecodeKeepBom(self, a: &Arena, bytes: []u8): OutOfMemory!str from a
```

- Flows: The result borrows from a.
- Errors: OutOfMemory

DecodeKeepBom is Decode with a leading byte order mark kept as U+FEFF,
as a TextDecoder with ignoreBOM true does; for Utf8 it is the Encoding
Standard's "UTF-8 decode without BOM", which the URL Standard asks of a
form body. O(n); allocates the text's length in a, at most 3 bytes for
each byte of input.

### `Encoding.DecodeStrict`

```
export Encoding.DecodeStrict(self, a: &Arena, bytes: []u8): BadInput|OutOfMemory!str from a
```

- Flows: The result borrows from a.
- Errors: BadInput(offset: int), OutOfMemory

DecodeStrict is bytes decoded to text, a leading byte order mark of this
encoding removed. Fails with BadInput at the first malformed unit: the
start of a UTF-8 sequence, a UTF-16 unit or a byte the encoding leaves
out, or of a unit the input ends inside of; any input to Replacement is
BadInput at 0. O(n); allocates the text's length in a, nothing on error.

### `Encoding.Decoder`

```
export Encoding.Decoder(self): Decoder
```

Decoder is a decoder of a new stream in this encoding. O(1); allocates
nothing.

### `Encoding.Encode`

```
export Encoding.Encode(self, a: &Arena, s: str): BadInput|Unsupported|OutOfMemory![]u8 from a
```

- Flows: The result borrows from a.
- Errors: BadInput(offset: int), Unsupported, OutOfMemory

Encode is s in this encoding, with no byte order mark. Fails with
BadInput at the first scalar this encoding has no bytes for (the
legacy single-byte and multi-byte encodings and x-user-defined), and
with Unsupported for Replacement, which encodes nothing. O(n), and
O(n log 128) for a single-byte encoding and O(n log m) for one whose
index has m entries (std.encoding.cjk's); allocates the result's length
in a, nothing on error.

### `Encoding.Name`

```
export Encoding.Name(self): str
```

Name is the encoding's name in the Encoding Standard: "UTF-8",
"windows-1252", "ISO-8859-2", "Shift_JIS"; a Codec's name for an
encoding Encoding.Of made. O(1); allocates nothing.

### `Encoding.Of`

```
export Encoding.Of(codec: Codec): Encoding
```

Of is the encoding that codec decodes and encodes. Decode,
DecodeStrict, Decoder, Encode and Name work on it as on the other
encodings, except that no byte order mark is removed. O(1); allocates
nothing. A const may hold one, as std.encoding.cjk's do.

Decode's bound of three bytes of text for each byte of input holds when
no unit's scalars take more than three bytes of UTF-8 for each of its
bytes, as in every encoding of the standard. Encode calls codec.encode
twice for each scalar. Provisional, as Codec is.

### `Error`

```
export open type Error = BadInput | UnknownLabel | Unsupported | TooLarge
```

Error is every failure of std.encoding.

### `FormatPem`

```
export FormatPem(w: mut Writer, label: str, der: []u8): WriteError!void
```

- Errors: WriteError

FormatPem writes der as one PEM block with label: the begin line, the
base64 of der in lines of 64 characters, and the end line, each ending
in LF. The label is written as given. O(n); allocates nothing (one
64-byte line on the stack).

### `HexDecode`

```
export HexDecode(a: &Arena, s: str): Error|OutOfMemory![]u8 from a
```

- Flows: The result borrows from a.
- Errors: Error|OutOfMemory (OutOfMemory, BadInput(offset: int), TooLarge, UnknownLabel, Unsupported)

HexDecode is the bytes s spells in hex, either case, in a. Fails with
BadInput at the first byte that is not a hex digit, else at the last
byte of an odd-length s. O(n); allocates s.Len() / 2 bytes in a, also
when the decode then fails.

### `HexEncode`

```
export HexEncode(a: &Arena, data: []u8): OutOfMemory!str from a
```

- Flows: The result borrows from a.
- Errors: OutOfMemory

HexEncode is the lower-case hex of data, two characters a byte, in a.
O(n); allocates 2 * data.Len() bytes in a.

### `Ibm866`

```
export const Ibm866: Encoding
```

Ibm866 is the WHATWG IBM866 encoding.

### `Iso885910`

```
export const Iso885910: Encoding
```

Iso885910 is the WHATWG ISO-8859-10 encoding.

### `Iso885913`

```
export const Iso885913: Encoding
```

Iso885913 is the WHATWG ISO-8859-13 encoding.

### `Iso885914`

```
export const Iso885914: Encoding
```

Iso885914 is the WHATWG ISO-8859-14 encoding.

### `Iso885915`

```
export const Iso885915: Encoding
```

Iso885915 is the WHATWG ISO-8859-15 encoding.

### `Iso885916`

```
export const Iso885916: Encoding
```

Iso885916 is the WHATWG ISO-8859-16 encoding.

### `Iso88592`

```
export const Iso88592: Encoding
```

Iso88592 is the WHATWG ISO-8859-2 encoding.

### `Iso88593`

```
export const Iso88593: Encoding
```

Iso88593 is the WHATWG ISO-8859-3 encoding.

### `Iso88594`

```
export const Iso88594: Encoding
```

Iso88594 is the WHATWG ISO-8859-4 encoding.

### `Iso88595`

```
export const Iso88595: Encoding
```

Iso88595 is the WHATWG ISO-8859-5 encoding.

### `Iso88596`

```
export const Iso88596: Encoding
```

Iso88596 is the WHATWG ISO-8859-6 encoding.

### `Iso88597`

```
export const Iso88597: Encoding
```

Iso88597 is the WHATWG ISO-8859-7 encoding.

### `Iso88598`

```
export const Iso88598: Encoding
```

Iso88598 is the WHATWG ISO-8859-8 encoding.

### `Iso88598I`

```
export const Iso88598I: Encoding
```

Iso88598I is the WHATWG ISO-8859-8-I encoding.

### `Koi8R`

```
export const Koi8R: Encoding
```

Koi8R is the WHATWG KOI8-R encoding.

### `Koi8U`

```
export const Koi8U: Encoding
```

Koi8U is the WHATWG KOI8-U encoding.

### `Lookup`

```
export Lookup(label: str): UnknownLabel|Unsupported!Encoding
```

- Errors: UnknownLabel, Unsupported

Lookup is the encoding a label names, by the Encoding Standard's get an
encoding: ASCII whitespace around the label is removed and ASCII letters
match either case. Fails with Unsupported for a label of a multi-byte
CJK encoding, which std.encoding.cjk builds and its Lookup returns, and
UnknownLabel for any other label the standard does not have. O(log n) in
the label table plus the label's length; allocates nothing; names every
encoding of this module, so it links every table of this module and none
of std.encoding.cjk's.

### `Macintosh`

```
export const Macintosh: Encoding
```

Macintosh is the WHATWG macintosh encoding.

### `ParsePem`

```
export ParsePem(a: &Arena, text: str into a): Error|OutOfMemory!List[PemBlock] from a, text
```

- Flows: The result borrows from a and text.
- Errors: Error|OutOfMemory (OutOfMemory, BadInput(offset: int), TooLarge, UnknownLabel, Unsupported)

ParsePem is every PEM block of text, in order, with the text around them
skipped. Fails with BadInput at the start of a block's begin line when
its label is not RFC 7468's, its end line is missing, or its base64 is
not canonical padded base64 (with whitespace allowed); at the start of
the end line when it names another label or has text after its closing
dashes; and at the start of a header line, a line holding `:` such as
"Proc-Type: 4,ENCRYPTED", so an encrypted key never reads as a plain
one. O(n); allocates the list, the decoded bytes, and each block's
base64 length while it is read. Each block's label is a view of text
kept in the list in a, so text is stored into a.

### `PemBlock`

```
export struct PemBlock
```

PemBlock is one PEM block: its label ("CERTIFICATE", "PRIVATE KEY") and
the DER bytes its base64 holds.

Fields:

- `label: str`: The block's label, such as "CERTIFICATE" or "PRIVATE KEY".
- `der: []u8`: The DER bytes the block's base64 holds.

### `PercentDecode`

```
export PercentDecode(a: &Arena, s: str): OutOfMemory![]u8 from a
```

- Flows: The result borrows from a.
- Errors: OutOfMemory

PercentDecode is s with every `%` that two hex digits follow replaced by
the byte they spell, in a; any other `%` stays as it is, and `+` is not
a space (the URL Standard's percent-decode). O(n); allocates the
result's length in a.

### `PercentEncode`

```
export PercentEncode(a: &Arena, s: []u8, set: PercentSet): OutOfMemory!str from a
```

- Flows: The result borrows from a.
- Errors: OutOfMemory

PercentEncode is s with every byte in set written as `%` and two
upper-case hex digits, in a. FormUrlencoded writes a space as `+`, as
the URL Standard's application/x-www-form-urlencoded serializer does.
O(n); allocates the result's length in a, at most 3 * s.Len() bytes.

### `PercentSet`

```
export enum PercentSet
```

PercentSet is one of the URL Standard's percent-encode sets. Each holds the
C0 controls and every byte above 0x7E. Query holds C0Control; Path,
Userinfo, Component and FormUrlencoded each hold the set before it in
that list, starting from Query; Fragment adds to C0Control alone and
SpecialQuery to Query alone.

Variants:

- `C0Control`
- `Fragment`
- `Query`
- `SpecialQuery`
- `Path`
- `Userinfo`
- `Component`
- `FormUrlencoded`

### `PercentSet.Has`

```
export PercentSet.Has(self, b: u8): bool
```

Has is whether PercentEncode with this set writes b as `%` and two hex
digits: the URL Standard's "in the percent-encode set". Every byte
below 0x20 or above 0x7E is in every set. FormUrlencoded holds the
space, which PercentEncode writes as `+`. O(1); allocates nothing.

### `Replacement`

```
export const Replacement: Encoding
```

Replacement is the WHATWG replacement encoding, which the labels of
ISO-2022-KR, ISO-2022-CN and the HZ encoding name: any non-empty input
decodes to one U+FFFD, and nothing encodes.

### `TooLarge`

```
export error TooLarge
```

TooLarge is output that does not fit where it must go, or input past a
stated bound (DER's depth of 32).

### `UnknownLabel`

```
export error UnknownLabel
```

UnknownLabel is a label the Encoding Standard does not have.

### `Unsupported`

```
export error Unsupported
```

Unsupported is an encoding the standard has and this module does not
build (the multi-byte CJK set, which std.encoding.cjk builds), or an
operation an encoding lacks.

### `Utf16Be`

```
export const Utf16Be: Encoding
```

Utf16Be is the WHATWG UTF-16BE encoding.

### `Utf16Le`

```
export const Utf16Le: Encoding
```

Utf16Le is the WHATWG UTF-16LE encoding.

### `Utf8`

```
export const Utf8: Encoding
```

Utf8 is the WHATWG UTF-8 encoding.

### `Windows1250`

```
export const Windows1250: Encoding
```

Windows1250 is the WHATWG windows-1250 encoding.

### `Windows1251`

```
export const Windows1251: Encoding
```

Windows1251 is the WHATWG windows-1251 encoding.

### `Windows1252`

```
export const Windows1252: Encoding
```

Windows1252 is the WHATWG windows-1252 encoding.

### `Windows1253`

```
export const Windows1253: Encoding
```

Windows1253 is the WHATWG windows-1253 encoding.

### `Windows1254`

```
export const Windows1254: Encoding
```

Windows1254 is the WHATWG windows-1254 encoding.

### `Windows1255`

```
export const Windows1255: Encoding
```

Windows1255 is the WHATWG windows-1255 encoding.

### `Windows1256`

```
export const Windows1256: Encoding
```

Windows1256 is the WHATWG windows-1256 encoding.

### `Windows1257`

```
export const Windows1257: Encoding
```

Windows1257 is the WHATWG windows-1257 encoding.

### `Windows1258`

```
export const Windows1258: Encoding
```

Windows1258 is the WHATWG windows-1258 encoding.

### `Windows874`

```
export const Windows874: Encoding
```

Windows874 is the WHATWG windows-874 encoding.

### `XMacCyrillic`

```
export const XMacCyrillic: Encoding
```

XMacCyrillic is the WHATWG x-mac-cyrillic encoding.

### `XUserDefined`

```
export const XUserDefined: Encoding
```

XUserDefined is the WHATWG x-user-defined encoding: bytes 0x80 to 0xFF
are U+F780 to U+F7FF.

### Module `std.encoding.cjk`

Stability: Stable.

#### `Big5`

```
export const Big5: encoding.Encoding
```

Big5 is the WHATWG Big5 encoding, with the Hong Kong Supplementary
Character Set.

#### `EucJp`

```
export const EucJp: encoding.Encoding
```

EucJp is the WHATWG EUC-JP encoding; JIS X 0212 decodes and does not
encode.

#### `EucKr`

```
export const EucKr: encoding.Encoding
```

EucKr is the WHATWG EUC-KR encoding (Windows code page 949).

#### `Gb18030`

```
export const Gb18030: encoding.Encoding
```

Gb18030 is the WHATWG gb18030 encoding: one, two or four bytes, every
scalar but U+E5E5 encodable.

#### `Gbk`

```
export const Gbk: encoding.Encoding
```

Gbk is the WHATWG GBK encoding: gb18030's decoder, and gb18030's encoder
limited to GBK, so U+20AC is the byte 0x80 and a code point with no
two-byte form has no bytes.

#### `Iso2022Jp`

```
export const Iso2022Jp: encoding.Encoding
```

Iso2022Jp is the WHATWG ISO-2022-JP encoding: a stateful encoding whose
Decoder keeps its mode between chunks, and whose Encode ends in ASCII.

#### `Lookup`

```
export Lookup(label: str): encoding.UnknownLabel!encoding.Encoding
```

- Errors: encoding.UnknownLabel

Lookup is the encoding a label names, by the Encoding Standard's get an
encoding, over every label of the standard: encoding.Lookup's, and the
labels of the multi-byte encodings this module builds, for which
encoding.Lookup fails with Unsupported. Fails with UnknownLabel for a
label the standard does not have. O(log n) in the label tables plus the
label's length; allocates nothing; names every encoding, so it links
every table of std.encoding and of this module.

#### `ShiftJis`

```
export const ShiftJis: encoding.Encoding
```

ShiftJis is the WHATWG Shift_JIS encoding, with Windows' EUDC range
decoding to the Private Use Area.

<!-- api:end -->
