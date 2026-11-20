// Why: the file-system calls beyond plain files (call.mk, dir.mk,
// removeat.mk) need libSystem functions that libsystem.mk does not
// declare, and each must be declared once, with one Mk signature.
//
// What: openat, fstatat, fstatat64, unlinkat, flock and utimensat, as
// bodiless externs, each named in libSystem.def.
//
// How: the conventions of libsystem.mk: C int is i32, a pointer *u8 (or
// *i64 for an array of struct timespec). openat is variadic in libSystem,
// so it is declared with `...` and its mode, when passed, is a C int. On
// x86_64 the 64-bit-inode fstatat is fstatat64; on arm64 fstatat has only
// that layout. Private to std.sys.

extern flock(fd: i32, op: i32): i32
extern fstatat(fd: i32, path: *u8, st: *u8, flags: i32): i32
extern fstatat64(fd: i32, path: *u8, st: *u8, flags: i32): i32
extern openat(fd: i32, path: *u8, flags: i32, ...): i32
extern unlinkat(fd: i32, path: *u8, flags: i32): i32
extern utimensat(fd: i32, path: *u8, times: *i64, flags: i32): i32
