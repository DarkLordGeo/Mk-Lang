// Why: commands take numbers that must be checked against their range.
//
// What: Number, HexDigit and the NotANumber and OutOfBounds errors.
//
// How: decimal, or hexadecimal after 0x, each checked against the range its
// command accepts.

/// The word is not a decimal or 0x-prefixed hexadecimal number.
export error NotANumber
/// The number is outside the range the command takes.
export error OutOfBounds(value: u64, low: u64, high: u64)

/// Why a word is not a number a command takes.
export type NumberError = NotANumber | OutOfBounds

HexDigit(b: u8): ?u64 {
    if b >= 0x30 && b <= 0x39 {
        return some(u64(b - 0x30))
    }
    if b >= 0x61 && b <= 0x66 {
        return some(u64(b - 0x61 + 10))
    }
    if b >= 0x41 && b <= 0x46 {
        return some(u64(b - 0x41 + 10))
    }
    return none
}

/// Reads `word` as a decimal number, or as hexadecimal after `0x`, and
/// checks that it lies in [low, high]. O(len(word)).
export Number(word: str, low: u64, high: u64): NumberError!u64 {
    var value: u64 = 0
    if word.StartsWith("0x") || word.StartsWith("0X") {
        digits := word[2..]
        if digits.Len() == 0 || digits.Len() > 16 {
            fail NotANumber
        }
        for b in digits.AsBytes() {
            d := HexDigit(b) else {
                fail NotANumber
            }
            value = value << 4 | d
        }
    } else {
        n := word.ParseInt() else {
            fail NotANumber
        }
        if n < 0 {
            fail NotANumber
        }
        value = Cast[u64](n)
    }
    if value < low || value > high {
        fail OutOfBounds(value: value, low: low, high: high)
    }
    return value
}
