// Why: a freestanding program must format with Print before its heap works
// and inside its panic hook, where there is no Arena and no Io.
//
// What: Capped and Sink, the two public makers of a Writer that need no
// Arena and no Io. Layer 1 of std.
//
// How: each is std.core's, where the unsafe code that makes a Writer
// lives: Capped is a bounded target over a caller's buffer, and Sink is a
// Writer over a program's own device or state through a function it names
// (a stored callback). Neither allocates, and neither reaches a float, so
// a general-regs-only bin that uses them meets no E1108 here.

import std.core

/// A fixed buffer to write into that keeps what fits and counts what does
/// not. Make one over your buffer with `New`; `Writer` gives the Writer
/// that fills it, whose writes never fail; `Bytes` is what was kept,
/// `Dropped` how many bytes were not (saturating at the largest int), and
/// `Reset` empties it for the next line. Needs no Arena and no Io, and
/// allocates nothing.
export type Capped = core.Capped

/// Makes Writers over your own device or state of type S. `New(write)`
/// names the function that sends one run of bytes to it, and
/// `Writer(s, buf)` makes a Writer that holds s exclusively and buffers in
/// buf. Needs no Arena and no Io, and allocates nothing. New runs at
/// compile time and the result is plain, so a const can hold it.
export type Sink[S] = core.Sink[S]
