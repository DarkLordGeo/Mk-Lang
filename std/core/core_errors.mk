// Why: the prelude's own operations fail in a few common ways, and every
// module should name those failures the same way.
//
// What: the prelude errors OutOfMemory, Canceled, Closed, ReadError,
// WriteError, ParseError and Utf8Error.
//
// How: each is a payload-free error declaration: the value names the
// failure and carries nothing else.

/// An allocator or arena could not supply the memory a call needed.
export error OutOfMemory
/// The operation's scope was canceled, before it started or while it waited.
export error Canceled
/// The channel or stream was closed.
export error Closed
/// A read failed, or a line did not fit its buffer.
export error ReadError
/// A write or flush failed.
export error WriteError
/// The text is not a number of the requested type, or the number is out of its
/// range.
export error ParseError
/// The bytes are not well-formed UTF-8.
export error Utf8Error
