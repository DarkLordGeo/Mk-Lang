// Why: signals, the terminal, Unix-domain sockets, descriptor passing and
// the dynamic loader need libSystem functions that libsystem.mk does not
// declare, and every function std.sys calls is declared once.
//
// What: getpgrp, sendmsg, recvmsg, dlsym and dlerror, as bodiless externs,
// each named in libSystem.def; the poller's datagram receive calls recvmsg
// too.
//
// How: C int is i32, size_t uint, ssize_t int, pid_t i32, a pointer *u8;
// none is variadic. dlopen and dlclose fold onto std.sys's DlOpen and
// DlClose, so they are std.sys.lc's (lc_misc.mk). Private to std.sys.

extern dlerror(): *u8
extern dlsym(h: *u8, name: *u8): *u8
extern getpgrp(): i32
extern recvmsg(fd: i32, msg: *u8, flags: i32): int
extern sendmsg(fd: i32, msg: *u8, flags: i32): int
