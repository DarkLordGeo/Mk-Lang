// Why: macOS starts a child process through posix_spawn, a single system
// call that libSystem wraps with its file-action and attribute objects;
// fork is unsafe in a threaded process, and libSystem is the only stable
// way to the kernel.
//
// What: the libSystem functions proc.mk and child.mk call beyond
// libsystem.mk's: posix_spawn with its file actions and attributes, and
// faccessat for the PATH search.
//
// How: posix_spawn_file_actions_t and posix_spawnattr_t are each one
// pointer on Darwin (<spawn.h>: typedef void *), so the caller passes the
// address of a u64 its init call fills. Every posix_spawn* call returns the
// error number itself, 0 on success, and leaves errno alone. C short and
// mode_t parameters are declared i32: Apple's arm64 convention has the
// caller extend a small integer to 32 bits, which an i32 of the same value
// is, and x86_64 reads only the low bits. Each name is listed in
// libSystem.def.

extern faccessat(fd: i32, path: *u8, mode: i32, flag: i32): i32
extern posix_spawn(pid: *i32, path: *u8, actions: *u8, attr: *u8, argv: *u8, envp: *u8): i32
extern posix_spawn_file_actions_addchdir_np(actions: *u8, path: *u8): i32
extern posix_spawn_file_actions_adddup2(actions: *u8, fd: i32, to: i32): i32
extern posix_spawn_file_actions_addinherit_np(actions: *u8, fd: i32): i32
extern posix_spawn_file_actions_addopen(actions: *u8, fd: i32, path: *u8, flags: i32, mode: i32): i32
extern posix_spawn_file_actions_destroy(actions: *u8): i32
extern posix_spawn_file_actions_init(actions: *u8): i32
extern posix_spawnattr_destroy(attr: *u8): i32
extern posix_spawnattr_init(attr: *u8): i32
extern posix_spawnattr_setflags(attr: *u8, flags: i32): i32
extern posix_spawnattr_setpgroup(attr: *u8, pgroup: i32): i32
extern posix_spawnattr_setsigdefault(attr: *u8, set: *u32): i32
extern posix_spawnattr_setsigmask(attr: *u8, set: *u32): i32
