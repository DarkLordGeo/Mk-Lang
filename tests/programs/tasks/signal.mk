// Interrupt handling: the cookbook shutdown recipe without the
// listener: a watcher task waits for Interrupt and cancels the scope; the body sleeps until its
// Io returns Canceled. It prints "ready" (flushed) before waiting, so the driver test knows when
// to send Ctrl+C (Windows, a new process group) or SIGINT (Linux), and "stopped" after the join.
// No .cases file: a case cannot send a signal, and with none the program would wait forever.
// The expected standard output is signal.out, exit 0.
import std.proc
import std.time

Watch(s: &Scope, io: Io) {
    proc.WaitSignal(io, proc.Signal.Interrupt) else {
        return
    }
    s.Cancel()
}

export Main(sys: &System): WriteError!void {
    var out := sys.Stdout()
    try Print(mut out, "ready\n")
    try out.Flush()
    scope s := sys.io.Scope() {
        s.go Watch(&s, sys.io)
        for {
            sys.io.Sleep(time.Millis(100)) else {
                break
            }
        }
    }
    try Print(mut out, "stopped\n")
}
