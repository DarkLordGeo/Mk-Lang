// Why: certificates and keys travel as PEM text, and a reader must find
// the blocks in lax text without letting an encrypted key read as a plain
// one.
//
// What: PemBlock, ParsePem and FormatPem, RFC 7468's textual encoding.
//
// How: ParsePem reads the standard's lax form (section 3): text outside
// the blocks is skipped as explanatory text, whitespace (space, tab, CR,
// LF, vertical tab and form feed) may sit anywhere in a block's base64, and
// lines end in LF, CRLF or CR. Inside a block it is strict: the label is
// section 3's label, the end line names the same label, the base64 is
// canonical and padded, and an RFC 1421 header line
// ("Proc-Type: 4,ENCRYPTED"), which section 2 leaves out of the format, is
// refused. FormatPem writes section 2's strict form, 64 characters a line,
// as Go's encoding/pem does.

/// PemBlock is one PEM block: its label ("CERTIFICATE", "PRIVATE KEY") and
/// the DER bytes its base64 holds.
export struct PemBlock {
    /// The block's label, such as "CERTIFICATE" or "PRIVATE KEY".
    export label: str
    /// The DER bytes the block's base64 holds.
    export der: []u8
}

const PemBegin: str = "-----BEGIN "
const PemEnd: str = "-----END "
const PemDashes: str = "-----"

// LineEnd is the offset of the end of the line that starts at i (its LF,
// CR or text's end), and Next the start of the line after it.
type Line = (end: int, next: int)

LineAt(b: []u8, i: int): Line {
    var j := i
    for j < b.Len() && b[j] != 0x0A && b[j] != 0x0D {
        j += 1
    }
    var k := j
    if k < b.Len() && b[k] == 0x0D {
        k += 1
    }
    if k < b.Len() && b[k] == 0x0A && (k == j || b[j] == 0x0D) {
        k += 1
    }
    return (end: j, next: k)
}

// Trimmed is text[i..end] without its trailing spaces and tabs.
Trimmed(text: str, i: int, end: int): str from text {
    b := text.AsBytes()
    var e := end
    for e > i && (b[e - 1] == 0x20 || b[e - 1] == 0x09) {
        e -= 1
    }
    return text[i..e]
}

// PemSpace is RFC 7468 section 3's W, the whitespace the lax form allows
// in a block's base64: WSP (space, tab), CR, LF, VT and FF. It is base64's
// IsSpace (WHATWG's set) with VT added.
PemSpace(c: u8): bool {
    return IsSpace(c) || c == 0x0B
}

// LabelOk is RFC 7468 section 3's label: printable ASCII but `-`, with
// single spaces or hyphens between such characters, or empty.
LabelOk(l: str): bool {
    b := l.AsBytes()
    for i in 0..b.Len() {
        c := b[i]
        if c == b'-' || c == b' ' {
            if i == 0 || i == b.Len() - 1 || b[i - 1] == b'-' || b[i - 1] == b' ' {
                return false
            }
        } else if c < 0x21 || c > 0x7E {
            return false
        }
    }
    return true
}

// Boundary is the label of a line "-----BEGIN label-----" (or END, by
// head), none when the line is not one.
Boundary(line: str, head: str): ?str from line {
    if !line.StartsWith(head) || !line.EndsWith(PemDashes) || line.Len() < head.Len() + PemDashes.Len() {
        return none
    }
    return some(line[head.Len()..line.Len() - PemDashes.Len()])
}

/// ParsePem is every PEM block of text, in order, with the text around them
/// skipped. Fails with BadInput at the start of a block's begin line when
/// its label is not RFC 7468's, its end line is missing, or its base64 is
/// not canonical padded base64 (with whitespace allowed); at the start of
/// the end line when it names another label or has text after its closing
/// dashes; and at the start of a header line, a line holding `:` such as
/// "Proc-Type: 4,ENCRYPTED", so an encrypted key never reads as a plain
/// one. O(n); allocates the list, the decoded bytes, and each block's
/// base64 length while it is read. Each block's label is a view of text
/// kept in the list in a, so text is stored into a.
export ParsePem(a: &Arena, text: str into a): Error|OutOfMemory!List[PemBlock] from a, text {
    var blocks := List[PemBlock].New(a)
    b := text.AsBytes()
    var i := 0
    for i < b.Len() {
        ln := LineAt(b, i)
        label := Boundary(Trimmed(text, i, ln.end), PemBegin) else {
            i = ln.next
            continue
        }
        begin := i
        if !LabelOk(label) {
            fail BadInput(offset: begin)
        }
        // The body runs to the end line; count its base64 characters.
        var j := ln.next
        var count := 0
        var close := -1
        var after := -1
        for j < b.Len() {
            bl := LineAt(b, j)
            line := Trimmed(text, j, bl.end)
            if line.StartsWith(PemEnd) {
                named := Boundary(line, PemEnd) else {
                    fail BadInput(offset: j)
                }
                if named != label {
                    fail BadInput(offset: j)
                }
                close = j
                after = bl.next
                break
            }
            for x in b[j..bl.end] {
                if x == b':' {
                    fail BadInput(offset: j)
                }
                if !PemSpace(x) {
                    count += 1
                }
            }
            j = bl.next
        }
        if close < 0 {
            fail BadInput(offset: begin)
        }
        chars := try a.Slice[u8](count)
        var o := 0
        for x in b[ln.next..close] {
            if !PemSpace(x) {
                chars[o] = x
                o += 1
            }
        }
        shape := StrictShape(chars[..], true)
        der := try a.Slice[u8](shape.out)
        StrictTo(mut der[..], chars[..], shape, Base64) else {
            fail BadInput(offset: begin)
        }
        try blocks.Push(PemBlock(label: label, der: der[..]))
        i = after
    }
    return blocks
}

/// FormatPem writes der as one PEM block with label: the begin line, the
/// base64 of der in lines of 64 characters, and the end line, each ending
/// in LF. The label is written as given. O(n); allocates nothing (one
/// 64-byte line on the stack).
export FormatPem(w: mut Writer, label: str, der: []u8): WriteError!void {
    try w.Str(PemBegin)
    try w.Str(label)
    try w.Str("-----\n")
    var line: [65]u8 = [0; 65]
    var i := 0
    for i < der.Len() {
        end := if der.Len() - i > 48 { i + 48 } else { der.Len() }
        k := Base64EncodeTo(mut line[..], der[i..end], Base64)
        line[k] = 0x0A
        try w.Bytes(line[0..k + 1])
        i = end
    }
    try w.Str(PemEnd)
    try w.Str(label)
    try w.Str("-----\n")
}
