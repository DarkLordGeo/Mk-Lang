// verdict: ok
// rule: D-538
// note: doc/cookbook.md's "Call a function in a Windows DLL" with the pkg.mk it names; no Main, since mk corpus --build does not read a unit's links (tests/programs/ffi/winmm links and runs it)
extern timeGetTime(): u32

UptimeMillis(): int {
    var ms := 0
    unsafe "timeGetTime takes nothing and reads a clock" {
        ms = int(timeGetTime())
    }
    return ms
}
