// Why: every codec of std.encoding fails in the same few ways, so they
// share one error set.
//
// What: BadInput, UnknownLabel, Unsupported, TooLarge and Error, their
// union. The codecs are one file each: base64.mk, hex.mk, percent.mk, the
// WHATWG Encoding Standard in text.mk, labels.mk, single.mk and
// single_tables.mk, codec.mk's Codec for std.encoding.cjk's multi-byte
// encodings, der.mk (X.690 DER) and pem.mk (RFC 7468).
//
// How: every decoder reads untrusted bytes: it never panics on input,
// recurses nowhere, and holds memory bounded by its input.

/// BadInput is input that is not in the format, found wrong at byte offset:
/// the first byte that cannot be there, or the input's length when it ends
/// too early; each function's line says which byte it names.
export error BadInput(offset: int)

/// UnknownLabel is a label the Encoding Standard does not have.
export error UnknownLabel

/// Unsupported is an encoding the standard has and this module does not
/// build (the multi-byte CJK set, which std.encoding.cjk builds), or an
/// operation an encoding lacks.
export error Unsupported

/// TooLarge is output that does not fit where it must go, or input past a
/// stated bound (DER's depth of 32).
export error TooLarge

/// Error is every failure of std.encoding.
export open type Error = BadInput | UnknownLabel | Unsupported | TooLarge
