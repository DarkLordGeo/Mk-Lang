// Why: a calculator error must point at the word that caused it.
//
// What: every error evaluating a line can return, and the Error union.
//
// How: each payload carries the column of the failing word.

/// An operation needed more values than the stack held.
export error StackUnderflow(column: int, need: int, have: int)
/// A divisor or modulus was zero.
export error DivisionByZero(column: int)
/// sqrt of a negative value.
export error NegativeSquareRoot(column: int)
/// inv found no inverse: the value and the modulus share a factor.
export error NoInverse(column: int)
/// A count (a shift, an exponent, a bit index, a base, a repeat count) was
/// negative, not an integer that fits, or past its limit.
export error CountOutOfRange(column: int)
/// A result would be longer than the run's bit limit.
export error ResultTooLarge(column: int, bits: int, limit: int)
/// A word that is neither a number nor an operation.
export error UnknownWord(column: int)
/// A number that does not parse.
export error BadNumber(column: int)
/// A number literal longer than the run's digit limit.
export error TooManyDigits(column: int, limit: int)
/// A word that needs a name after it (=name, $name) has none.
export error MissingName(column: int)
/// $name of a variable that was never stored.
export error NoSuchVariable(column: int)

/// Error is every way evaluating a line can fail.
export type Error = StackUnderflow | DivisionByZero | NegativeSquareRoot | NoInverse | CountOutOfRange | ResultTooLarge | UnknownWord | BadNumber | TooManyDigits | MissingName | NoSuchVariable | OutOfMemory | WriteError | Canceled
