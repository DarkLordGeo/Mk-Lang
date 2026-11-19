// Why: code that branches on the target (Target.os, Os.Windows, a
// `.Freestanding` pattern) must find these names in the prelude, while the
// types themselves live in std.target, the module below std.core and
// std.simd.
//
// What: the aliases TargetInfo, Arch and Os.
//
// How: each is a type alias of the std.target declaration
// (std/target/target.mk), so a prelude name and the std.target name are the
// same type.

import std.target

/// The build's target, as the compile-time constant Target: arch (Arch), os
/// (Os), pointer_bytes and vector_bits (128 on x86_64 and aarch64). Each is
/// fixed by the target, never by --cpu.
export type TargetInfo = target.TargetInfo

/// The target's architecture: X64, Arm64 or Riscv64. The enum is open, so a
/// match on it needs a `_` arm.
export type Arch = target.Arch

/// The target's operating system: Linux, Windows, Macos, Freestanding or
/// Nutshell. The enum is open, so a match on it needs a `_` arm.
export type Os = target.Os
