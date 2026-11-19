// Why: checking every name against every subtree of RFC 5280 4.2.1.10's
// name constraints is O(names x k), which a hostile certificate with many
// constraints turns into a denial of service.
//
// What: Side, one side (permitted or excluded) of an issuer's constraints,
// built by BuildSide and SortPrune, and the lookups DnsHeld, MailHeld and
// UriHeld, with the orders DnsCmp, IpCmp and FoldCmp.
//
// How: Go 1.27's constraints.go technique: each form's subtrees are sorted
// and pruned, so the one subtree that can hold a name is its predecessor in
// the order, found by binary search: O(k log k) to build, O(log k) per
// name. All work is charged to the caller's budget. The orders follow.

// Pruning drops every subtree that lies inside an earlier one. DNS names
// sort by Go's dnsCompare (from the last byte back, A to Z folded, '.'
// below every byte, so a domain sorts right before its subdomains); IP
// ranges by family, network and mask; exact mailboxes by their key bytes.
// The hosts of email and URI constraints keep RFC 5280's reading (a base is
// that host exactly, one with a leading '.' any host below it), which sorts
// and prunes by the same order.

// Side is one side (permitted or excluded) of an issuer's constraints:
// whether it has a base of each form, whether an empty base makes it hold
// every name of the form, and the sorted, pruned bases as spans of the
// subtrees list (boxes as spans of norm, the exact mailboxes' keys).
struct Side {
    dns_any: bool
    dns_all: bool
    ip_any: bool
    mail_any: bool
    mail_all: bool
    uri_any: bool
    uri_all: bool
    dns: []Span
    par: []Span
    ip: []Span
    mdom: []Span
    uri: []Span
    box: []Span
    norm: []u8
}

// Log2 is floor(log2(n)) for n >= 1, 0 below.
Log2(n: int): int {
    var k := 0
    var m := n
    for m > 1 {
        m = m >> 1
        k += 1
    }
    return k
}

// DnsByte is b in Go's dnsCompare order: A to Z lowered, '.' as 0.
DnsByte(b: u8): u8 {
    if b == b'.' {
        return 0
    }
    if b >= b'A' && b <= b'Z' {
        return b + 32
    }
    return b
}

// DnsCmp is Go's dnsCompare: a against b read from the last byte back, -1,
// 0 or 1; a name that is a suffix of the other sorts first.
DnsCmp(a: []u8, b: []u8): int {
    var i := a.Len() - 1
    var j := b.Len() - 1
    for i >= 0 && j >= 0 {
        x := DnsByte(a[i])
        y := DnsByte(b[j])
        if x != y {
            return if x < y { -1 } else { 1 }
        }
        i -= 1
        j -= 1
    }
    if i < j {
        return -1
    }
    if j < i {
        return 1
    }
    return 0
}

// FoldCmp is a against b from the first byte, A to Z folded.
FoldCmp(a: []u8, b: []u8): int {
    var i := 0
    for i < a.Len() && i < b.Len() {
        x := Lower(a[i])
        y := Lower(b[i])
        if x != y {
            return if x < y { -1 } else { 1 }
        }
        i += 1
    }
    if a.Len() == b.Len() {
        return 0
    }
    return if a.Len() < b.Len() { -1 } else { 1 }
}

// ByteCmp is a against b, byte for byte.
ByteCmp(a: []u8, b: []u8): int {
    var i := 0
    for i < a.Len() && i < b.Len() {
        if a[i] != b[i] {
            return if a[i] < b[i] { -1 } else { 1 }
        }
        i += 1
    }
    if a.Len() == b.Len() {
        return 0
    }
    return if a.Len() < b.Len() { -1 } else { 1 }
}

// IpCmp orders IP ranges (an address and its mask): IPv4 first, then by
// the network (the address under its mask), then by the mask.
IpCmp(a: []u8, b: []u8): int {
    if a.Len() != b.Len() {
        return if a.Len() < b.Len() { -1 } else { 1 }
    }
    n := a.Len() / 2
    for i in 0..n {
        x := a[i] & a[n + i]
        y := b[i] & b[n + i]
        if x != y {
            return if x < y { -1 } else { 1 }
        }
    }
    for i in n..a.Len() {
        if a[i] != b[i] {
            return if a[i] < b[i] { -1 } else { 1 }
        }
    }
    return 0
}

// IpAt orders range r against address x: by family, then r's network
// against x (Go's ipBinarySearch).
IpAt(r: []u8, x: []u8): int {
    if r.Len() != 2 * x.Len() {
        return if r.Len() < 2 * x.Len() { -1 } else { 1 }
    }
    n := x.Len()
    for i in 0..n {
        y := r[i] & r[n + i]
        if y != x[i] {
            return if y < x[i] { -1 } else { 1 }
        }
    }
    return 0
}

// IpSubset is whether range x lies inside range p (Go's ipNetworkSubset:
// p holds x's network and its last address).
IpSubset(p: []u8, x: []u8): bool {
    if p.Len() != x.Len() {
        return false
    }
    n := p.Len() / 2
    for i in 0..n {
        net := x[i] & x[n + i]
        last := net | (x[n + i] ^ 0xFF)
        want := p[i] & p[n + i]
        if (net & p[n + i]) != want || (last & p[n + i]) != want {
            return false
        }
    }
    return true
}

// HostSubset is whether every host base x holds is held by base p, under
// HostIn's reading.
HostSubset(p: []u8, x: []u8): bool {
    if p.Len() > 0 && p[0] == b'.' {
        return SuffixFold(p, x)
    }
    return SameFold(p, x)
}

// The orders and prune rules SortPrune takes.
const OrderDns: int = 0
const OrderHost: int = 1
const OrderIp: int = 2
const OrderBytes: int = 3
const OrderFold: int = 4

// Cmp is a against b in order k.
Cmp(k: int, a: []u8, b: []u8): int {
    if k == OrderIp {
        return IpCmp(a, b)
    }
    if k == OrderBytes {
        return ByteCmp(a, b)
    }
    if k == OrderFold {
        return FoldCmp(a, b)
    }
    return DnsCmp(a, b)
}

// Within is whether x lies in p (so x is pruned after p) in order k.
Within(k: int, p: []u8, x: []u8): bool {
    if k == OrderDns {
        return SuffixFold(p, x)
    }
    if k == OrderHost {
        return HostSubset(p, x)
    }
    if k == OrderIp {
        return IpSubset(p, x)
    }
    return Cmp(k, p, x) == 0
}

// SortPrune sorts the spans of src in xs by order k and drops each that
// lies within the one kept before it (Go's sortAndPrune); it returns how
// many are kept. O(k log k) by pdqsort, charged to budget; allocates
// nothing.
SortPrune(src: []u8, xs: mut []Span, k: int, budget: mut int): int {
    n := xs.Len()
    if n == 0 {
        return 0
    }
    *budget -= n * (Log2(n) + 2)
    xs.Sort(fn(p: &Span, q: &Span): bool {
        return Cmp(k, src[p.at..p.end], src[q.at..q.end]) < 0
    })
    var w := 1
    for r in 1..n {
        last := xs[w - 1]
        if !Within(k, src[last.at..last.end], src[xs[r].at..xs[r].end]) {
            xs[w] = xs[r]
            w += 1
        }
    }
    return w
}

// Below is the first index of xs whose base does not sort below name: by
// DnsCmp, IpAt or ByteCmp for order k. O(log n), charged to budget.
Below(src: []u8, xs: []Span, k: int, name: []u8, budget: mut int): int {
    *budget -= Log2(xs.Len()) + 2
    var lo := 0
    var hi := xs.Len()
    for lo < hi {
        mid := lo + (hi - lo) / 2
        b := src[xs[mid].at..xs[mid].end]
        c := if k == OrderIp { IpAt(b, name) } else { Cmp(k, b, name) }
        if c < 0 {
            lo = mid + 1
        } else {
            hi = mid
        }
    }
    return lo
}

// Holds is whether a base of xs, sorted and pruned by order k, holds name:
// the base equal to it, else its predecessor (Go's search).
Holds(src: []u8, xs: []Span, k: int, name: []u8, budget: mut int): bool {
    if xs.Len() == 0 {
        return false
    }
    i := Below(src, xs, k, name, budget)
    if i < xs.Len() {
        b := src[xs[i].at..xs[i].end]
        c := if k == OrderIp { IpAt(b, name) } else { Cmp(k, b, name) }
        if c == 0 {
            return true
        }
    }
    if i == 0 {
        return false
    }
    p := src[xs[i - 1].at..xs[i - 1].end]
    if k == OrderDns {
        return SuffixFold(p, name)
    }
    if k == OrderHost {
        return HostIn(p, name)
    }
    if k == OrderIp {
        return IpIn(p, name)
    }
    return false
}

// Forms are the flags of a Side.
struct Forms {
    dns_any: bool
    dns_all: bool
    ip_any: bool
    mail_any: bool
    mail_all: bool
    uri_any: bool
    uri_all: bool
}

// Counts are how many bases of each form a subtrees list holds.
struct Counts {
    dns: int
    ip: int
    mdom: int
    uri: int
    box: int
    box_bytes: int
}

// BuildSide reads a subtrees list (Parse has validated it) into a sorted,
// pruned Side in a: two walks, then one sort per form. O(k log k);
// allocates the span arrays and the exact mailboxes' keys in a; every
// element walked and every sort is charged to budget, which the caller
// checks.
BuildSide(a: &Arena, list: []u8, excluded: bool, budget: mut int): OutOfMemory!Side from a {
    var f := Forms(dns_any: false, dns_all: false, ip_any: false, mail_any: false, mail_all: false, uri_any: false, uri_all: false)
    var n := Counts(dns: 0, ip: 0, mdom: 0, uri: 0, box: 0, box_bytes: 0)
    var c := Cur(der: list, pos: 0, end: list.Len())
    for c.More() {
        st := c.Next() else {
            break
        }
        *budget -= 1
        var sc := Inside(list, &st)
        g := sc.Next() else {
            break
        }
        if g.class != 2 || g.cons {
            continue
        }
        b := Content(list, &g)
        if g.tag == NameDns {
            f.dns_any = true
            if b.Len() == 0 {
                f.dns_all = true
            } else {
                n.dns += 1
            }
        } else if g.tag == NameIp {
            f.ip_any = true
            n.ip += 1
        } else if g.tag == NameEmail {
            f.mail_any = true
            if b.Len() == 0 {
                f.mail_all = true
            } else if HasByte(b, b'@') {
                n.box += 1
                n.box_bytes += b.Len()
            } else {
                n.mdom += 1
            }
        } else if g.tag == NameUri {
            f.uri_any = true
            if b.Len() == 0 {
                f.uri_all = true
            } else {
                n.uri += 1
            }
        }
    }
    dns := try a.Slice[Span](n.dns)
    ip := try a.Slice[Span](n.ip)
    mdom := try a.Slice[Span](n.mdom)
    uri := try a.Slice[Span](n.uri)
    box := try a.Slice[Span](n.box)
    norm := try a.Slice[u8](n.box_bytes)
    var m := Counts(dns: 0, ip: 0, mdom: 0, uri: 0, box: 0, box_bytes: 0)
    c = Cur(der: list, pos: 0, end: list.Len())
    for c.More() {
        st := c.Next() else {
            break
        }
        *budget -= 1
        var sc := Inside(list, &st)
        g := sc.Next() else {
            break
        }
        if g.class != 2 || g.cons || g.end == g.at {
            continue
        }
        sp := Span(at: g.at, end: g.end)
        if g.tag == NameDns && m.dns < n.dns {
            dns[m.dns] = sp
            m.dns += 1
        } else if g.tag == NameIp && m.ip < n.ip {
            ip[m.ip] = sp
            m.ip += 1
        } else if g.tag == NameUri && m.uri < n.uri {
            uri[m.uri] = sp
            m.uri += 1
        } else if g.tag == NameEmail {
            b := Content(list, &g)
            if !HasByte(b, b'@') {
                if m.mdom < n.mdom {
                    mdom[m.mdom] = sp
                    m.mdom += 1
                }
            } else if m.box < n.box {
                at := m.box_bytes
                // Parse refused a constraint that is not a mailbox.
                bx := Mailbox2821(b, mut norm[at..at + b.Len()]) else {
                    continue
                }
                box[m.box] = Span(at: at, end: at + bx.n)
                m.box += 1
                m.box_bytes += b.Len()
            }
        }
    }
    nd := SortPrune(list, mut dns[0..m.dns], OrderDns, budget)
    ni := SortPrune(list, mut ip[0..m.ip], OrderIp, budget)
    nm := SortPrune(list, mut mdom[0..m.mdom], OrderHost, budget)
    nu := SortPrune(list, mut uri[0..m.uri], OrderHost, budget)
    nb := SortPrune(norm[..], mut box[0..m.box], OrderBytes, budget)
    // Go's parentConstraints, excluded side only: each kept DNS base from
    // its first '.', for a wildcard name whose parent is the base's.
    par := try a.Slice[Span](if excluded { nd } else { 0 })
    var np := 0
    for i in 0..par.Len() {
        d := dns[i]
        p := Parent(list[d.at..d.end])
        if p.Len() > 0 {
            par[np] = Span(at: d.end - p.Len(), end: d.end)
            np += 1
        }
    }
    np = SortPrune(list, mut par[0..np], OrderFold, budget)
    return Side(dns_any: f.dns_any, dns_all: f.dns_all, ip_any: f.ip_any, mail_any: f.mail_any, mail_all: f.mail_all, uri_any: f.uri_any, uri_all: f.uri_all, dns: dns[0..nd], par: par[0..np], ip: ip[0..ni], mdom: mdom[0..nm], uri: uri[0..nu], box: box[0..nb], norm: norm[..])
}

// DnsHeld is whether side s holds DNS name d: an empty base, a base d is
// within, or, for an excluded side, a wildcard d whose parent is a base's.
DnsHeld(list: []u8, s: &Side, d: []u8, excluded: bool, budget: mut int): bool {
    if s.dns_all || Holds(list, s.dns, OrderDns, d, budget) {
        return true
    }
    return excluded && d.Len() > 0 && d[0] == b'*' && Holds(list, s.par, OrderFold, Parent(d), budget)
}

// MailHeld is whether side s holds the mailbox whose key is key and whose
// domain is domain.
MailHeld(list: []u8, s: &Side, key: []u8, domain: []u8, budget: mut int): bool {
    if s.mail_all || Holds(s.norm, s.box, OrderBytes, key, budget) {
        return true
    }
    return Holds(list, s.mdom, OrderHost, domain, budget)
}

// UriHeld is whether side s holds the URI host h.
UriHeld(list: []u8, s: &Side, h: []u8, budget: mut int): bool {
    return s.uri_all || Holds(list, s.uri, OrderHost, h, budget)
}
