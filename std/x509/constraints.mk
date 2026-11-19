// Why: a CA's name constraints (RFC 5280 4.2.1.10) limit which names the
// certificates below it may carry; a verifier that skips them trusts a
// constrained CA with every name.
//
// What: PathNames, the check over a whole path; IssuerNames, one issuer's
// constraints; Constrained, one certificate's names; HostIn, IpIn,
// SuffixFold, Parent and the budget MaxComparisons.
//
// How: as Go's crypto/x509 applies them (constraints.go), on a complete
// path only, each issuer's constraints bind the subjectAltName names of
// every certificate below it, except a self-issued one other than the leaf
// (RFC 5280 6.1.3 (b)). The matching rules follow.

// A name of a form the issuer constrains must fall in some permitted
// subtree of that form and in no excluded one. DNS names match a constraint
// that is a suffix at a label boundary, or any subdomain of a constraint
// with a leading dot; an excluded constraint also catches a wildcard name
// whose parent is the constraint's parent. The hosts of URIs and the
// domains of email addresses match a constraint exactly, or any host below
// one with a leading dot (RFC 5280's reading, where Go reads them as DNS
// names); an exact mailbox constraint matches the mailbox, its local part
// unquoted (mailbox.mk). IP addresses match a range of their own family.
// The subtrees are sorted and pruned once per issuer and each name is found
// by binary search (nameset.mk), so the check is O((k + names) log k), and
// all of it is charged to one budget per Verify, past which Verify fails
// with TooComplex.

// MaxComparisons is the budget of constraint work for one Verify: one
// unit per element walked, n (log2 n + 2) per sort of n subtrees and
// log2 n + 2 per search.
const MaxComparisons: int = 1_048_576

// The outcomes of a check.
const NameOk: int = 0
const NameBad: int = 1
const NameTooMany: int = 2

// SuffixFold is Go's dnsHasSuffix: whether constraint is a suffix of name
// ignoring ASCII case, at a label boundary unless constraint starts with
// '.'; the empty constraint matches every name.
SuffixFold(constraint: []u8, name: []u8): bool {
    lc := constraint.Len()
    ln := name.Len()
    if lc == 0 {
        return true
    }
    if lc > ln {
        return false
    }
    for i in 0..lc {
        if Lower(constraint[i]) != Lower(name[ln - lc + i]) {
            return false
        }
    }
    return constraint[0] == b'.' || ln == lc || name[ln - lc - 1] == b'.'
}

// Parent is s from its first '.' on, empty when s has none.
Parent(s: []u8): []u8 from s {
    for i in 0..s.Len() {
        if s[i] == b'.' {
            return s[i..]
        }
    }
    return s[0..0]
}

// IpIn is whether address a (4 or 16 bytes) is in range r (8 or 32: an
// address and its mask), of the same family only.
IpIn(r: []u8, a: []u8): bool {
    if r.Len() != 2 * a.Len() {
        return false
    }
    n := a.Len()
    for i in 0..n {
        if (a[i] & r[n + i]) != (r[i] & r[n + i]) {
            return false
        }
    }
    return true
}

// HostIn is whether host matches the email or URI constraint c (not
// empty): RFC 5280 4.2.1.10 makes a constraint with a leading '.' any host
// below it, and one without exactly that host.
HostIn(c: []u8, host: []u8): bool {
    if c.Len() > 0 && c[0] == b'.' {
        return host.Len() > c.Len() && SuffixFold(c, host)
    }
    return SameFold(c, host)
}

// Constrained checks every subjectAltName name of x against an issuer's
// sides p (permitted, over plist) and e (excluded, over elist): NameOk,
// NameBad (a name outside them, or one that cannot be matched: a DNS name
// or a domain Go's domainNameValid refuses, an email address that is not
// an RFC 2821 mailbox, a URI whose host UriHost does not give or that is
// an IP address; under an issuer with no URI constraint, only a URI with
// no host or an IP host, as Go), or NameTooMany when the budget ran out. O(names x log k);
// allocates one buffer of the SAN's length in a for the mailboxes' keys.
Constrained(a: &Arena, x: &Certificate, p: &Side, plist: []u8, e: &Side, elist: []u8, budget: mut int): OutOfMemory!int {
    san := x.San()
    key := try a.Slice[u8](san.Len())
    var c := Cur(der: san, pos: 0, end: san.Len())
    for c.More() {
        g := c.Next() else {
            return NameBad
        }
        *budget -= 1
        if *budget < 0 {
            return NameTooMany
        }
        if g.class != 2 || g.cons {
            continue
        }
        b := Content(san, &g)
        var bad := false
        if g.tag == NameDns {
            if !DomainValid(b, false) {
                return NameBad
            }
            bad = (p.dns_any && !DnsHeld(plist, p, b, false, budget)) || (e.dns_any && DnsHeld(elist, e, b, true, budget))
        } else if g.tag == NameIp {
            bad = (p.ip_any && !Holds(plist, p.ip, OrderIp, b, budget)) || (e.ip_any && Holds(elist, e.ip, OrderIp, b, budget))
        } else if g.tag == NameEmail {
            bx := Mailbox2821(b, mut key[0..b.Len()]) else {
                return NameBad
            }
            k := key[0..bx.n]
            d := b[bx.at..]
            bad = (p.mail_any && !MailHeld(plist, p, k, d, budget)) || (e.mail_any && MailHeld(elist, e, k, d, budget))
        } else if g.tag == NameUri {
            if p.uri_any || e.uri_any {
                h := UriHost(b) else {
                    return NameBad
                }
                if IpOf(h).IsSome() || !DomainValid(h, false) {
                    return NameBad
                }
                bad = (p.uri_any && !UriHeld(plist, p, h, budget)) || (e.uri_any && UriHeld(elist, e, h, budget))
            } else if !UriNamed(b) {
                // Go's parseURIs: under any constraint, a URI with no
                // host or an IP host cannot be matched.
                return NameBad
            }
        }
        if *budget < 0 {
            return NameTooMany
        }
        if bad {
            return NameBad
        }
    }
    return NameOk
}

// IssuerNames checks the constraints of path[i] against the certificates
// below it in path (ids into chain and pool, the leaf first), in a
// sub-arena of a freed on return. O((k + names) log k).
IssuerNames(a: &Arena, chain: []Certificate, pool: []Certificate, path: []int, i: int, budget: mut int): OutOfMemory!int {
    ca := Node(chain, pool, path[i])
    var s := try a.Sub()
    defer s.Free()
    plist := ca.Permitted()
    elist := ca.Excluded()
    p := try BuildSide(&s, plist, false, budget)
    e := try BuildSide(&s, elist, true, budget)
    if *budget < 0 {
        return NameTooMany
    }
    for k in 0..i {
        below := Node(chain, pool, path[k])
        // RFC 5280 6.1.3 (b): a self-issued certificate other than the
        // leaf is not held to the constraints above it.
        if k > 0 && Same(below.SubjectDer(), below.IssuerDer()) {
            continue
        }
        r := try Constrained(&s, below, &p, plist, &e, elist, budget)
        if r != NameOk {
            return r
        }
    }
    return NameOk
}

// PathNames checks every issuer's name constraints over a complete path
// (ids into chain and pool, the leaf first and the root last): NameOk,
// NameBad or NameTooMany.
PathNames(a: &Arena, chain: []Certificate, pool: []Certificate, path: []int, budget: mut int): OutOfMemory!int {
    for i in 1..path.Len() {
        if Node(chain, pool, path[i]).Has(FlagConstraints) {
            r := try IssuerNames(a, chain, pool, path, i, budget)
            if r != NameOk {
                return r
            }
        }
    }
    return NameOk
}
