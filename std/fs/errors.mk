// Why: every call of std.fs fails with members of one error set, so a
// caller can match on what went wrong without knowing the OS.
// What: NotFound, Exists, Denied, IsDirectory, NotDirectory, InvalidPath,
// NoSpace, Unsupported, Os(code) and their union Error.
// How: every member is payload-free except Os; Utf8Error and Canceled are
// the prelude's.

/// No file or directory at the path.
export error NotFound
/// Something is already at the path.
export error Exists
/// The OS refused access.
export error Denied
/// The path is a directory where a file is needed.
export error IsDirectory
/// A component of the path, or the path itself, is not a directory.
export error NotDirectory
/// The device is full.
export error NoSpace
/// The path cannot name a file on this OS.
export error InvalidPath
/// Any other OS failure, with the OS's error code. The code is 0 when an
/// argument is out of range (a negative position or length) or the runtime
/// broke its contract (a count out of range, a write with no progress).
export error Os(code: i64)
/// An operation this target or this Io cannot do: a symbolic link without
/// the privilege on Windows, an Options set no target opens alike, a Copy
/// of something that is not a regular file, a WriteAt on a file opened
/// under append, or a call a simulated Io has no file system for.
export error Unsupported

/// Every way an fs operation can fail. The set is open: it may gain
/// members, so a match on it outside std needs a `_` arm.
export open type Error = NotFound | Exists | Denied | IsDirectory | NotDirectory | NoSpace | InvalidPath | Utf8Error | Os | Canceled | Unsupported
