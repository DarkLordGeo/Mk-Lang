// Why: Mk compares module-level names ignoring case and underscores, so
// libSystem's poll cannot be declared in std.sys beside its poller's names.
//
// What: the poll extern, private here, and its wrapper for std.sys,
// PollFds.
//
// How: the wrapper is the call and nothing more; errno is read by the
// caller through __error(), on the same thread, right after. The name is
// in std/sys/macos/libSystem.def.

extern poll(fds: *u8, nfds: u32, timeout: i32): i32

/// PollFds is poll(2) of nfds struct pollfd records (8 bytes each: fd,
/// events, revents) at fds, waiting at most timeout milliseconds (0: not at
/// all); the count of records with events, or -1 with errno set. Needs
/// unsafe: fds must hold nfds records.
/// O(nfds); allocates nothing.
export PollFds(fds: *u8, nfds: u32, timeout: i32): i32 {
    unsafe "the caller's fds holds nfds struct pollfd records" {
        return poll(fds, nfds, timeout)
    }
}
