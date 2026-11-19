// Why: std.sys on Windows talks to the system through kernel32 alone, and
// every function it calls must be declared once, with one Mk signature.
//
// What: the kernel32 functions std.sys calls, as bodiless externs, each
// named exactly once in kernel32.def, which internal/driver reads.
//
// How: handles are uint, BOOL is i32, DWORD is u32. Private to std.sys;
// std externs are shallow and run on the task stack.

extern AddVectoredExceptionHandler(first: u32, handler: fn(*u8): i32): *u8
extern CancelIoEx(h: uint, overlapped: *u8): i32
extern CancelSynchronousIo(thread: uint): i32
extern CloseHandle(h: uint): i32
extern CopyFileExW(src: *u16, dst: *u16, progress: *u8, data: *u8, cancel: *i32, flags: u32): i32
extern CreateDirectoryW(name: *u16, sa: *u8): i32
extern CreateEventW(sa: *u8, manual: i32, initial: i32, name: *u16): uint
extern CreateFileMappingW(h: uint, sa: *u8, protect: u32, high: u32, low: u32, name: *u16): uint
extern CreateFileW(name: *u16, access: u32, share: u32, sa: *u8, disposition: u32, flags: u32, template: uint): uint
extern CreateIoCompletionPort(h: uint, port: uint, key: uint, threads: u32): uint
extern CreateJobObjectW(sa: *u8, name: *u16): uint
extern CreateNamedPipeW(name: *u16, open: u32, mode: u32, max: u32, out_size: u32, in_size: u32, timeout: u32, sa: *u8): uint
extern CreatePipe(read: *uint, write: *uint, sa: *u8, size: u32): i32
extern CreateProcessW(app: *u16, cmd: *u16, psa: *u8, tsa: *u8, inherit: i32, flags: u32, env: *u16, cwd: *u16, si: *u8, pi: *u8): i32
extern CreateSemaphoreW(sa: *u8, initial: i32, max: i32, name: *u16): uint
extern CreateSymbolicLinkW(link: *u16, target: *u16, flags: u32): i32
extern CreateThread(sa: *u8, stack: uint, start: fn(*u8): u32, arg: *u8, flags: u32, tid: *u32): uint
extern CreateWaitableTimerExW(sa: *u8, name: *u16, flags: u32, access: u32): uint
extern DeleteFileW(name: *u16): i32
extern DeleteProcThreadAttributeList(list: *u8)
extern DeviceIoControl(h: uint, code: u32, input: *u8, input_size: u32, out: *u8, out_size: u32, done: *u32,
    overlapped: *u8): i32
extern DuplicateHandle(src_process: uint, src: uint, dst_process: uint, dst: *uint, access: u32, inherit: i32, options: u32): i32
extern ExitProcess(code: u32): never
extern FlushFileBuffers(h: uint): i32
extern FlushInstructionCache(process: uint, base: *u8, size: uint): i32
extern FreeEnvironmentStringsW(p: *u16): i32
extern FreeLibrary(module: uint): i32
extern GetActiveProcessorCount(group: u16): u32
extern GetCommandLineW(): *u16
extern GetConsoleMode(h: uint, mode: *u32): i32
extern GetConsoleScreenBufferInfo(h: uint, info: *u8): i32
extern GetCurrentDirectoryW(size: u32, buf: *u16): u32
extern GetCurrentProcess(): uint
extern GetCurrentProcessId(): u32
extern GetCurrentThreadId(): u32
extern GetCurrentThreadStackLimits(low: *uint, high: *uint)
extern GetDynamicTimeZoneInformation(info: *u8): u32
extern GetEnvironmentStringsW(): *u16
extern GetEnvironmentVariableW(name: *u16, buf: *u16, size: u32): u32
extern GetExitCodeProcess(h: uint, code: *u32): i32
extern GetFileAttributesExW(name: *u16, level: u32, info: *u8): i32
extern GetFileAttributesW(name: *u16): u32
extern GetFileInformationByHandle(h: uint, info: *u8): i32
extern GetFileInformationByHandleEx(h: uint, class: u32, buf: *u8, size: u32): i32
extern GetFileSizeEx(h: uint, size: *i64): i32
extern GetFileType(h: uint): u32
extern GetFinalPathNameByHandleW(h: uint, buf: *u16, size: u32, flags: u32): u32
extern GetFullPathNameW(name: *u16, size: u32, buf: *u16, part: *u8): u32
extern GetHandleInformation(h: uint, flags: *u32): i32
extern GetLastError(): u32
extern GetModuleFileNameW(module: uint, buf: *u16, size: u32): u32
extern GetModuleHandleW(name: *u16): uint
extern GetNamedPipeInfo(h: uint, flags: *u32, out_size: *u32, in_size: *u32, max: *u32): i32
extern GetNumberOfConsoleInputEvents(h: uint, n: *u32): i32
extern GetOverlappedResult(h: uint, overlapped: *u8, done: *u32, wait: i32): i32
extern GetProcAddress(module: uint, name: *u8): *u8
extern GetProcessAffinityMask(h: uint, process: *uint, system: *uint): i32
extern GetProcessHandleCount(process: uint, count: *u32): i32
extern GetQueuedCompletionStatusEx(port: uint, entries: *u8, count: u32, removed: *u32, ms: u32, alertable: i32): i32
extern GetStdHandle(which: u32): uint
extern GetSystemDirectoryW(buf: *u16, size: u32): u32
extern GetSystemTimePreciseAsFileTime(ft: *u64)
extern GetTimeZoneInformationForYear(year: u16, dynamic: *u8, info: *u8): i32
extern InitializeProcThreadAttributeList(list: *u8, count: u32, flags: u32, size: *uint): i32
extern LoadLibraryExW(name: *u16, file: uint, flags: u32): uint
extern LockFileEx(h: uint, flags: u32, reserved: u32, low: u32, high: u32, overlapped: *u8): i32
extern MapViewOfFile(m: uint, access: u32, high: u32, low: u32, size: uint): *u8
extern MoveFileExW(src: *u16, dst: *u16, flags: u32): i32
extern PostQueuedCompletionStatus(port: uint, bytes: u32, key: uint, overlapped: *u8): i32
extern QueryPerformanceCounter(count: *i64): i32
extern QueryPerformanceFrequency(freq: *i64): i32
extern ReOpenFile(h: uint, access: u32, share: u32, flags: u32): uint
extern ReadConsoleInputW(h: uint, buf: *u8, len: u32, read: *u32): i32
extern ReadConsoleW(h: uint, buf: *u16, size: u32, done: *u32, control: *u8): i32
extern ReadFile(h: uint, buf: *u8, size: u32, done: *u32, overlapped: *u8): i32
extern RegisterWaitForSingleObject(wait: *uint, h: uint, callback: fn(*u8, u8), context: *u8, ms: u32, flags: u32): i32
extern ReleaseSemaphore(h: uint, n: i32, previous: *i32): i32
extern RemoveDirectoryW(name: *u16): i32
extern RemoveVectoredExceptionHandler(h: *u8): u32
extern RtlLookupFunctionEntry(pc: uint, base: *uint, history: *u8): *u8
extern RtlVirtualUnwind(kind: u32, base: uint, pc: uint, entry: *u8, ctx: *u8, data: *u8, frame: *uint, ptrs: *u8): *u8
extern SearchPathW(path: *u16, name: *u16, ext: *u16, size: u32, buf: *u16, part: *u8): u32
// handler none is NULL: add 0 enables Ctrl+C, add 1 ignores it.
extern SetConsoleCtrlHandler(handler: ?fn(u32): i32, add: i32): i32
extern SetConsoleMode(h: uint, mode: u32): i32
extern SetEnvironmentVariableW(name: *u16, value: *u16): i32
extern SetFileAttributesW(name: *u16, attr: u32): i32
extern SetFileCompletionNotificationModes(h: uint, flags: u8): i32
extern SetFileInformationByHandle(h: uint, class: u32, buf: *u8, size: u32): i32
extern SetFilePointerEx(h: uint, distance: i64, new_pos: *i64, method: u32): i32
extern SetHandleInformation(h: uint, mask: u32, flags: u32): i32
extern SetLastError(code: u32)
extern SetThreadStackGuarantee(size: *u32): i32
extern SetUnhandledExceptionFilter(filter: fn(*u8): i32): *u8
extern SetWaitableTimer(h: uint, due: *i64, period: i32, routine: *u8, arg: *u8, resume: i32): i32
extern Sleep(ms: u32)
extern SwitchToThread(): i32
extern TerminateJobObject(job: uint, code: u32): i32
extern TerminateProcess(h: uint, code: u32): i32
extern UnlockFileEx(h: uint, reserved: u32, low: u32, high: u32, overlapped: *u8): i32
extern UnmapViewOfFile(base: *u8): i32
extern UnregisterWaitEx(wait: uint, event: uint): i32
extern UpdateProcThreadAttribute(list: *u8, flags: u32, attr: uint, value: *u8, size: uint, prev: *u8, ret: *uint): i32
extern VirtualAlloc(addr: *u8, size: uint, kind: u32, protect: u32): *u8
extern VirtualFree(addr: *u8, size: uint, kind: u32): i32
extern VirtualProtect(addr: *u8, size: uint, protect: u32, old: *u32): i32
extern WaitForSingleObject(h: uint, ms: u32): u32
extern WaitForSingleObjectEx(h: uint, ms: u32, alertable: i32): u32
extern WriteConsoleW(h: uint, buf: *u16, size: u32, done: *u32, reserved: *u8): i32
extern WriteFile(h: uint, buf: *u8, size: u32, done: *u32, overlapped: *u8): i32
