// Why: big integers are read from and written to text in any base from 2 to
// 36, and parsing untrusted text must have bounded cost.
//
// What: big.Parse, Int.Text and the chunk helpers Chunk, DigitValue and
// PutDigits.
//
// How: a base that is a power of two takes bits directly; any other converts
// a word's worth of digits at a time (base^k, the largest power below 2^64),
// as Go's nat.scan and nat.utoa do, and long values divide and conquer
// (parsedc.mk, textdc.mk). Parse decides TooLarge from the text's length
// before it reads a digit or allocates, so its memory is bounded by
// max_digits, and it never panics on input.

import std.raw

// Digits are the digit characters, lower case.
const Digits: str = "0123456789abcdefghijklmnopqrstuvwxyz"

// Log2Base is ceil(log2(b) * 1024) for each base b from 0 to 36 (0 and 1
// unused): Parse's words for n digits are at most n * Log2Base[b] / 65536
// + 1 (rounded up).
const Log2Base: [37]int = [0, 0, 1024, 1624, 2048, 2378, 2648, 2875, 3072, 3247, 3402, 3543, 3672, 3790, 3899, 4001, 4096, 4186, 4271, 4350, 4426, 4498, 4567, 4633, 4696, 4756, 4814, 4870, 4923, 4975, 5025, 5074, 5120, 5166, 5210, 5253, 5295]

// FormatWords bounds the values Format writes in decimal: its scratch is
// on the stack, two arrays of this many words.
const FormatWords: int = 512

// Chunk is the largest power of base below 2^64, and its exponent.
Chunk(base: u64): (k: int, bb: u64) {
    var bb := base
    var k := 1
    for bb <= 0xFFFFFFFFFFFFFFFF / base {
        bb = bb * base
        k += 1
    }
    return (k, bb)
}

// DigitValue is c's value as a digit in any base up to 36, or 99.
DigitValue(c: u8): u64 {
    if c >= 0x30 && c <= 0x39 {
        return u64(c - 0x30)
    }
    if c >= 0x61 && c <= 0x7A {
        return u64(c - 0x61) + 10
    }
    if c >= 0x41 && c <= 0x5A {
        return u64(c - 0x41) + 10
    }
    return 99
}

/// Parses the integer s spells in base, 2 to 36. The text is an optional +
/// or -, then one or more digits, with letters in either case for the
/// digits 10 to 35. Nothing else is allowed: no space, no _ and no prefix
/// such as 0x.
///
/// Errors, checked in this order: ParseError when base is outside 2 to 36
/// or there are no digits; TooLarge when s has more than max_digits digits,
/// decided from its length before any digit is read or any word allocated;
/// ParseError for a character that is not a digit in base; OutOfMemory when
/// a cannot give the words. Never panics on input, so max_digits bounds the
/// time and memory untrusted text can cost.
///
/// Cost for d digits and n = d log2(base) / 64 words: O(d) for a base that
/// is a power of two, else O(d^2) up to 20,000 digits and O(M(n) log n) word
/// operations above, M being the product's cost (Karatsuba, Toom-3). It
/// allocates n + 2 words in a, and above 20,000 digits at most about 14n
/// scratch words (the powers of base it multiplies by, and one product's
/// scratch) in a sub-arena of a that it frees before returning. Its
/// recursion is at most log2(n) levels deep.
export Parse(a: &Arena, s: str, base: int, max_digits: int): Error!Int from a {
    if base < 2 || base > 36 {
        fail ParseError
    }
    b := s.AsBytes()
    var i := 0
    var neg := false
    if b.Len() > 0 && (b[0] == 0x2B || b[0] == 0x2D) {
        neg = b[0] == 0x2D
        i = 1
    }
    nd := b.Len() - i
    if nd == 0 {
        fail ParseError
    }
    if nd > max_digits {
        fail TooLarge
    }
    ub := u64(Trunc[u32](base))
    for j in i..b.Len() {
        if DigitValue(b[j]) >= ub {
            fail ParseError
        }
    }
    z := try a.Slice[u64](WordsFor(nd, base))
    d := b[i..]
    if ub & (ub - 1) == 0 {
        ParseBits(mut z[..], d, 63 - raw.Clz64(ub))
    } else if nd > ParseLeafDigits {
        try ParseLarge(a, mut z[..], d, base, ub)
    } else {
        (k, bb: _) := Chunk(ub)
        ParseHorner(mut z[..], d, ub, k)
    }
    return Make(z[..], neg)
}

// PutDigits writes r's digits in base into buf, ending before pos, exactly
// width of them (leading zeros included) when full is true, else until r
// is zero; it returns the new start.
PutDigits(buf: mut []u8, var pos: int, var r: u64, base: u64, width: int, full: bool): int {
    if base == 10 {
        // A constant divisor, so the division is a multiplication.
        for k in 0..width {
            if !full && r == 0 && k > 0 {
                break
            }
            pos -= 1
            buf[pos] = Digits.Byte(Cast[int](r % 10))
            r = r / 10
        }
        return pos
    }
    for k in 0..width {
        if !full && r == 0 && k > 0 {
            break
        }
        pos -= 1
        buf[pos] = Digits.Byte(Cast[int](r % base))
        r = r / base
    }
    return pos
}

/// Writes x as text in base, 2 to 36: lower-case digits, a - when x is
/// negative, no prefix, and "0" for zero. It writes a value of any size.
/// Panics when base is outside 2 to 36. Fails with OutOfMemory when a
/// cannot give the words.
///
/// O(n) for a base that is a power of two, else O(M(n) log^2 n) word
/// operations for n words, M being Karatsuba's cost: above 30 words it
/// divides and conquers over DivMod's divisions, below it divides by one
/// word per run of digits. A caller holding untrusted input bounds n. It
/// allocates the text, at most n * 64 / floor(log2 base) + 2 bytes, in a,
/// with n scratch words in a up to 30 words and, above, O(n log n) scratch
/// words in a sub-arena that it frees before returning.
export Int.Text(&self, a: &Arena, base: int): OutOfMemory!str from a {
    if base < 2 || base > 36 {
        Panic("big.Int.Text: base outside 2 to 36")
    }
    x := self.mag
    n := x.Len()
    if n == 0 {
        return try a.Dup("0")
    }
    ub := u64(Trunc[u32](base))
    lg := 63 - raw.Clz64(ub)
    size := self.BitLen() / lg + 2
    buf := try a.Slice[u8](size)
    var pos := size
    if ub & (ub - 1) == 0 {
        // A power of two: lg bits a digit, from the lowest.
        bits := self.BitLen()
        var at := 0
        for at < bits {
            w := at >> 6
            s := at & 63
            var d := x[w] >> s
            if s + lg > 64 && w + 1 < n {
                d = d | (x[w + 1] << (64 - s))
            }
            pos -= 1
            buf[pos] = Digits.Byte(Cast[int](d & (ub - 1)))
            at += lg
        }
    } else if n > TextLeafWords {
        pos = try TextLarge(a, mut buf[..], x, ub)
    } else {
        work := try a.Slice[u64](n)
        Copy(mut work[..], x)
        var len := n
        (k, bb) := Chunk(ub)
        for len > 0 {
            r := DivInPlace(mut work[..len], bb)
            for len > 0 && work[len - 1] == 0 {
                len -= 1
            }
            pos = PutDigits(mut buf[..], pos, r, ub, k, len > 0)
        }
    }
    if self.negative {
        pos -= 1
        buf[pos] = 0x2D
    }
    // Every byte written is an ASCII digit or '-', so the text is UTF-8.
    return str.From(buf[pos..]).Must()
}

impl Format for Int {
    /// Writes x in decimal, as Text(a, 10) does, when its magnitude has at
    /// most 512 words (about 9,800 digits), and above that in hexadecimal
    /// with a 0x prefix. Format has no allocator, and decimal needs scratch
    /// the size of the value; call Text for decimal at any size. Fails with
    /// the Writer's WriteError. O(n^2) word operations; it allocates nothing
    /// (8 KiB of stack).
    Format(&self, w: mut Writer): WriteError!void {
        x := self.mag
        n := x.Len()
        if self.negative {
            try w.Str("-")
        }
        if n == 0 {
            try w.Str("0")
            return
        }
        if n > FormatWords {
            try w.Str("0x")
            top := x[n - 1]
            try Print(w, "{top:x}", (top))
            var i := n - 2
            for i >= 0 {
                word := x[i]
                try Print(w, "{word:016x}", (word))
                i -= 1
            }
            return
        }
        // 10^19 is the largest power of ten below 2^64; each division
        // leaves a chunk of 19 digits, kept until the top one is known.
        var work: [FormatWords]u64 = [0; FormatWords]
        var chunks: [528]u64 = [0; 528]
        for i in 0..n {
            work[i] = x[i]
        }
        var len := n
        var count := 0
        for len > 0 {
            chunks[count] = DivInPlace(mut work[..len], 10_000_000_000_000_000_000)
            count += 1
            for len > 0 && work[len - 1] == 0 {
                len -= 1
            }
        }
        top := chunks[count - 1]
        try Print(w, "{top}", (top))
        var i := count - 2
        for i >= 0 {
            c := chunks[i]
            try Print(w, "{c:019}", (c))
            i -= 1
        }
    }
}
