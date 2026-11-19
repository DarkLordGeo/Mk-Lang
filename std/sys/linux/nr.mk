// Why: the system-call numbers differ between x86_64 and aarch64, and every
// call in std/sys/linux goes through them.
//
// What: the Nr table of numbers and Nrs(), the table for the build's
// Target.arch, and StatMode.
//
// How: Target.arch is constant per build, so the other table emits nothing.
// aarch64 has no epoll_wait, renameat or open, so both architectures use
// epoll_pwait, renameat2 and openat.

struct Nr {
    read: uint
    write: uint
    close: uint
    fstat: uint
    mmap: uint
    mprotect: uint
    munmap: uint
    madvise: uint
    rt_sigaction: uint
    sigaltstack: uint
    pread64: uint
    pwrite64: uint
    sched_yield: uint
    getpid: uint
    gettid: uint
    exit_group: uint
    fsync: uint
    futex: uint
    sched_getaffinity: uint
    clock_gettime: uint
    tgkill: uint
    openat: uint
    unlinkat: uint
    readlinkat: uint
    renameat2: uint
    epoll_create1: uint
    epoll_ctl: uint
    epoll_pwait: uint
    eventfd2: uint
    getrandom: uint
    io_uring_setup: uint
    io_uring_enter: uint
    socket: uint
    connect: uint
    accept4: uint
    sendto: uint
    recvfrom: uint
    shutdown: uint
    bind: uint
    listen: uint
    getsockname: uint
    getpeername: uint
    setsockopt: uint
    getsockopt: uint
    epoll_pwait2: uint
    getdents64: uint
    kill: uint
    prlimit64: uint
    statx: uint
    mkdirat: uint
    pipe2: uint
    dup3: uint
    execve: uint
    waitid: uint
    rt_sigprocmask: uint
    pidfd_send_signal: uint
    pidfd_open: uint
    fcntl: uint
    socketpair: uint
    wait4: uint
    ftruncate: uint
    nanosleep: uint
    // The calls behind call.mk's IoTable entries.
    ioctl: uint
    lseek: uint
    flock: uint
    fdatasync: uint
    getcwd: uint
    fchmodat: uint
    symlinkat: uint
    utimensat: uint
    copy_file_range: uint
    setpgid: uint
    setsid: uint
    fstatfs: uint
    openat2: uint
    clone3: uint
    chdir: uint
    close_range: uint
    ppoll: uint
    faccessat2: uint
    memfd_create: uint
}

const NrX64 = Nr(read: 0, write: 1, close: 3, fstat: 5, mmap: 9, mprotect: 10, munmap: 11, madvise: 28,
    rt_sigaction: 13, sigaltstack: 131, pread64: 17, pwrite64: 18, sched_yield: 24, getpid: 39, gettid: 186,
    exit_group: 231, fsync: 74, futex: 202, sched_getaffinity: 204, clock_gettime: 228, tgkill: 234, openat: 257,
    unlinkat: 263, readlinkat: 267, renameat2: 316, epoll_create1: 291, epoll_ctl: 233, epoll_pwait: 281,
    eventfd2: 290, getrandom: 318, io_uring_setup: 425, io_uring_enter: 426, socket: 41, connect: 42, accept4: 288,
    sendto: 44, recvfrom: 45, shutdown: 48, bind: 49, listen: 50, getsockname: 51, getpeername: 52, setsockopt: 54,
    getsockopt: 55, epoll_pwait2: 441, getdents64: 217, kill: 62, prlimit64: 302, statx: 332, mkdirat: 258,
    pipe2: 293, dup3: 292, execve: 59, waitid: 247, rt_sigprocmask: 14, pidfd_send_signal: 424, pidfd_open: 434,
    fcntl: 72, socketpair: 53, wait4: 61, ftruncate: 77, nanosleep: 35,
    ioctl: 16, lseek: 8, flock: 73, fdatasync: 75, getcwd: 79, fchmodat: 268, symlinkat: 266, utimensat: 280,
    copy_file_range: 326, setpgid: 109, setsid: 112, fstatfs: 138, openat2: 437, clone3: 435, chdir: 80, close_range: 436, ppoll: 271,
    faccessat2: 439, memfd_create: 319)

const NrA64 = Nr(read: 63, write: 64, close: 57, fstat: 80, mmap: 222, mprotect: 226, munmap: 215, madvise: 233,
    rt_sigaction: 134, sigaltstack: 132, pread64: 67, pwrite64: 68, sched_yield: 124, getpid: 172, gettid: 178,
    exit_group: 94, fsync: 82, futex: 98, sched_getaffinity: 123, clock_gettime: 113, tgkill: 131, openat: 56,
    unlinkat: 35, readlinkat: 78, renameat2: 276, epoll_create1: 20, epoll_ctl: 21, epoll_pwait: 22, eventfd2: 19,
    getrandom: 278, io_uring_setup: 425, io_uring_enter: 426, socket: 198, connect: 203, accept4: 242, sendto: 206,
    recvfrom: 207, shutdown: 210, bind: 200, listen: 201, getsockname: 204, getpeername: 205, setsockopt: 208,
    getsockopt: 209, epoll_pwait2: 441, getdents64: 61, kill: 129, prlimit64: 261, statx: 291, mkdirat: 34,
    pipe2: 59, dup3: 24, execve: 221, waitid: 95, rt_sigprocmask: 135, pidfd_send_signal: 424, pidfd_open: 434,
    fcntl: 25, socketpair: 199, wait4: 260, ftruncate: 46, nanosleep: 101,
    ioctl: 29, lseek: 62, flock: 32, fdatasync: 83, getcwd: 17, fchmodat: 53, symlinkat: 36, utimensat: 88,
    copy_file_range: 285, setpgid: 154, setsid: 157, fstatfs: 44, openat2: 437, clone3: 435, chdir: 49, close_range: 436, ppoll: 73,
    faccessat2: 439, memfd_create: 279)

// Nrs is the table of the build's architecture.
Nrs(): Nr {
    return if Target.arch == Arch.Arm64 { NrA64 } else { NrX64 }
}

// StatMode and StatSize are the offsets of st_mode and st_size in struct
// stat (x86_64: 24 and 48; aarch64: 16 and 48).
StatMode(): int {
    return if Target.arch == Arch.Arm64 { 16 } else { 24 }
}

const StatSize: int = 48
