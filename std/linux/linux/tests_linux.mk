// Why: the raw system call must hand back the kernel's result and errno
// unchanged.
// What: tests of Syscall: getpid succeeds, close of a bad fd is EBADF, an
// unknown number is ENOSYS.
// How: run on x86_64-linux only (`mk test --std=std.linux
// --target=x86_64-linux`).

test "Linux Syscall getpid is positive with errno 0" {
    v := Syscall(39, 0, 0, 0, 0, 0, 0)
    Assert(v.errno == 0)
    Assert(v.r > 0 && v.r < 1 << 22)
}

test "Linux Syscall close of a bad fd is EBADF" {
    v := Syscall(3, 0xFFFF_FFFF, 0, 0, 0, 0, 0)
    Assert(v.errno == 9)
    Assert(v.r == 0xFFFF_FFFF_FFFF_FFF7)
}

test "Linux Syscall of an unknown number is ENOSYS" {
    v := Syscall(100000, 0, 0, 0, 0, 0, 0)
    Assert(v.errno == 38)
}
