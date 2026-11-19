// The cookbook's "Call a function in a Windows DLL" (doc/cookbook.md)
// with a Main that prints UptimeMillis(): timeGetTime comes from
// winmm.dll, the named link, not from kernel32, which exports it too
// (a named link comes before the system libraries).
extern timeGetTime(): u32

UptimeMillis(): int {
    var ms := 0
    unsafe "timeGetTime takes nothing and reads a clock" {
        ms = int(timeGetTime())
    }
    return ms
}

export Main(sys: &System): WriteError!void {
    var out := sys.Stdout()
    ms := UptimeMillis()
    try Print(mut out, "{ms}\n", (ms: ms))
}
