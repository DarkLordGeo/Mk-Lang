// Why: std.time's failures are shared by its readers, its zones and std's
// timeouts elsewhere, so they are declared once.
//
// What: TimedOut, BadName, BadData, NotFound, NoDatabase, Skipped, Repeated,
// OutOfRange and the open Error set.
//
// How: plain error declarations; TimedOut is also a member of net.Error.

/// A deadline passed: std's own timeouts (an HTTP client's connect,
/// response-header and idle limits, a server's request-header and
/// keep-alive limits, a TLS handshake, a Close that speaks to the peer)
/// report it, never a bare Canceled. A member of net.Error, and
/// through it of tls.Error and websocket.Error.
export error TimedOut

/// A zone name that is not one or more segments of ASCII letters, digits,
/// '.', '_', '+' and '-' joined by '/', with no segment "." or "..", at most
/// 255 bytes; checked before any file is opened.
export error BadName

/// Zone data that is not TZif (RFC 8536 versions 1 to 4) with a valid POSIX
/// footer, a zone file over 1 MiB, or one that could not be read.
export error BadData

/// No zone of that name in any place Zone.Load looked.
export error NotFound

/// Zone.Load had nowhere to look: TZDIR unset or not a directory, no
/// /usr/share/zoneinfo directory (or a target other than Linux), and no
/// embedded Database.
export error NoDatabase

/// A local time that a transition skipped: no instant of the zone shows it.
export error Skipped

/// A local time that a transition repeated: two instants of the zone show it.
export error Repeated

/// A field outside its range (month 1 to 12, day 1 to the month's length,
/// hour 0 to 23, minute and second 0 to 59, nanos 0 to 999,999,999), or an
/// instant outside Time's range.
export error OutOfRange

/// Every way std.time's zone and calendar functions fail.
export open type Error = BadName | BadData | NotFound | NoDatabase | Skipped | Repeated | OutOfRange | Canceled
