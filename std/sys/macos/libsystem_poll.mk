// Why: the kqueue poller and the socket layer call libSystem functions that
// libsystem.mk does not declare, and every function std.sys calls must be
// declared once, with one Mk signature.
//
// What: the externs of the poller and the sockets: getrlimit, setrlimit and
// sysctlbyname (RaiseFdLimit) and waitid (a child peek that does not
// reap); recvmsg, which the datagram receive also calls, is in
// libsystem_misc.mk.
//
// How: the conventions of libsystem.mk: C int is i32, size_t uint, ssize_t
// int, id_t u32, a pointer *u8. poll(2) folds onto std.sys's names, so it
// is in std.sys.lc (lc_poll.mk). Every name is in libSystem.def.

extern getrlimit(resource: i32, rlp: *u8): i32
extern setrlimit(resource: i32, rlp: *u8): i32
extern sysctlbyname(name: *u8, oldp: *u8, oldlenp: *uint, newp: *u8, newlen: uint): i32
extern waitid(idtype: i32, id: u32, info: *u8, options: i32): i32
