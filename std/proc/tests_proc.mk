// Why: WaitSignal must honour a cancelled scope and SignalCode must match
// IoTable.signal's numbering.
// What: two test blocks; proc.Exit and real signal delivery are whole
// programs in std-io_mods_test.go and std-io_signal_test.go instead.
// How: run by mk test --std=std.proc and std/std-io_blocks_test.go.

test "WaitSignal in a cancelled scope is Canceled" (sys: &System) {
    var got := 0
    scope s := sys.io.Scope() {
        s.Cancel()
        WaitSignal(sys.io, Signal.Interrupt) else {
            got = 1
        }
    }
    Assert(got == 1)
}

test "SignalCode numbers the signals as IoTable.signal does" {
    Assert(SignalCode(Signal.Interrupt) == 0)
    Assert(SignalCode(Signal.Terminate) == 1)
    Assert(SignalCode(Signal.Hangup) == 2)
}
