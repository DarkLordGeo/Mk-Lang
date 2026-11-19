// verdict: ok
// rule: D-538
// note: a .def name list stands for the DLL on any host; checked only, since no zstd.dll is on the build machine
extern ZSTD_versionNumber(): u32

Version(): int {
    var v := 0
    unsafe "ZSTD_versionNumber takes nothing" {
        v = int(ZSTD_versionNumber())
    }
    return v
}
