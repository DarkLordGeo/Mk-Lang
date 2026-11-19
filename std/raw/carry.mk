// Why: multi-limb arithmetic (std.crypto's field code, std.big) must be one
// branch-free add-with-carry or subtract-with-borrow chain in every build.
// What: AddCarry64 and SubBorrow64.
// How: bodiless intrinsics: codegen lowers each to two overflowing adds or
// subtracts and eval runs its definition. A branch or select on the last
// flag may still become a compare and a jump per limb, or a cmov
// (std/crypto/crypto.mk's constant-time rules cover that). Safe, since they
// read only their arguments; O(1), allocates nothing.

/// AddCarry64 is x + y + c: sum is its low 64 bits, and carry is 1 when it
/// is 2^64 or more, else 0. c is meant to be 0 or 1, the carry of the limb
/// below. Safe. A chain of calls compiles to one add-with-carry chain
/// (adc on x86_64), with no branches, in every build. O(1); allocates
/// nothing.
export AddCarry64(x: u64, y: u64, c: u64): (sum: u64, carry: u64)
/// SubBorrow64 is x - y - b: diff is its value modulo 2^64, and borrow is 1
/// when x is less than y + b, else 0. b is meant to be 0 or 1, the borrow
/// of the limb below. Safe. A chain of calls compiles to one
/// subtract-with-borrow chain (sbb on x86_64), with no branches, in every
/// build. O(1); allocates nothing.
export SubBorrow64(x: u64, y: u64, b: u64): (diff: u64, borrow: u64)
