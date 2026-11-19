# std.url

`std.url` parses, resolves, changes and prints URLs by the WHATWG URL Standard, the rules browsers implement, and reads and writes `application/x-www-form-urlencoded` query strings. It also converts international domain names between their Unicode and ASCII forms (UTS 46). Reach for it whenever a URL comes from a user, a page or a peer; `std.http` and `std.websocket` take a parsed `Url`. For percent-encoding a byte string on its own, use `std.encoding`.

Read the [library guide](README.md) first.

```mk
// verdict: parse
import std.url
```

## Contents

- [The main pieces](#the-main-pieces)
- [The Url value](#the-url-value)
- [Changing a URL](#changing-a-url)
- [Hosts and international names](#hosts-and-international-names)
- [Errors](#errors)
- [Memory and cost](#memory-and-cost)
- [Examples](#examples): [read the parts of a URL](#read-the-parts-of-a-url), [resolve links and change parts](#resolve-links-and-change-parts), [read and write a query](#read-and-write-a-query), [refused URLs and international names](#refused-urls-and-international-names)
- [API](#api)

## The main pieces

| Task | Use |
|---|---|
| Parse a URL | `Parse(a, input, none)` |
| Resolve a link against a page's URL | `Parse(a, link, some(&base))` |
| Read its parts | `Url.Scheme`, `Username`, `Password`, `Host`, `Hostname`, `Port`, `Path`, `Query`, `Fragment`, `Origin` |
| The exact text | `Url.Href` |
| Print it for a log, without the password | `{u}` with `(u: &u)` |
| Change one part | `Url.Set(a, part, value)` with a `Part` |
| Split a query into pairs | `ParseQuery` |
| Build a query from pairs | `FormatQuery` |
| Convert a domain name | `DomainToAscii`, `DomainToUnicode` |

## The Url value

`Parse` returns a `Url`: the URL's serialization, its text in the URL Standard's canonical form, and the offsets of its parts. Every accessor but `Origin` returns a view of that text in O(1) and allocates nothing; `Origin(a)` builds its text in `a`.

- `Href()` is the exact serialization, password included. `{u}` prints the same text without the password, so a URL can go to a log.
- The parser normalizes by the standard: the scheme and an ASCII host are lower-cased, and `.` and `..` path segments are resolved. A port equal to the scheme's default is dropped, so `Port()` is `none` for `https://example.com:443/`.
- `Query()` and `Fragment()` leave out the `?` and `#`, and are `""` when the URL has none. `Username()`, `Password()`, `Query()` and `Fragment()` stay percent-encoded.
- `Origin(a)` is `"scheme://host"` (with `":port"` when there is one) for http, https, ws, wss and ftp. A blob URL whose path parses as an http or https URL has the origin of that URL. Every other URL's origin is `"null"`.

A `Url` is a view, not plain. It lives in the arena it was parsed into and holds a loan of its input, which `Parse` stores into that arena (`input: str into a`, `from a, input`). Copy `Href()` with `a.Dup` when a result must outlive the `Url`. The cookbook's [Parse a URL and resolve a link](../cookbook.md#parse-a-url-and-resolve-a-link) does that.

## Changing a URL

`Url.Set(a, part, value)` returns a new `Url` with one part replaced, as the URL Standard's setter for that part does it. Those setters are the `href`, `protocol`, `search` and other properties of a browser's `URL` object. The original is unchanged.

- A value the setter ignores gives the URL back as it was: a scheme that cannot change from special to non-special, a port on a URL with no host.
- `Part.Protocol` takes the scheme with or without its `:`. `Part.Search` and `Part.Hash` take their text with or without `?` or `#`. `""` for `Port`, `Search` or `Hash` removes that part.
- Only `Part.Href` fails with `BadUrl` or `BadHost`, since it parses the whole value as `Parse` does.

## Hosts and international names

`Url.Host()` returns a `Host`: a `Domain` (ASCII, lower case), an `Ipv4` address (its 32-bit value) or an `Ipv6` address (16 bytes). A scheme that is not special, such as `ssh:`, may have an `Opaque` host. `Empty` stands for an empty host and for no host at all. `Hostname()` is the same host as text, an IPv6 address in brackets.

A domain with non-ASCII letters goes through UTS 46 and is stored in its ASCII form: `https://bücher.example/` has the host `xn--bcher-kva.example`. `DomainToUnicode` turns it back for display.

`Parse` follows the URL Standard here: an ASCII host is lower-cased and not checked by UTS 46, so an `xn--` label in a parsed URL need not be valid Punycode. When a name must be valid, check it with `DomainToAscii(a, host, true)`. It applies UTS 46 with its strict rules (CheckHyphens, UseSTD3ASCIIRules and VerifyDnsLength) and fails with `BadHost`. `DomainToUnicode` never fails on its input: when UTS 46 records an error it returns the domain unchanged.

## Errors

| Member | Meaning |
|---|---|
| `BadUrl(offset)` | The parser refused the input; `offset` is the byte where it stopped |
| `BadHost` | The host is not a valid IPv4 or IPv6 address or domain: a forbidden code point, a number out of range, or a name UTS 46 refuses |
| `IdnaUnsupported` | The host needs UTS 46 and std was built without IDNA. std as shipped includes IDNA, so no call returns this member |
| `OutOfMemory` | The arena is full (from the prelude) |

`url.Error` is the first three; each function's signature adds `OutOfMemory`. The set is open, so a `match` on it outside std keeps a `_` arm.

`Parse` takes input from anyone: it never panics on its input, and its memory is bounded by the input's length. Its input is a `str`, so it is UTF-8; read bytes from the network with `str.From` first.

## Memory and cost

Every function that builds text takes an `a: &Arena` and puts its result there. `Parse` costs O(n + m) for n bytes of input and an m-byte base. It allocates the serialization, at most 3n + m + 64 bytes, and O(n + m) bytes of scratch. Input that is already in canonical form costs one copy and no scratch. `Url.Set` costs O(n + m) for the URL and the value. `ParseQuery` and `FormatQuery` are O(n). The domain conversions are O(n log n) in the name's length, for Punycode.

`Url.Format`, what `{u}` runs, writes to a `Writer`. If the write is interrupted part way, a prefix of the URL has been written, as with any write to a stream.

## Examples

Each program below runs as written with `mk run`.

### Read the parts of a URL

```mk
// verdict: ok
import std.url

export Main(sys: &System): url.Error|OutOfMemory|WriteError!void {
    u := try url.Parse(sys.arena, "HTTPS://user:secret@Example.COM:443/a/./b/../c?q=1#top", none)
    try sys.Print("href     {h}\n", (h: u.Href()))
    try sys.Print("printed  {u}\n", (u: &u)) // {u} leaves out the password
    try sys.Print("scheme   {s}\n", (s: u.Scheme()))
    try sys.Print("hostname {h}\n", (h: u.Hostname()))
    try sys.Print("path     {p}\n", (p: u.Path()))
    try sys.Print("query    {q}\n", (q: u.Query()))
    try sys.Print("fragment {f}\n", (f: u.Fragment()))
    if p := u.Port() {
        try sys.Print("port     {p}\n", (p))
    } else {
        try sys.Print("port     default\n", ()) // 443 is https's default
    }
}
```

```text
$ mk run parts.mk
href     https://user:secret@example.com/a/c?q=1#top
printed  https://user@example.com/a/c?q=1#top
scheme   https
hostname example.com
path     /a/c
query    q=1
fragment top
port     default
```

- The scheme and host are lower-cased, `./` and `b/../` are resolved, and the default port is dropped.
- The accessors this program calls return views of `u`'s text; none allocates.

### Resolve links and change parts

```mk
// verdict: ok
import std.url

export Main(sys: &System): url.Error|OutOfMemory|WriteError!void {
    base := try url.Parse(sys.arena, "https://example.com/docs/guide/intro.html", none)
    for link in ["setup.html", "../api/", "/search?q=mk", "//cdn.example.net/x.js", "#top"] {
        u := try url.Parse(sys.arena, link, some(&base)) // relative to base
        try sys.Print("{link} -> {u}\n", (link, u: &u))
    }
    var v := try base.Set(sys.arena, url.Part.Search, "page=2")
    v = try v.Set(sys.arena, url.Part.Hash, "#install") // with or without the '#'
    v = try v.Set(sys.arena, url.Part.Port, "8443")
    try sys.Print("{v}\n", (v: &v))
}
```

```text
$ mk run links.mk
setup.html -> https://example.com/docs/guide/setup.html
../api/ -> https://example.com/docs/api/
/search?q=mk -> https://example.com/search?q=mk
//cdn.example.net/x.js -> https://cdn.example.net/x.js
#top -> https://example.com/docs/guide/intro.html#top
https://example.com:8443/docs/guide/intro.html?page=2#install
```

- Each `Set` returns a new `Url` in the arena; `base` is unchanged.

### Read and write a query

```mk
// verdict: ok
import std.url

export Main(sys: &System): url.Error|OutOfMemory|WriteError!void {
    u := try url.Parse(sys.arena, "https://shop.example/find?q=red+shoes&size=42&q=caf%C3%A9", none)
    pairs := try url.ParseQuery(sys.arena, u.Query())
    for p in pairs[..] {
        try sys.Print("{name} = {value}\n", (name: p.name, value: p.value))
    }
    out := try url.FormatQuery(sys.arena, [(name: "q", value: "blue & green"), (name: "page", value: "2")][..])
    try sys.Print("{out}\n", (out))
}
```

```text
$ mk run query.mk
q = red shoes
size = 42
q = café
q=blue+%26+green&page=2
```

- `ParseQuery` reads `+` as a space and decodes percent escapes. The pairs keep their order, and a name may repeat.
- `FormatQuery` writes a space as `+` and escapes the rest, so its output reads back with `ParseQuery`.

### Refused URLs and international names

```mk
// verdict: ok
import std.url

export Main(sys: &System): OutOfMemory|WriteError!void {
    for input in ["https://bücher.example/", "http://[::1]:8080/", "http://exa mple.com/", "https://999.1.1.1/"] {
        u := url.Parse(sys.arena, input, none) else e {
            match e {
                url.BadUrl(offset) { try sys.Print("{input}: bad URL at byte {offset}\n", (input, offset)) }
                url.BadHost { try sys.Print("{input}: bad host\n", (input)) }
                _ { try sys.Print("{input}: {e}\n", (input, e)) }
            }
            continue
        }
        try sys.Print("{input}: host {h}\n", (input, h: u.Hostname()))
    }
    name := try url.DomainToUnicode(sys.arena, "xn--bcher-kva.example")
    try sys.Print("{name}\n", (name))
}
```

```text
$ mk run hosts.mk
https://bücher.example/: host xn--bcher-kva.example
http://[::1]:8080/: host [::1]
http://exa mple.com/: bad host
https://999.1.1.1/: bad host
bücher.example
```

- A space is a forbidden code point in a host, and a host whose last label is a number must be a valid IPv4 address: both are `BadHost`.
- `url.Error` is open, so the `match` keeps a `_` arm.

## API

<!-- api:begin -->
Stability: Stable.

### `BadHost`

```
export error BadHost
```

A host the URL Standard's host parser refuses: a bad IPv4 or IPv6
address, a forbidden code point, a domain that is empty or not valid
UTF-8 once percent-decoded, or a domain UTS 46's ToASCII refuses.

### `BadUrl`

```
export error BadUrl(offset: int)
```

Input the URL parser refuses. offset is the byte of the input, as
given, where the parser stopped.

### `DomainToAscii`

```
export DomainToAscii(a: &Arena, domain: str, strict: bool): Error|OutOfMemory!str from a
```

- Flows: The result borrows from a.
- Errors: Error|OutOfMemory (OutOfMemory, BadHost, BadUrl(offset: int), IdnaUnsupported)

The ASCII form of a domain name, as the URL Standard's "domain to
ASCII" gives it with strict as its beStrict: "bücher.example" becomes
"xn--bcher-kva.example".

With strict false, an ASCII domain is lower-cased and any other goes
through UTS 46's ToASCII, as Parse reads the host of a special URL.
With strict true, every domain goes through ToASCII with CheckHyphens,
UseSTD3ASCIIRules and VerifyDnsLength, so an xn-- label must also be
valid Punycode. Errors: BadHost where the parser fails, OutOfMemory.
O(n log n) for n bytes (Punycode); allocates O(n) in a.

### `DomainToUnicode`

```
export DomainToUnicode(a: &Arena, domain: str): OutOfMemory!str from a
```

- Flows: The result borrows from a.
- Errors: OutOfMemory

The Unicode form of a domain name, as the URL Standard's "domain to
Unicode" gives it: UTS 46's ToUnicode with CheckHyphens and
UseSTD3ASCIIRules false, each Punycode label decoded. When ToUnicode
records an error, the result is domain itself, copied. Cannot fail but
for OutOfMemory. O(n log n) for n bytes (Punycode); allocates O(n) in
a.

### `Error`

```
export open type Error = BadUrl | BadHost | IdnaUnsupported
```

Every way a std.url function fails. It is open: a match on it outside
std keeps a `_` arm.

### `FormatQuery`

```
export FormatQuery(a: &Arena, pairs: [](name: str, value: str)): OutOfMemory!str from a
```

- Flows: The result borrows from a.
- Errors: OutOfMemory

Joins pairs into a query (without a '?'), as the URL Standard's
application/x-www-form-urlencoded serializer does: each name and value
percent-encoded, a space written as '+', the two joined by '=' and the
pairs by '&'. Cannot fail but for OutOfMemory. O(n); allocates the
result in a, at most 3n bytes plus a separator per pair.

### `Host`

```
export enum Host
```

A URL's host: a domain (ASCII, lower case), an IPv4 address as its
32-bit value, an IPv6 address as its 16 bytes in network order, an
opaque host (the host of a URL whose scheme is not special,
percent-encoded), or Empty, which stands both for an empty host and for
no host at all.

Variants:

- `Domain(name: str)`: A domain, ASCII and lower case.
- `Ipv4(addr: u32)`: An IPv4 address as its 32-bit value.
- `Ipv6(addr: [16]u8)`: An IPv6 address as its 16 bytes in network order.
- `Opaque(text: str)`: The percent-encoded host of a URL whose scheme is not special.
- `Empty`: An empty host, or no host at all.

### `IdnaUnsupported`

```
export error IdnaUnsupported
```

A host that needs UTS 46 processing (a non-ASCII code point, or a label
that starts with xn--) in a build of std without IDNA.

### `Parse`

```
export Parse(a: &Arena, input: str into a, base: ?&Url): Error|OutOfMemory!Url from a, input
```

- Flows: The result borrows from a and input.
- Errors: Error|OutOfMemory (OutOfMemory, BadHost, BadUrl(offset: int), IdnaUnsupported)

Parses input as a URL, resolved against base when one is given, by the
WHATWG URL Standard's parser; input is UTF-8, as every str is. The
result lives in a.

Errors: BadUrl where the parser fails, BadHost for a host the host
parser refuses, IdnaUnsupported for a host that needs IDNA in a build
without it, OutOfMemory. An ASCII host is lower-cased and not checked
by UTS 46, so an xn-- label in a parsed URL need not be valid Punycode;
DomainToAscii(a, host, true) checks it. Input from a peer never makes
Parse panic, and its memory is bounded by the input's length.

O(n + m) for n bytes of input and an m-byte base. It allocates in a the
URL's serialization (at most 3n + m + 64 bytes) and O(n + m) bytes of
scratch: the components that need encoding, the path's segments, and,
only when the parser reads the base beyond its scheme (a relative
input), a copy of the base's href and segments. Input that is already
in its serialized form costs one copy and no scratch, with or without a
base. The parser keeps views of input in a, so input is stored into a
and the result holds its loan.

### `ParseQuery`

```
export ParseQuery(a: &Arena, s: str): OutOfMemory!List[(name: str, value: str)] from a
```

- Flows: The result borrows from a.
- Errors: OutOfMemory

Splits a query (without its '?') into name and value pairs, as the URL
Standard's application/x-www-form-urlencoded parser does. Each
non-empty '&'-separated piece splits at its first '='; '+' reads as a
space, percent escapes are decoded, and malformed UTF-8 becomes U+FFFD.
A piece with no '+' and no '%' is one copy of its bytes, with no
decoding or UTF-8 check. The pairs keep their order, and a name may
repeat. Cannot fail but for OutOfMemory. O(n); allocates at most n
bytes and the list in a, and a malformed value's decoding.

### `Part`

```
export enum Part
```

The component Url.Set changes. Each member is one of the URL
Standard's setters: Href replaces the whole URL, Protocol the scheme,
Pathname the path, Search the query and Hash the fragment.

Variants:

- `Href`
- `Protocol`
- `Username`
- `Password`
- `Host`
- `Hostname`
- `Port`
- `Pathname`
- `Search`
- `Hash`

### `Url`

```
export view struct Url
```

A parsed URL. It holds its serialization, in the arena it was parsed
into, and its components are views of that text. A Url is a view, not
plain: it cannot outlive its arena or its input. `{u}` prints it
without the password; Href is the exact text.

### `Url.Fragment`

```
export Url.Fragment(&self): str from self
```

- Flows: The result borrows from self.

The URL's fragment without its '#'; "" when it has none. O(1);
allocates nothing.

### `Url.Host`

```
export Url.Host(&self): Host from self
```

- Flows: The result borrows from self.

The URL's host; Empty for an empty host and for a URL with none.
O(1); allocates nothing.

### `Url.Hostname`

```
export Url.Hostname(&self): str from self
```

- Flows: The result borrows from self.

The URL's host as text, an IPv6 address in brackets, or "" when it has
none: the URL Standard's hostname. O(1); allocates nothing.

### `Url.Href`

```
export Url.Href(&self): str from self
```

- Flows: The result borrows from self.

The URL's serialization, the URL Standard's href: the exact text,
password included. O(1); allocates nothing.

### `Url.Origin`

```
export Url.Origin(&self, a: &Arena): OutOfMemory!str from a
```

- Flows: The result borrows from a.
- Errors: OutOfMemory

The URL's origin as text: "scheme://host", with ":port" when it has a
port, for http, https, ws, wss and ftp, and for a blob URL whose path
parses as an http or https URL; "null" for every other URL. O(n);
allocates the result in a, and for a blob URL the parse of its path.

### `Url.Password`

```
export Url.Password(&self): str from self
```

- Flows: The result borrows from self.

The URL's password, percent-encoded; "" when it has none. O(1);
allocates nothing.

### `Url.Path`

```
export Url.Path(&self): str from self
```

- Flows: The result borrows from self.

The URL's path as text, the URL Standard's pathname. O(1); allocates
nothing.

### `Url.Port`

```
export Url.Port(&self): ?u16
```

The URL's port; none when it has none, or when the port is its
scheme's default (80 for http, 443 for https), which the parser drops.
O(1); allocates nothing.

### `Url.Query`

```
export Url.Query(&self): str from self
```

- Flows: The result borrows from self.

The URL's query without its '?'; "" when it has none. Read its pairs
with ParseQuery. O(1); allocates nothing.

### `Url.Scheme`

```
export Url.Scheme(&self): str from self
```

- Flows: The result borrows from self.

The URL's scheme, lower case, without the ':'. O(1); allocates
nothing.

### `Url.Set`

```
export Url.Set(&self, a: &Arena, part: Part, value: str): Error|OutOfMemory!Url from a
```

- Flows: The result borrows from a.
- Errors: Error|OutOfMemory (OutOfMemory, BadHost, BadUrl(offset: int), IdnaUnsupported)

A copy of the URL with part set to value, as the URL Standard's setter
of that part does it, in a. self is unchanged.

A value the setter ignores gives the URL as it was, and one it refuses
part way keeps what it had set (a host whose port is out of range keeps
the new host). Protocol takes the scheme with or without its ':',
Search and Hash take their text with or without the '?' or '#', and ""
for Port, Search or Hash removes that component.

Errors: Href parses value and fails as Parse does (BadUrl, BadHost); no
other part fails with BadUrl or BadHost. Any part fails with
IdnaUnsupported for a host that needs IDNA in a build without it.
O(n + m) for the URL's n bytes and the value's m; allocates the new
serialization in a, and the components that need encoding. value is
copied into a first, an Href's too, so the result holds no loan of
value.

### `Url.Username`

```
export Url.Username(&self): str from self
```

- Flows: The result borrows from self.

The URL's username, percent-encoded; "" when it has none. O(1);
allocates nothing.

<!-- api:end -->
