// Why: every std module that depends on the build's target (SIMD width,
// OS) needs one compile-time fact it can read without importing anything.
//
// What: TargetInfo, Arch and Os. Target, the compile-time constant of type
// TargetInfo, is made by the compiler for the build's target.
//
// How: the variants are matched by name, so these spellings are part of the
// contract with the driver.

// std.target is layer 0's lowest module: it imports nothing, so every std
// module may import it, std.simd below std.core included, and read the one
// compile-time fact Target.os. It is std-internal; the prelude offers
// TargetInfo, Arch and Os through std.core's aliases (core_target.mk), and
// Target is a universe name.

/// What the build's target is: its architecture, OS, pointer size and SIMD
/// width. Target, the compile-time constant of this type, is made by the
/// compiler.
export struct TargetInfo {
    export arch: Arch
    export os: Os
    export pointer_bytes: int
    // vector_bits is the target's fixed SIMD register width in bits, 128 on
    // x86_64 and aarch64, never from --cpu.
    export vector_bits: int
}

/// The target's architecture. The spellings are part of the contract with the
/// driver, which matches them by name.
export open enum Arch {
    X64
    Arm64
    Riscv64
}

/// The target's operating system, or Freestanding for none. The spellings are
/// part of the contract with the driver.
export open enum Os {
    Linux
    Windows
    Macos
    Freestanding
    // Nutshell is the x86_64-nutshell and aarch64-nutshell targets.
    Nutshell
}
