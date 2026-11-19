// Why: the request target and framing rules are std.http's defence against
// request smuggling.
// What: the four target forms and the Host field, Dispatch's 400 on a dot
// segment, and a corpus of smuggling shapes refused at the head.
// How: ParseHead's verdict on each head (TgOff, TgPath), shared by
// ReadRequest, Serve, ServeWith, Fetch and the Client.

// TgOff is ParseHead's verdict on a request head: -1 when it parses, the
// BadRequest offset when it does not, -2 for any other error.
TgOff(a: &Arena, text: str into a): int {
    _ = ParseHead(a, text, false, 0) else e {
        match e {
            BadRequest(offset) {
                return offset
            }
            _ {
                return -2
            }
        }
    }
    return -1
}

// TgPath is the path and query ParseHead reads from a request head, as
// "path|query", or "!" when it does not parse.
TgPath(a: &Arena, text: str into a): OutOfMemory!str from a {
    h := ParseHead(a, text, false, 0) else {
        return "!"
    }
    return try a.Fmt("{p}|{q}", (p: h.path, q: h.query))
}

test "origin-form keeps the bytes received, neither decoded nor normalised" (sys: &System) {
    var ar := try Arena.New(sys.heap)
    defer ar.Free()
    Assert(try TgPath(&ar, "GET /a/../b%2F?x=%41&y=/? HTTP/1.1\r\nHost: h\r\n\r\n") == "/a/../b%2F|x=%41&y=/?")
    Assert(try TgPath(&ar, "GET //x HTTP/1.1\r\nHost: h\r\n\r\n") == "//x|")
    Assert(try TgPath(&ar, "GET /;p=1:@!$&'()*+,~ HTTP/1.1\r\nHost: h\r\n\r\n") == "/;p=1:@!$&'()*+,~|")
}

test "a target byte outside RFC 3986's path and query sets is BadRequest at that byte" (sys: &System) {
    var ar := try Arena.New(sys.heap)
    defer ar.Free()
    // backslash, '#', '"', '<', '|', '{', '^', '`', '[', non-ASCII
    Assert(TgOff(&ar, "GET /a\\b HTTP/1.1\r\nHost: h\r\n\r\n") == 6)
    Assert(TgOff(&ar, "GET /a#f HTTP/1.1\r\nHost: h\r\n\r\n") == 6)
    Assert(TgOff(&ar, "GET /\"x HTTP/1.1\r\nHost: h\r\n\r\n") == 5)
    Assert(TgOff(&ar, "GET /<x HTTP/1.1\r\nHost: h\r\n\r\n") == 5)
    Assert(TgOff(&ar, "GET /?a|b HTTP/1.1\r\nHost: h\r\n\r\n") == 7)
    Assert(TgOff(&ar, "GET /{x} HTTP/1.1\r\nHost: h\r\n\r\n") == 5)
    Assert(TgOff(&ar, "GET /^ HTTP/1.1\r\nHost: h\r\n\r\n") == 5)
    Assert(TgOff(&ar, "GET /` HTTP/1.1\r\nHost: h\r\n\r\n") == 5)
    Assert(TgOff(&ar, "GET /[ HTTP/1.1\r\nHost: h\r\n\r\n") == 5)
    Assert(TgOff(&ar, "GET /\u{e9} HTTP/1.1\r\nHost: h\r\n\r\n") == 5)
    // a tab or a control byte ends nothing: it is refused where it stands
    Assert(TgOff(&ar, "GET /a\tb HTTP/1.1\r\nHost: h\r\n\r\n") == 6)
    Assert(TgOff(&ar, "GET /a\u{0}b HTTP/1.1\r\nHost: h\r\n\r\n") == 6)
}

test "a % not followed by two hex digits is BadRequest at the %" (sys: &System) {
    var ar := try Arena.New(sys.heap)
    defer ar.Free()
    Assert(TgOff(&ar, "GET /a%2 HTTP/1.1\r\nHost: h\r\n\r\n") == 6)
    Assert(TgOff(&ar, "GET /a%zz HTTP/1.1\r\nHost: h\r\n\r\n") == 6)
    Assert(TgOff(&ar, "GET /?q=% HTTP/1.1\r\nHost: h\r\n\r\n") == 8)
    Assert(TgOff(&ar, "GET /a%2f%2F HTTP/1.1\r\nHost: h\r\n\r\n") == -1)
}

test "an origin-form that does not start with / and an empty target are BadRequest" (sys: &System) {
    var ar := try Arena.New(sys.heap)
    defer ar.Free()
    Assert(TgOff(&ar, "GET a/b HTTP/1.1\r\nHost: h\r\n\r\n") == 4)
    Assert(TgOff(&ar, "GET ?q HTTP/1.1\r\nHost: h\r\n\r\n") == 4)
    Assert(TgOff(&ar, "GET  HTTP/1.1\r\nHost: h\r\n\r\n") == 4)
}

test "absolute-form is http or https, split strictly, its authority equal to Host" (sys: &System) {
    var ar := try Arena.New(sys.heap)
    defer ar.Free()
    Assert(try TgPath(&ar, "GET http://h:8080/p?q HTTP/1.1\r\nHost: h:8080\r\n\r\n") == "/p|q")
    Assert(try TgPath(&ar, "GET HTTPS://H HTTP/1.1\r\nHost: h\r\n\r\n") == "/|")
    Assert(try TgPath(&ar, "GET http://h?q HTTP/1.1\r\nHost: h\r\n\r\n") == "/|q")
    // ftp, a scheme with no "//", userinfo, an empty authority
    Assert(TgOff(&ar, "GET ftp://h/ HTTP/1.1\r\nHost: h\r\n\r\n") == 4)
    Assert(TgOff(&ar, "GET http:/h HTTP/1.1\r\nHost: h\r\n\r\n") == 4)
    Assert(TgOff(&ar, "GET http://u@h/ HTTP/1.1\r\nHost: h\r\n\r\n") == 12)
    Assert(TgOff(&ar, "GET http:///p HTTP/1.1\r\nHost: h\r\n\r\n") == 11)
    // a port that is not digits
    Assert(TgOff(&ar, "GET http://h:8x/ HTTP/1.1\r\nHost: h\r\n\r\n") == 14)
    // Host names another origin than the target
    Assert(TgOff(&ar, "GET http://a/ HTTP/1.1\r\nHost: b\r\n\r\n") == 30)
    Assert(TgOff(&ar, "GET http://a/ HTTP/1.1\r\nHost: a:80\r\n\r\n") == 30)
    // with no Host on HTTP/1.0 the authority stands alone
    Assert(TgOff(&ar, "GET http://a/ HTTP/1.0\r\n\r\n") == -1)
}

test "authority-form is CONNECT's only, with a port; * is OPTIONS' only" (sys: &System) {
    var ar := try Arena.New(sys.heap)
    defer ar.Free()
    Assert(try TgPath(&ar, "CONNECT h:443 HTTP/1.1\r\nHost: h:443\r\n\r\n") == "h:443|")
    Assert(try TgPath(&ar, "CONNECT [::1]:443 HTTP/1.1\r\nHost: h\r\n\r\n") == "[::1]:443|")
    Assert(TgOff(&ar, "CONNECT h HTTP/1.1\r\nHost: h\r\n\r\n") == 9)
    Assert(TgOff(&ar, "CONNECT h: HTTP/1.1\r\nHost: h\r\n\r\n") == 10)
    Assert(TgOff(&ar, "CONNECT /x HTTP/1.1\r\nHost: h\r\n\r\n") == 8)
    Assert(TgOff(&ar, "GET h:443 HTTP/1.1\r\nHost: h\r\n\r\n") == 4)
    Assert(try TgPath(&ar, "OPTIONS * HTTP/1.1\r\nHost: h\r\n\r\n") == "*|")
    Assert(TgOff(&ar, "GET * HTTP/1.1\r\nHost: h\r\n\r\n") == 4)
    Assert(TgOff(&ar, "OPTIONS *x HTTP/1.1\r\nHost: h\r\n\r\n") == 8)
}

test "Host is a host and an optional port, at most once on HTTP/1.1, and required there" (sys: &System) {
    var ar := try Arena.New(sys.heap)
    defer ar.Free()
    Assert(TgOff(&ar, "GET / HTTP/1.1\r\nHost: example.com:80\r\n\r\n") == -1)
    Assert(TgOff(&ar, "GET / HTTP/1.1\r\nHost: [::1]:8080\r\n\r\n") == -1)
    Assert(TgOff(&ar, "GET / HTTP/1.1\r\nHost: 10.0.0.1\r\n\r\n") == -1)
    Assert(TgOff(&ar, "GET / HTTP/1.1\r\nHost: \r\n\r\n") == -1)
    Assert(TgOff(&ar, "GET / HTTP/1.1\r\nHost: a/b\r\n\r\n") == 23)
    Assert(TgOff(&ar, "GET / HTTP/1.1\r\nHost: a b\r\n\r\n") == 23)
    Assert(TgOff(&ar, "GET / HTTP/1.1\r\nHost: u@a\r\n\r\n") == 23)
    Assert(TgOff(&ar, "GET / HTTP/1.1\r\nHost: a:x\r\n\r\n") == 24)
    Assert(TgOff(&ar, "GET / HTTP/1.1\r\nHost: [::1\r\n\r\n") == 26)
    Assert(TgOff(&ar, "GET / HTTP/1.1\r\nHost: a\r\nHost: a\r\n\r\n") == 25)
    Assert(TgOff(&ar, "GET / HTTP/1.1\r\n\r\n") == 16)
}

test "The strict framing corpus: each smuggling shape is refused at the head" (sys: &System) {
    var ar := try Arena.New(sys.heap)
    defer ar.Free()
    // both Transfer-Encoding and Content-Length, in either order
    Assert(TgOff(&ar, "POST / HTTP/1.1\r\nHost: h\r\nContent-Length: 3\r\nTransfer-Encoding: chunked\r\n\r\n") >= 0)
    Assert(TgOff(&ar, "POST / HTTP/1.1\r\nHost: h\r\nTransfer-Encoding: chunked\r\nContent-Length: 3\r\n\r\n") >= 0)
    // a Content-Length that is not one run of digits, and two that differ
    Assert(TgOff(&ar, "POST / HTTP/1.1\r\nHost: h\r\nContent-Length: +3\r\n\r\n") >= 0)
    Assert(TgOff(&ar, "POST / HTTP/1.1\r\nHost: h\r\nContent-Length: 3, 3\r\n\r\n") >= 0)
    Assert(TgOff(&ar, "POST / HTTP/1.1\r\nHost: h\r\nContent-Length: 0x3\r\n\r\n") >= 0)
    Assert(TgOff(&ar, "POST / HTTP/1.1\r\nHost: h\r\nContent-Length:\r\n\r\n") >= 0)
    Assert(TgOff(&ar, "POST / HTTP/1.1\r\nHost: h\r\nContent-Length: 3\r\nContent-Length: 4\r\n\r\n") >= 0)
    Assert(TgOff(&ar, "POST / HTTP/1.1\r\nHost: h\r\nContent-Length: 3\r\nContent-Length: 3\r\n\r\n") == -1)
    // a coding other than a final chunked, two, and any on HTTP/1.0
    Assert(TgOff(&ar, "POST / HTTP/1.1\r\nHost: h\r\nTransfer-Encoding: gzip, chunked\r\n\r\n") >= 0)
    Assert(TgOff(&ar, "POST / HTTP/1.1\r\nHost: h\r\nTransfer-Encoding: chunked, gzip\r\n\r\n") >= 0)
    Assert(TgOff(&ar, "POST / HTTP/1.1\r\nHost: h\r\nTransfer-Encoding: xchunked\r\n\r\n") >= 0)
    Assert(TgOff(&ar, "POST / HTTP/1.1\r\nHost: h\r\nTransfer-Encoding: chunked\r\nTransfer-Encoding: chunked\r\n\r\n") >= 0)
    Assert(TgOff(&ar, "POST / HTTP/1.0\r\nTransfer-Encoding: chunked\r\n\r\n") >= 0)
    // obsolete line folding, whitespace before the first field, whitespace
    // before a colon, a name byte that is not a token byte
    Assert(TgOff(&ar, "GET / HTTP/1.1\r\nHost: h\r\nX: a\r\n b\r\n\r\n") >= 0)
    Assert(TgOff(&ar, "GET / HTTP/1.1\r\n Host: h\r\n\r\n") >= 0)
    Assert(TgOff(&ar, "GET / HTTP/1.1\r\nHost : h\r\n\r\n") >= 0)
    Assert(TgOff(&ar, "GET / HTTP/1.1\r\nHost: h\r\nX[1]: a\r\n\r\n") >= 0)
    // a NUL or a control byte in a value, a bare CR or LF in the head
    Assert(TgOff(&ar, "GET / HTTP/1.1\r\nHost: h\r\nX: a\u{0}b\r\n\r\n") >= 0)
    Assert(TgOff(&ar, "GET / HTTP/1.1\r\nHost: h\r\nX: a\u{1b}b\r\n\r\n") >= 0)
    Assert(TgOff(&ar, "GET / HTTP/1.1\r\nHost: h\r\nX: a\rb\r\n\r\n") >= 0)
    Assert(TgOff(&ar, "GET / HTTP/1.1\nHost: h\r\n\r\n") >= 0)
    // the same rules on a reply's head
    _ = ParseHead(&ar, "HTTP/1.1 200 OK\r\nContent-Length: 1\r\nTransfer-Encoding: chunked\r\n\r\n", true, 0) else {
        return
    }
    Assert(false)
}

test "Dispatch answers 400 to a . or .. segment, plain or percent-encoded" (sys: &System) {
    Assert(HasDotSegment("/a/./b"))
    Assert(HasDotSegment("/a/.."))
    Assert(HasDotSegment("/%2e/b"))
    Assert(HasDotSegment("/a/%2E%2e/"))
    Assert(HasDotSegment("/a/.%2E"))
    Assert(HasDotSegment("/a/%2e."))
    Assert(!HasDotSegment("/a/.b/c."))
    Assert(!HasDotSegment("/a/.../b"))
    Assert(!HasDotSegment("/a//b"))
    Assert(!HasDotSegment("/"))
    Assert(!HasDotSegment("*"))
}
