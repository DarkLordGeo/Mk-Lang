// Why: the domain parser needs UTS 46 for non-ASCII domains, and keeping its
// two calls in one small file lets IDNA be removed by hand.
//
// What: IdnaAsciiAllowed and IdnaToAscii, the two calls host.mk makes into
// UTS 46 (section 3.3 of the URL Standard), here with IDNA built
// (idna_uts46.mk).
//
// How: removing IDNA deletes idna_*.mk and replaces this file with its
// refusal form, the text std/std-url_harness_test.go holds as urlCutSeam.

// IdnaAsciiAllowed is whether an ASCII domain may be used as it is: always,
// since the domain parser lower-cases an ASCII domain whatever ToASCII
// says (step 5).
IdnaAsciiAllowed(domain: []u8): bool {
    _ = domain
    return true
}

// IdnaToAscii is the domain parser's step 6, UTS 46's ToASCII with
// beStrict false, on a domain that is not ASCII. Fails with BadHost where
// ToASCII fails.
IdnaToAscii(a: &Arena, domain: str): Error|OutOfMemory![]u8 from a {
    r := try Uts46ToAscii(a, domain, false)
    ascii := r else {
        fail BadHost
    }
    return ascii.AsBytes()
}
