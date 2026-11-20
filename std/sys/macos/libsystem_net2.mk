// Why: handle passing must tell a listening socket from a stream, and
// Darwin's getsockopt has no SO_ACCEPTCONN; every function std.sys calls
// must be declared once, with one Mk signature.
//
// What: proc_pidfdinfo, which describes one of a process's descriptors
// (passfd.mk reads a socket's options from it).
//
// How: the conventions of libsystem.mk: C int is i32, a pointer *u8. Every
// name is in libSystem.def.

extern proc_pidfdinfo(pid: i32, fd: i32, flavor: i32, buffer: *u8, size: i32): i32
