// Why: `fetch url` shows how a client parses and resolves a URL, a common
// debugging question.
// What: UrlCommand, Parsed, Describe, HostText, Domain, Escape and Unescape.
// How: std.url parses against --base, applies each --set and prints every
// part; domains are shown in ASCII and Unicode forms (UTS 46).

import std.encoding
import std.url

// UrlCommand is `fetch url`: it parses a URL as a client would, against
// --base when given, applies each --set, and prints the parts, the query
// pairs and each --domain in its ASCII and Unicode forms.
UrlCommand(sys: &System, args: []str): int *args into sys {
    a := sys.arena
    var base: ?str = none
    var sets := List[str].New(a)
    var domains := List[str].New(a)
    var link: ?str = none
    var i := 0
    for i < args.Len() {
        arg := args[i]
        i += 1
        if arg == "--base" || arg == "--set" || arg == "--domain" || arg == "--escape" || arg == "--unescape" {
            if i == args.Len() {
                return Fatal(sys, 2, Quote(a, "a value is needed after", arg))
            }
            value := args[i]
            i += 1
            match arg {
                "--base" { base = some(value) }
                "--escape" { Escape(sys, value) }
                "--unescape" { Unescape(sys, value) }
                "--set" {
                    sets.Push(value) else {
                        return Fatal(sys, 1, "out of memory")
                    }
                }
                _ {
                    domains.Push(value) else {
                        return Fatal(sys, 1, "out of memory")
                    }
                }
            }
            continue
        }
        if arg.StartsWith("-") || link.IsSome() {
            return Fatal(sys, 2, Quote(a, "unexpected argument", arg))
        }
        link = some(arg)
    }
    for d in domains.Items() {
        Domain(sys, d)
    }
    text := link else {
        if domains.Len() > 0 || args.Len() > 0 {
            return 0
        }
        return Fatal(sys, 2, "fetch url needs a URL")
    }
    var u := Parsed(a, text, base) else e {
        return Fatal(sys, 3, a.Fmt("{text}: {e}", (text, e)) else { "bad URL" })
    }
    for s in sets.Items() {
        eq := s.Find("=") else {
            return Fatal(sys, 2, Quote(a, "--set needs PART=VALUE, not", s))
        }
        name := s[..eq]
        value := s[eq + 1..]
        part := Part(name) else {
            return Fatal(sys, 2, Quote(a, "unknown URL part", name))
        }
        u = u.Set(a, part, value) else e {
            return Fatal(sys, 3, a.Fmt("--set {s}: {e}", (s, e)) else { "bad --set" })
        }
    }
    Describe(sys, &u) else {
        return 1
    }
    return 0
}

// Parsed is text parsed as a URL, against base when one is given.
Parsed(a: &Arena, text: str into a, base: ?str into a): url.Error|OutOfMemory!url.Url from a, text {
    if b := base {
        root := try url.Parse(a, b, none)
        return try url.Parse(a, text, some(&root))
    }
    return try url.Parse(a, text, none)
}

// Describe prints every part of u, then its query's pairs.
Describe(sys: &System, u: &url.Url): WriteError|OutOfMemory!void {
    a := sys.arena
    href := u.Href()
    try sys.Print("href      {href}\nshown     {u}\n", (href, u))
    scheme := u.Scheme()
    user := u.Username()
    password := u.Password()
    try sys.Print("scheme    {scheme}\nusername  {user}\npassword  {password}\n", (scheme, user, password))
    host := try HostText(a, u.Host())
    hostname := u.Hostname()
    try sys.Print("host      {host}\nhostname  {hostname}\n", (host, hostname))
    port := if p := u.Port() { try a.Fmt("{p}", (p)) } else { "default" }
    path := u.Path()
    query := u.Query()
    fragment := u.Fragment()
    origin := try u.Origin(a)
    try sys.Print("port      {port}\npath      {path}\nquery     {query}\nfragment  {fragment}\norigin    {origin}\n", (port, path, query, fragment, origin))
    q := if query.StartsWith("?") { query[1..] } else { query }
    pairs := try url.ParseQuery(a, q)
    for pair in pairs.Items() {
        try sys.Print("pair      {pair.name} = {pair.value}\n", (pair))
    }
}

// HostText names a host's kind and shows its value.
HostText(a: &Arena, host: url.Host): OutOfMemory!str from a, host {
    return match host {
        .Domain(name) { try a.Fmt("domain {name}", (name)) }
        .Ipv4(addr) {
            b0 := addr >> 24
            b1 := (addr >> 16) & 255
            b2 := (addr >> 8) & 255
            b3 := addr & 255
            try a.Fmt("ipv4 {b0}.{b1}.{b2}.{b3}", (b0, b1, b2, b3))
        }
        .Ipv6(addr) {
            hex := try encoding.HexEncode(a, addr[..])
            try a.Fmt("ipv6 {hex}", (hex))
        }
        .Opaque(text) { try a.Fmt("opaque {text}", (text)) }
        .Empty { "empty" }
    }
}

// Domain prints a domain's ASCII form, as a client would look it up, and
// its Unicode form, as a browser would show it (UTS 46).
Domain(sys: &System, d: str) {
    a := sys.arena
    ascii := url.DomainToAscii(a, d, true) else e {
        sys.Print("domain    {d}: {e}\n", (d, e)) else {}
        return
    }
    shown := url.DomainToUnicode(a, ascii) else {
        return
    }
    sys.Print("domain    {d} -> {ascii} -> {shown}\n", (d, ascii, shown)) else {}
}

// Part is the url.Part a --set name selects.
Part(name: str): ?url.Part {
    return match name {
        "href" { some(url.Part.Href) }
        "protocol" { some(url.Part.Protocol) }
        "username" { some(url.Part.Username) }
        "password" { some(url.Part.Password) }
        "host" { some(url.Part.Host) }
        "hostname" { some(url.Part.Hostname) }
        "port" { some(url.Part.Port) }
        "pathname" { some(url.Part.Pathname) }
        "search" { some(url.Part.Search) }
        "hash" { some(url.Part.Hash) }
        _ { none }
    }
}

// Escape prints text percent-encoded as a URL component: every byte
// outside the URL Standard's component percent-encode set kept.
Escape(sys: &System, text: str) {
    out := encoding.PercentEncode(sys.arena, text.AsBytes(), encoding.PercentSet.Component) else {
        return
    }
    sys.Print("escaped   {out}\n", (out)) else {}
}

// Unescape prints text with its percent escapes decoded; bytes that are
// not UTF-8 are shown as their hex.
Unescape(sys: &System, text: str) {
    a := sys.arena
    bytes := encoding.PercentDecode(a, text) else {
        return
    }
    shown := str.From(bytes) else {
        hex := encoding.HexEncode(a, bytes) else {
            return
        }
        sys.Print("unescaped hex {hex}\n", (hex)) else {}
        return
    }
    sys.Print("unescaped {shown}\n", (shown)) else {}
}
