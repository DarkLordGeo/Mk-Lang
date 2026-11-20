// Why: std.term only matters on a real terminal, so its behaviour has to be
// proved end to end, on Linux and on a Windows pseudo-console.
// What: the child harness TmChild with its modes, and the pseudo-terminal
// blocks for Open, Raw, Read, ReadSecret, mouse capture and the exit paths.
// How: each block reruns this executable on a proc.Pty and asserts on the
// child's transcript and exit code, and on the mode a probe reads.

// End-to-end blocks for std.term on a pseudo-terminal: the block
// opens a proc.Pty and runs this test executable again on it (args[1] the
// block's index, args[2] the mode TmChild acts on, args[3] the transcript
// file). The child opens the terminal with term.Open, sets raw mode, and
// writes a transcript of its state after each event to the file; the
// block types into the pseudo-terminal and asserts on the transcript and
// the exit code, never on the terminal's bytes (a Windows pseudo-console
// draws its own rendering of the screen), except that the mouse
// blocks look for MouseOff and AltLeave (TmDrainOff), since mouse capture
// is held by no mode a probe can read, and the kitty blocks of
// tests_kitty.mk look for the protocol's pushes and pops (TmDrainKitty),
// which no probe can read either. The mode left on the terminal is
// read by a probe child before and after, and must be the same.

import std.core
import std.fs
import std.proc
import std.raw
import std.rt

// TmMs is a millisecond as a Duration.
TmMs(n: i64): core.Duration {
    return core.DurationOf(n * 1_000_000)
}

// TmLineMode puts the terminal (input h, output out) in line mode with
// echo off, as a shell may while the job is stopped; failed is set when
// the terminal refuses.
TmLineMode(io: Io, h: uint, out: uint, failed: &raw.Atomic[u32]) {
    var old: [64]u8 = [0; 64]
    var none_: [0]u8 = []
    if rt.TermMode(io, h, out, core.IoTermNoEcho, none_[..], mut old[..]).err != core.SysErr.None {
        failed.Store(1)
    }
}

// TmLineModeLater is TmLineMode 150 ms from now.
TmLineModeLater(io: Io, h: uint, out: uint, failed: &raw.Atomic[u32]) {
    io.Sleep(TmMs(150)) else {
        failed.Store(1)
        return
    }
    TmLineMode(io, h, out, failed)
}

// TmReadOrExit is t.Read, or the child's end with a code that names the
// failure: 90 Canceled, 100 + code for Device (code under 50), 91 any
// other.
TmReadOrExit(t: mut Term, a: &Arena, io: Io): Event from a {
    e := t.Read(a, io) else err {
        match err {
            Canceled { proc.Exit(90) }
            Device(code) { proc.Exit(if code >= 0 && code < 50 { 100 + Trunc[int](code) } else { 91 }) }
            _ { proc.Exit(91) }
        }
    }
    return e
}

// TmSecretCut is a ReadSecret that a cancel or a deadline cut (cuts
// counts them): none then; any other failure ends the child (75).
TmSecretCut(t: mut Term, a: &Arena, io: Io, cuts: mut int): ?str from a {
    line := t.ReadSecret(a, io) else err {
        match err {
            Canceled {
                *cuts += 1
                return none
            }
            _ { proc.Exit(75) }
        }
    }
    return some(line)
}

// TmChild is the child half of the blocks below; it never returns.
TmChild(sys: &System): never {
    mode := sys.args[2]
    path := sys.args[3]
    io := sys.io
    if mode == "probe" {
        TmProbe(sys, path)
    }
    if mode == "hold" {
        // A second client of the pseudo-console for TmModeAfter: Windows
        // resets a pseudo-console's modes when its last client exits, so
        // without it a mode the child left would be gone before the probe.
        // TmModeAfter kills it after the last probe.
        io.Sleep(TmMs(60_000)) else {}
        proc.Exit(0)
    }
    if mode == "color" {
        // What a program on the terminal sees of colour, in the
        // environment the block gave it.
        color := UseColor(io, Std.Stdout)
        out := IsTerminal(io, Std.Stdout)
        input := IsTerminal(io, Std.Stdin)
        line := sys.arena.Fmt("color {color} out {out} in {input}\n", (color, out, input)).Must()
        fs.WriteAll(io, path, line.AsBytes()) else { proc.Exit(74) }
        proc.Exit(0)
    }
    if mode == "vt" {
        // UseColor's console modes (tests_vt.mk).
        TmVtChild(sys, path)
    }
    if mode == "piped" {
        // A grandchild whose three streams are a pipe and the null device
        // opens the terminal itself and reads the keys (mode "keys").
        var a: [4]str = [sys.args[0], sys.args[1], "sized", path]
        var c := proc.DefaultCommand
        c.args = a[..]
        c.stdin = proc.Stdio.Pipe
        c.stdout = proc.Stdio.Null
        c.stderr = proc.Stdio.Null
        (child, stdin, stdout, stderr) := proc.Spawn(sys.arena, io, &c) else {
            proc.Exit(76)
        }
        st := child.Wait(io) else {
            proc.Exit(77)
        }
        TmCloseEnds(stdin, stdout, stderr)
        proc.Exit(st.code else { 78 })
    }
    if mode == "stopfull" {
        // The restore table full before Open (tests_cont.mk).
        TmFillRegistry()
    }
    var t := Open(io) else {
        proc.Exit(70)
    }
    if mode == "secretcooked" {
        TmSecretCooked(sys, t, path)
    }
    t.Raw(io) else {
        proc.Exit(71)
    }
    if mode == "cutbare" {
        // The reported failure exactly: no drain of what the terminal queued
        // at start, nothing written to it.
        var log0 := Text.New(sys.arena)
        var cuts := 0
        var first: ?Event = none
        scope s := io.Scope() {
            s.CancelAfter(TmMs(300))
            first = TmReadCut(mut t, sys.arena, io, mut cuts)
        }
        if e := first {
            log0.Push(sys.arena.Fmt("first {d}\n", (d: TmDescribe(sys.arena, e))).Must()).Must()
        }
        log0.Push("cut\n").Must()
        fs.WriteAll(io, path, log0.Str().AsBytes()) else { proc.Exit(74) }
        e := TmReadOrExit(mut t, sys.arena, io)
        log0.Push(TmDescribe(sys.arena, e)).Must()
        log0.Push("\n").Must()
        fs.WriteAll(io, path, log0.Str().AsBytes()) else { proc.Exit(74) }
        t.Close(io)
        proc.Exit(0)
    }
    // What the terminal queued at start (a pseudo-console's first size)
    // is not the block's.
    for {
        var quiet := false
        scope s := io.Scope() {
            s.CancelAfter(TmMs(200))
            _ = t.Read(sys.arena, io) else {
                quiet = true
            }
        }
        if quiet {
            break
        }
    }
    if mode == "kpanic" || mode == "kexit" || mode == "kclose" || mode == "konly" {
        // The kitty keyboard flags in the restore registry:
        // tests_kitty.mk.
        TmKittyChild(sys, t, mode, path)
    }
    if mode == "cont" {
        // The runtime's SIGCONT hook: tests_cont.mk.
        TmContChild(sys, t, path)
    }
    if mode == "contread" {
        TmContReadChild(sys, t, path)
    }
    if mode == "stopfull" {
        TmContFullChild(sys, t, path)
    }
    if mode == "modewait" {
        // Mode changes during a wait: tests_modewait.mk.
        TmModeWaitChild(sys, t, path)
    }
    if mode == "modecancel" {
        TmModeCancelChild(sys, t, path)
    }
    var buf: [256]u8 = [0; 256]
    var w := t.Writer(io, mut buf[..])
    BracketedPaste(mut w, true) else { proc.Exit(72) }
    if mode == "mouse" || mode == "mpanic" || mode == "mexit" || mode == "monly" || mode == "mrestore" || mode == "hang" {
        MouseCapture(mut w, true) else { proc.Exit(72) }
    }
    if mode == "kitty" {
        KittyKeys(mut w, true) else { proc.Exit(72) }
    }
    if mode != "keys" && mode != "sized" && mode != "stopcont" && mode != "stopbusy" && mode != "mouse" && mode != "kitty" && mode != "mexit" && mode != "monly" && mode != "mrestore" && mode != "cutonce" {
        AltScreen(mut w, true) else { proc.Exit(72) }
    }
    w.Str("ready\r\n") else { proc.Exit(72) }
    w.Flush() else { proc.Exit(72) }
    var log := Text.New(sys.arena)
    if mode == "secret" {
        s := t.ReadSecret(sys.arena, io) else {
            proc.Exit(75)
        }
        raw_after := t.is_raw
        log.Push(sys.arena.Fmt("secret {s} raw {raw_after}\n", (s, raw_after)).Must()).Must()
        fs.WriteAll(io, path, log.Str().AsBytes()) else { proc.Exit(74) }
        t.Close(io)
        proc.Exit(0)
    }
    if mode == "secretcut" {
        // ReadSecret cut by a 300 ms deadline, again and again, until a line
        // ends: what was typed before a cut is kept (Read is repeatable).
        var cuts := 0
        for {
            var got: ?str = none
            scope s := io.Scope() {
                s.CancelAfter(TmMs(300))
                got = TmSecretCut(mut t, sys.arena, io, mut cuts)
            }
            if line := got {
                many := cuts >= 2
                log.Push(sys.arena.Fmt("secret {line} cuts {many}\n", (line, many)).Must()).Must()
                break
            }
        }
        fs.WriteAll(io, path, log.Str().AsBytes()) else { proc.Exit(74) }
        t.Close(io)
        proc.Exit(0)
    }
    if mode == "cutonce" {
        // The reported failure's shape: one Read cut by a 300 ms deadline, its scope
        // ended, then a plain Read; the block types q only once "cut" is in
        // the transcript, so the key reaches the second Read alone.
        var first: ?Event = none
        var cuts := 0
        scope s := io.Scope() {
            s.CancelAfter(TmMs(300))
            first = TmReadCut(mut t, sys.arena, io, mut cuts)
        }
        if e := first {
            log.Push(sys.arena.Fmt("first {d}\n", (d: TmDescribe(sys.arena, e))).Must()).Must()
        }
        log.Push("cut\n").Must()
        fs.WriteAll(io, path, log.Str().AsBytes()) else { proc.Exit(74) }
        e := TmReadOrExit(mut t, sys.arena, io)
        log.Push(TmDescribe(sys.arena, e)).Must()
        log.Push("\n").Must()
        fs.WriteAll(io, path, log.Str().AsBytes()) else { proc.Exit(74) }
        t.Close(io)
        proc.Exit(0)
    }
    if mode == "winchpause" || mode == "winchlate" {
        // winchpause: the next Fill sleeps 1 s between its size compare
        // and its wait (FillPause), in which the block resizes the
        // terminal: the SIGWINCH comes after the compare, and only a
        // signal task that took its baseline before the compare sees it.
        // winchlate: the next Fill's signal tasks sleep 1 s before their
        // waits start (SignalLate), and the block resizes the terminal in
        // that sleep, after the compare: only a wait from a mark taken
        // before the compare sees it.
        log.Push("pre\n").Must()
        fs.WriteAll(io, path, log.Str().AsBytes()) else { proc.Exit(74) }
        if mode == "winchpause" {
            FillPause.Store(1000)
        } else {
            SignalLate.Store(1000)
        }
        for {
            e := TmReadOrExit(mut t, sys.arena, io)
            log.Push(TmDescribe(sys.arena, e)).Must()
            log.Push("\n").Must()
            fs.WriteAll(io, path, log.Str().AsBytes()) else { proc.Exit(74) }
            if TmIsQuit(e) {
                break
            }
        }
        t.Close(io)
        proc.Exit(0)
    }
    if mode == "bigpaste" {
        // Every Paste's text, joined, until q: a paste longer than the
        // Term's buffer arrives in pieces.
        for {
            e := t.Read(sys.arena, io) else {
                proc.Exit(73)
            }
            if TmIsQuit(e) {
                break
            }
            match e {
                .Paste(text) { log.Push(text).Must() }
                _ {}
            }
        }
        fs.WriteAll(io, path, log.Str().AsBytes()) else { proc.Exit(74) }
        t.Close(io)
        proc.Exit(0)
    }
    if mode == "sized" {
        // The size of the terminal Open made, with no standard stream on it.
        (cols, rows) := t.Size(io) else {
            proc.Exit(79)
        }
        log.Push(sys.arena.Fmt("size {cols} {rows}\n", (cols, rows)).Must()).Must()
    }
    var cancels := 0
    if mode == "keys" || mode == "cancel" || mode == "sized" || mode == "stopcont" || mode == "stopbusy" || mode == "mouse" || mode == "kitty" {
        for {
            var got: ?Event = none
            if mode == "cancel" {
                // Reads cut by a 3 ms deadline, again and again: each cut
                // must lose nothing (Read is repeatable).
                scope s := io.Scope() {
                    s.CancelAfter(TmMs(3))
                    got = TmReadCut(mut t, sys.arena, io, mut cancels)
                }
            } else if mode == "stopcont" && log.Str().EndsWith("key Char s\n") {
                // What a shell may do while this process is stopped: the
                // terminal back in line mode (echo off, ICANON on), behind
                // the Term's back, 150 ms into this wait (after Fill set raw
                // mode again). The block stops and continues this process
                // while it waits; Read must set raw mode again on SIGCONT,
                // or the next key never arrives alone.
                var failed := raw.Atomic[u32].New(0)
                scope s := io.Scope() {
                    s.go TmLineModeLater(io, t.input, t.out, &failed)
                    got = some(TmReadOrExit(mut t, sys.arena, io))
                }
                if failed.Load() != 0 {
                    proc.Exit(81)
                }
            } else {
                got = some(t.Read(sys.arena, io) else {
                    proc.Exit(73)
                })
            }
            e := got else {
                continue
            }
            if TmIsStartSize(e) {
                // A pseudo-console may report its first size late.
                continue
            }
            log.Push(TmDescribe(sys.arena, e)).Must()
            log.Push("\n").Must()
            fs.WriteAll(io, path, log.Str().AsBytes()) else { proc.Exit(74) }
            if TmIsQuit(e) {
                break
            }
            if mode == "stopbusy" && TmIsChar(e, 's') {
                // The terminal back in line mode behind the Term's
                // back, then a second of work outside Read, in which the
                // block stops and continues this process. No wait sees the
                // SIGCONT; the next Read must set raw mode again anyway.
                var failed := raw.Atomic[u32].New(0)
                TmLineMode(io, t.input, t.out, &failed)
                if failed.Load() != 0 {
                    proc.Exit(81)
                }
                io.Sleep(TmMs(1000)).Must()
            }
        }
        if mode == "cancel" {
            many := cancels >= 10
            log.Push(sys.arena.Fmt("cancels {many}\n", (many)).Must()).Must()
            fs.WriteAll(io, path, log.Str().AsBytes()) else { proc.Exit(74) }
        }
        if mode == "mouse" && !t.mouse {
            // The Writer must have seen MouseCapture, for Close to undo it.
            proc.Exit(82)
        }
        t.Close(io)
        proc.Exit(0)
    }
    if mode == "mpanic" || mode == "mexit" || mode == "monly" || mode == "mrestore" {
        // Mouse capture went through the Writer, so the runtime's
        // record holds it.
        if !t.mouse || t.reg < 0 {
            proc.Exit(83)
        }
        if mode == "mexit" {
            // Raw, capture on, the main screen: proc.Exit turns it off.
            fs.WriteAll(io, path, "raw mouse\n".AsBytes()) else { proc.Exit(74) }
            proc.Exit(5)
        }
        if mode == "mrestore" {
            // Restore turns capture off first, while the mode that
            // forwards it is still in force, and records it off; with no
            // mode left and the main screen, no record remains.
            t.Restore(io) else { proc.Exit(84) }
            if t.mouse || t.reg >= 0 || t.saved_len != 0 {
                proc.Exit(86)
            }
            // With no mode saved, Restore still turns capture off and drops
            // the mouse-only record.
            var again: [64]u8 = [0; 64]
            var w2 := t.Writer(io, mut again[..])
            MouseCapture(mut w2, true) else { proc.Exit(72) }
            w2.Flush() else { proc.Exit(72) }
            if !t.mouse || t.reg < 0 {
                proc.Exit(87)
            }
            t.Restore(io) else { proc.Exit(84) }
            if t.mouse || t.reg >= 0 {
                proc.Exit(88)
            }
            fs.WriteAll(io, path, "restored\n".AsBytes()) else { proc.Exit(74) }
            proc.Exit(7)
        }
        if mode == "monly" {
            // The mode put back (and capture with it), then capture
            // turned on again on the main screen: the record holds only
            // the capture, and proc.Exit still turns it off. "again" marks
            // where the block looks for the off sequence.
            t.Restore(io) else { proc.Exit(84) }
            var again: [64]u8 = [0; 64]
            var w2 := t.Writer(io, mut again[..])
            MouseCapture(mut w2, true) else { proc.Exit(72) }
            w2.Str("again\r\n") else { proc.Exit(72) }
            w2.Flush() else { proc.Exit(72) }
            if !t.mouse || t.reg < 0 || t.saved_len != 0 || t.alt {
                proc.Exit(85)
            }
            fs.WriteAll(io, path, "mouse only\n".AsBytes()) else { proc.Exit(74) }
            proc.Exit(6)
        }
        fs.WriteAll(io, path, "raw alt mouse\n".AsBytes()) else { proc.Exit(74) }
        Panic("std.term pty block: a panic with mouse capture on, in raw mode on the alternate screen")
    }
    fs.WriteAll(io, path, "raw alt\n".AsBytes()) else { proc.Exit(74) }
    if mode == "close" {
        t.Close(io)
        proc.Exit(0)
    }
    if mode == "exit" {
        proc.Exit(3)
    }
    if mode == "hang" {
        // Waits for the block's SIGTERM, which the runtime handles: it
        // restores the registered mode, then ends by the signal.
        io.Sleep(TmMs(60_000)) else {}
        proc.Exit(80)
    }
    // mode "panic": the Term is still open.
    Panic("std.term pty block: a panic in raw mode on the alternate screen")
}

// TmProbe writes the terminal's mode, as hex, to path: it sets raw mode to
// read the mode in force, then puts that mode back.
TmProbe(sys: &System, path: str): never {
    io := sys.io
    var h: [2]uint = [0; 2]
    if rt.TermOpen(io, mut h).err != core.SysErr.None {
        proc.Exit(60)
    }
    var old: [64]u8 = [0; 64]
    var none_: [0]u8 = []
    r := rt.TermMode(io, h[0], h[1], core.IoTermRaw, none_[..], mut old[..])
    if r.err != core.SysErr.None || r.value <= 0 || r.value > 64 {
        proc.Exit(61)
    }
    n := Trunc[int](r.value)
    var back: [64]u8 = [0; 64]
    _ = rt.TermMode(io, h[0], h[1], core.IoTermSaved, old[0..n], mut back[..])
    var hex := Text.New(sys.arena)
    digits := "0123456789abcdef".AsBytes()
    for i in 0..n {
        hex.PushChar(char(digits[int(old[i] >> 4)])).Must()
        hex.PushChar(char(digits[int(old[i] & 15)])).Must()
    }
    fs.WriteAll(io, path, hex.Str().AsBytes()) else { proc.Exit(62) }
    proc.Exit(0)
}

// TmIsQuit: e is the key q.
TmIsQuit(e: Event): bool {
    return TmIsChar(e, 'q')
}

// TmIsChar: e is the key c with no modifier.
TmIsChar(e: Event, c: char): bool {
    return match e {
        .Key(key, ch, mods) {
            match key {
                .Char { ch == c && !mods.ctrl && !mods.alt }
                _ { false }
            }
        }
        _ { false }
    }
}

// TmDescribe is one transcript line for e.
TmDescribe(a: &Arena, e: Event): str from a {
    return match e {
        .Key(key, ch, mods) {
            name := KeyName(key)
            c := match key {
                .Char { a.Fmt(" {ch}", (ch)).Must() }
                _ { "" }
            }
            s := if mods.shift { "+s" } else { "" }
            al := if mods.alt { "+a" } else { "" }
            ct := if mods.ctrl { "+c" } else { "" }
            a.Fmt("key {name}{c}{s}{al}{ct}", (name, c, s, al, ct)).Must()
        }
        .Paste(text) { a.Fmt("paste {text}", (text)).Must() }
        .Resize(cols, rows) { a.Fmt("resize {cols} {rows}", (cols, rows)).Must() }
        .Mouse(button, row, col, mods) {
            s := if mods.shift { "+s" } else { "" }
            al := if mods.alt { "+a" } else { "" }
            ct := if mods.ctrl { "+c" } else { "" }
            a.Fmt("mouse {button} {row} {col}{s}{al}{ct}", (button, row, col, s, al, ct)).Must()
        }
        .Focus(on) { a.Fmt("focus {on}", (on)).Must() }
    }
}

// TmDrain reads what the pseudo-terminal shows until the read fails (the
// block's cancel) or ends, and sets ready once "ready" was seen.
TmDrain(io: Io, p: &proc.Pty, ready: &raw.Atomic[u32]) {
    var b: [4096]u8 = [0; 4096]
    var tail: [16]u8 = [0; 16]
    var keep := 0
    for {
        n := p.Read(io, mut b[..]) else {
            return
        }
        if n == 0 {
            return
        }
        // The last bytes of the previous read, then this one's, so a word
        // split between reads is still found.
        var both: [4112]u8 = [0; 4112]
        for i in 0..keep {
            both[i] = tail[i]
        }
        for i in 0..n {
            both[keep + i] = b[i]
        }
        all := both[0..keep + n]
        if Find(all, "ready".AsBytes()).IsSome() {
            ready.Store(1)
        }
        keep = if all.Len() < 16 { all.Len() } else { 16 }
        for i in 0..keep {
            tail[i] = all[all.Len() - keep + i]
        }
    }
}

// TmLines is the number of lines of the transcript at path (0 before it
// exists).
TmLines(sys: &System, path: str): int {
    text := fs.ReadText(sys.arena, sys.io, path) else { "" }
    var n := 0
    for c in text.AsBytes() {
        if c == 0x0A {
            n += 1
        }
    }
    return n
}

// TmStep follows a step (a key typed, a resize): when paced it waits up
// to 5 s for the transcript to have more than before lines, else 150 ms.
TmStep(sys: &System, path: str, paced: bool, before: int) {
    io := sys.io
    if !paced {
        io.Sleep(TmMs(150)).Must()
        return
    }
    for _ in 0..500 {
        if TmLines(sys, path) > before {
            return
        }
        io.Sleep(TmMs(10)).Must()
    }
}

// TmRun runs mode on a new pseudo-terminal of 80 by 24, types keys (each
// once the last arrived, TmStep; an empty key is a 900 ms pause), resizing
// it to 100 by 30 before keys[resize_at], and returns the child's exit
// code and transcript.
TmRun(sys: &System, mode: str, keys: []str, resize_at: int): (code: int, text: str) from sys {
    io := sys.io
    dir := fs.TempDir(sys.arena, io).Must()
    var r: [8]u8 = [0; 8]
    io.Random(mut r[..]).Must()
    tag := u64(r[0]) | (u64(r[1]) << 8) | (u64(r[2]) << 16) | (u64(r[3]) << 24) | (u64(r[4]) << 32)
    path := sys.arena.Fmt("{dir}/mkterm-{mode}-{tag}.txt", (dir, mode, tag)).Must()
    p := proc.Pty.Open(io, 80, 24).Must()
    var a: [4]str = [sys.args[0], sys.args[1], mode, path]
    var c := proc.DefaultCommand
    c.args = a[..]
    c.terminal = some(&p)
    (child, stdin, stdout, stderr) := proc.Spawn(sys.arena, io, &c).Must()
    TmCloseEnds(stdin, stdout, stderr)
    var ready := raw.Atomic[u32].New(0)
    var code := -1000
    scope s := io.Scope() {
        s.go TmDrain(io, &p, &ready)
        var waited := 0
        for ready.Load() == 0 && waited < 1000 {
            io.Sleep(TmMs(10)).Must()
            waited += 1
        }
        // In the modes that write a transcript line per key and per
        // resize, each step waits for its line, so a loaded machine never
        // merges two keys (an escape and the next key would be one alt
        // key); the other modes give each step 150 ms.
        paced := mode == "keys" || mode == "cancel" || mode == "piped" || mode == "mouse" || mode == "kitty"
        for i in 0..keys.Len() {
            if i == resize_at {
                m := TmLines(sys, path)
                p.Resize(io, 100, 30).Must()
                TmStep(sys, path, paced, m)
            }
            if mode == "cancel" {
                // Idle time before each key, so the child's reads are cut
                // at least 10 times however coarse the timers are under
                // load (750 ms in all; a cut of up to 75 ms still counts
                // 10). Paced steps alone left gaps of about 10 ms, and a
                // loaded run once counted fewer than 10.
                io.Sleep(TmMs(150)).Must()
            }
            if keys[i].Len() == 0 {
                // A pause, for the cuts of a deadline.
                io.Sleep(TmMs(900)).Must()
                continue
            }
            n := TmLines(sys, path)
            p.WriteAll(io, keys[i].AsBytes()).Must()
            TmStep(sys, path, paced, n)
        }
        st := child.Wait(io).Must()
        code = st.code else { -1000 }
        s.Cancel()
    }
    p.Close()
    text := fs.ReadText(sys.arena, io, path) else { "" }
    fs.Remove(io, path) else {}
    return (code, text)
}

// TmCloseEnds closes the pipe ends a Spawn with a terminal never makes.
TmCloseEnds(i: ?proc.PipeWriter, o: ?proc.PipeReader, e: ?proc.PipeReader) {
    if w := i {
        w.Close()
    }
    if r := o {
        r.Close()
    }
    if r := e {
        r.Close()
    }
}

// TmMode is the terminal mode a probe child reads on a new pseudo-terminal
// after mode ran on it ("" runs nothing first), as hex.
TmModeAfter(sys: &System, mode: str): (before: str, after: str, code: int) from sys {
    io := sys.io
    p := proc.Pty.Open(io, 80, 24).Must()
    // The "hold" child is the pseudo-console's second client on Windows
    // only: on Linux a pty's termios outlives its clients, and a child that
    // made the pty its controlling terminal would make every later child's
    // Spawn on it Denied. There it runs off the terminal and only waits.
    // On macOS it does the same, and there the first open of a peer no one
    // holds resets termios (XNU's ptsopen), so every probe starts from the
    // default mode and before == after shows nothing about the restore.
    var a: [4]str = [sys.args[0], sys.args[1], "hold", "unused"]
    var c := proc.DefaultCommand
    c.args = a[..]
    if Target.os == core.Os.Windows {
        c.terminal = some(&p)
    }
    (child: hold, stdin: hin, stdout: hout, stderr: herr) := proc.Spawn(sys.arena, io, &c).Must()
    TmCloseEnds(hin, hout, herr)
    io.Sleep(TmMs(300)).Must()
    before := TmOnPty(sys, &p, "probe")
    (code, text: _) := TmOnPtyCode(sys, &p, mode)
    after := TmOnPty(sys, &p, "probe")
    hold.Kill().Must()
    _ = hold.Wait(io).Must()
    p.Close()
    return (before, after, code)
}

// TmOnPty runs mode on p and returns its transcript.
TmOnPty(sys: &System, p: &proc.Pty, mode: str): str from sys {
    (code, text) := TmOnPtyCode(sys, p, mode)
    Assert(code == 0)
    return text
}

// TmOnPtyCode runs mode on p, draining what it shows, and returns its exit
// code and transcript.
TmOnPtyCode(sys: &System, p: &proc.Pty, mode: str): (code: int, text: str) from sys {
    return TmOnPtyEnv(sys, p, mode, proc.Env.Inherit)
}

// TmOnPtyEnv is TmOnPtyCode with the child's environment env.
TmOnPtyEnv(sys: &System, p: &proc.Pty, mode: str, env: proc.Env): (code: int, text: str) from sys {
    io := sys.io
    dir := fs.TempDir(sys.arena, io).Must()
    var r: [8]u8 = [0; 8]
    io.Random(mut r[..]).Must()
    tag := u64(r[0]) | (u64(r[1]) << 8) | (u64(r[2]) << 16) | (u64(r[3]) << 24) | (u64(r[4]) << 32)
    path := sys.arena.Fmt("{dir}/mkterm-{mode}-{tag}.txt", (dir, mode, tag)).Must()
    var a: [4]str = [sys.args[0], sys.args[1], mode, path]
    var c := proc.DefaultCommand
    c.args = a[..]
    c.env = env
    c.terminal = some(p)
    (child, stdin, stdout, stderr) := proc.Spawn(sys.arena, io, &c).Must()
    TmCloseEnds(stdin, stdout, stderr)
    var ready := raw.Atomic[u32].New(0)
    var code := -1000
    scope s := io.Scope() {
        s.go TmDrain(io, p, &ready)
        st := child.Wait(io).Must()
        code = st.code else { -1000 }
        s.Cancel()
    }
    text := fs.ReadText(sys.arena, io, path) else { "" }
    fs.Remove(io, path) else {}
    return (code, text)
}

// The bits TmDrainOff sets: its mark ("ready", or "again") shown; MouseOff
// after it; AltLeave after that.
const TmSeenReady: u32 = 1
const TmSeenOff: u32 = 2
const TmSeenLeave: u32 = 4

// TmDrainOff is TmDrain that also notes, in seen, whether the terminal was
// sent MouseOff after "ready", and the alternate screen left after that:
// the one check of these blocks on the terminal's bytes, since no mode a
// probe can read holds mouse capture (it is the terminal emulator's). A
// Windows pseudo-console keeps focus reports (1004) for itself and passes
// on only the mouse modes' resets, so there the check is the last of them,
// 1000's (measured on this host: 1000, 1006 and 1049 pass, 1004 does not).
TmDrainOff(io: Io, p: &proc.Pty, mark: str, seen: &raw.Atomic[u32]) {
    off := if Target.os == core.Os.Windows { "\x1b[?1000l" } else { MouseOff }
    var b: [4096]u8 = [0; 4096]
    var tail: [32]u8 = [0; 32]
    var keep := 0
    for {
        n := p.Read(io, mut b[..]) else {
            return
        }
        if n == 0 {
            return
        }
        var both: [4128]u8 = [0; 4128]
        for i in 0..keep {
            both[i] = tail[i]
        }
        for i in 0..n {
            both[keep + i] = b[i]
        }
        all := both[0..keep + n]
        var bits := seen.Load()
        var from := 0
        if bits & TmSeenReady == 0 {
            if at := Find(all, mark.AsBytes()) {
                bits |= TmSeenReady
                from = at + mark.Len()
            }
        }
        if bits & TmSeenReady != 0 {
            if bits & TmSeenOff == 0 {
                if at := Find(all[from..], off.AsBytes()) {
                    bits |= TmSeenOff
                    from += at + off.Len()
                }
            }
            if bits & TmSeenOff != 0 && Find(all[from..], AltLeave.AsBytes()).IsSome() {
                bits |= TmSeenLeave
            }
        }
        seen.Store(bits)
        keep = if all.Len() < 32 { all.Len() } else { 32 }
        for i in 0..keep {
            tail[i] = all[all.Len() - keep + i]
        }
    }
}

// TmMouseAfter runs mode on a new pseudo-terminal between two probes, as
// TmModeAfter does, and also returns TmDrainOff's bits for its run; it
// waits up to 2 s after the child ended for the bits in want.
TmMouseAfter(sys: &System, mode: str, mark: str, want: u32): (before: str, after: str, code: int, seen: u32) from sys {
    io := sys.io
    p := proc.Pty.Open(io, 80, 24).Must()
    before := TmOnPty(sys, &p, "probe")
    dir := fs.TempDir(sys.arena, io).Must()
    var r: [8]u8 = [0; 8]
    io.Random(mut r[..]).Must()
    tag := u64(r[0]) | (u64(r[1]) << 8) | (u64(r[2]) << 16) | (u64(r[3]) << 24) | (u64(r[4]) << 32)
    path := sys.arena.Fmt("{dir}/mkterm-{mode}-{tag}.txt", (dir, mode, tag)).Must()
    var a: [4]str = [sys.args[0], sys.args[1], mode, path]
    var c := proc.DefaultCommand
    c.args = a[..]
    c.terminal = some(&p)
    (child, stdin, stdout, stderr) := proc.Spawn(sys.arena, io, &c).Must()
    TmCloseEnds(stdin, stdout, stderr)
    var seen := raw.Atomic[u32].New(0)
    var code := -1000
    scope s := io.Scope() {
        s.go TmDrainOff(io, &p, mark, &seen)
        st := child.Wait(io).Must()
        code = st.code else { -1000 }
        // What the child wrote before it ended is read by the drain within
        // 2 s, or never.
        for _ in 0..200 {
            if seen.Load() & want == want {
                break
            }
            io.Sleep(TmMs(10)).Must()
        }
        s.Cancel()
    }
    fs.Remove(io, path) else {}
    after := TmOnPty(sys, &p, "probe")
    p.Close()
    return (before, after, code, seen: seen.Load())
}

test "Term on a pseudo-terminal: keys, a cursor key, a paste, non-ASCII keys with a surrogate pair, and a resize, in order" (sys: &System) {
    if sys.args.Len() > 2 {
        TmChild(sys)
    }
    var keys: [8]str = ["a", "\x03", "\x1b", "\x1b[A", "\x1b[200~xyz\x1b[201~", "\u{e9}", "\u{1F600}", "q"]
    (code, text) := TmRun(sys, "keys", keys[..], 7)
    Assert(code == 0)
    Assert(TmSame(sys, text, "key Char a\nkey Char c+c\nkey Escape\nkey Up\npaste xyz\nkey Char \u{e9}\nkey Char \u{1F600}\nresize 100 30\nkey Char q\n"))
}

test "Term.Close on the alternate screen in raw mode puts the terminal's mode back" (sys: &System) {
    if sys.args.Len() > 2 {
        TmChild(sys)
    }
    (before, after, code) := TmModeAfter(sys, "close")
    Assert(code == 0)
    Assert(before.Len() > 0 && before == after)
}

test "Term.Read cut again and again by a 3 ms deadline loses no key, no sequence and no paste (Read is repeatable)" (sys: &System) {
    if sys.args.Len() > 2 {
        TmChild(sys)
    }
    var keys: [5]str = ["a", "\x1b[1;5C", "\x1b[200~p\u{e9}\x1b[201~", "\x1b", "q"]
    (code, text) := TmRun(sys, "cancel", keys[..], -1)
    Assert(code == 0)
    Assert(TmSame(sys, text, "key Char a\nkey Right+c\npaste p\u{e9}\nkey Escape\nkey Char q\ncancels true\n"))
}

test "Term.ReadSecret reads a line with Backspace and a paste, echoes nothing, and keeps raw mode it found" (sys: &System) {
    if sys.args.Len() > 2 {
        TmChild(sys)
    }
    var keys: [3]str = ["pa\x7fs", "\x1b[200~s\u{e9}\x1b[201~", "\r"]
    (code, text) := TmRun(sys, "secret", keys[..], -1)
    Assert(code == 0)
    Assert(TmSame(sys, text, "secret pss\u{e9} raw true\n"))
}

test "Term.ReadSecret cut by deadlines keeps what was typed, and the next call goes on with it (ReadSecret is repeatable)" (sys: &System) {
    if sys.args.Len() > 2 {
        TmChild(sys)
    }
    // "ab", then 900 ms with no key, in which 300 ms deadlines cut the
    // child's ReadSecret at least twice; then "c" and Enter.
    var keys: [4]str = ["a", "b", "", "c\r"]
    (code, text) := TmRun(sys, "secretcut", keys[..], -1)
    Assert(code == 0)
    Assert(TmSame(sys, text, "secret abc cuts true\n"))
}

// The runtime restores the mode std.term registered (rt.TermRegisterOpen)
// on a panic, in proc.Exit and on a fatal signal it handles.
test "A panic in raw mode on the alternate screen leaves the terminal's mode as it was" (sys: &System) {
    if sys.args.Len() > 2 {
        TmChild(sys)
    }
    (before, after, code) := TmModeAfter(sys, "panic")
    Assert(code == 101)
    Assert(before.Len() > 0 && before == after)
}

test "proc.Exit in raw mode on the alternate screen leaves the terminal's mode as it was" (sys: &System) {
    if sys.args.Len() > 2 {
        TmChild(sys)
    }
    (before, after, code) := TmModeAfter(sys, "exit")
    Assert(code == 3)
    Assert(before.Len() > 0 && before == after)
}

// The runtime's record also holds mouse capture, which a Writer
// turned on; the exit paths write MouseOff before they leave the
// alternate screen.
test "A panic with mouse capture on, in raw mode on the alternate screen, turns capture off, then leaves the screen, and puts the mode back" (sys: &System) {
    if sys.args.Len() > 2 {
        TmChild(sys)
    }
    (before, after, code, seen) := TmMouseAfter(sys, "mpanic", "ready", TmSeenReady | TmSeenOff | TmSeenLeave)
    Assert(code == 101)
    Assert(before.Len() > 0 && before == after)
    Assert(seen == TmSeenReady | TmSeenOff | TmSeenLeave)
}

test "proc.Exit with mouse capture on, in raw mode on the main screen, turns capture off and puts the mode back" (sys: &System) {
    if sys.args.Len() > 2 {
        TmChild(sys)
    }
    (before, after, code, seen) := TmMouseAfter(sys, "mexit", "ready", TmSeenReady | TmSeenOff)
    Assert(code == 5)
    Assert(before.Len() > 0 && before == after)
    Assert(seen & (TmSeenReady | TmSeenOff) == TmSeenReady | TmSeenOff)
}

// Restore turns capture off before it puts the mode back, so on
// Windows the off sequence goes out while virtual-terminal input is still
// on, which the pseudo-console forwards.
test "Term.Restore with mouse capture on turns capture off first, records it off, and puts the mode back" (sys: &System) {
    if sys.args.Len() > 2 {
        TmChild(sys)
    }
    (before, after, code, seen) := TmMouseAfter(sys, "mrestore", "ready", TmSeenReady | TmSeenOff)
    Assert(code == 7)
    Assert(before.Len() > 0 && before == after)
    Assert(seen & (TmSeenReady | TmSeenOff) == TmSeenReady | TmSeenOff)
}

// A record that holds only the capture: turned on again after Restore.
// On Windows the bytes cannot be checked: once virtual-terminal input is
// off again, the pseudo-console reads mouse-mode sequences itself and
// passes none to its terminal (measured on this host: a program's own
// "\x1b[?1000l" and "\x1b[?1003h" written after Restore never reached the
// pseudo-terminal's reader), so there the block checks the record and the
// exit code only (the child's exits 83 and 85).
test "proc.Exit with only mouse capture on (turned on again after Restore, no alternate screen) turns capture off" (sys: &System) {
    if sys.args.Len() > 2 {
        TmChild(sys)
    }
    want := if Target.os == core.Os.Windows { TmSeenReady } else { TmSeenReady | TmSeenOff }
    (before, after, code, seen) := TmMouseAfter(sys, "monly", "again", want)
    Assert(code == 6)
    Assert(before.Len() > 0 && before == after)
    Assert(seen & want == want)
}

test "On Linux, SIGTERM in raw mode on the alternate screen with mouse capture on turns capture off, leaves the screen and the terminal's mode as it was, and ends the child by the signal" (sys: &System) {
    if sys.args.Len() > 2 {
        TmChild(sys)
    }
    if Target.os != core.Os.Linux {
        // Windows delivers no signal to a child (proc.Child.Signal is
        // Unsupported there).
        return
    }
    io := sys.io
    p := proc.Pty.Open(io, 80, 24).Must()
    before := TmOnPty(sys, &p, "probe")
    dir := fs.TempDir(sys.arena, io).Must()
    var r: [8]u8 = [0; 8]
    io.Random(mut r[..]).Must()
    tag := u64(r[0]) | (u64(r[1]) << 8) | (u64(r[2]) << 16) | (u64(r[3]) << 24) | (u64(r[4]) << 32)
    path := sys.arena.Fmt("{dir}/mkterm-hang-{tag}.txt", (dir, tag)).Must()
    var a: [4]str = [sys.args[0], sys.args[1], "hang", path]
    var c := proc.DefaultCommand
    c.args = a[..]
    c.terminal = some(&p)
    (child, stdin, stdout, stderr) := proc.Spawn(sys.arena, io, &c).Must()
    TmCloseEnds(stdin, stdout, stderr)
    var bits := raw.Atomic[u32].New(0)
    var st := proc.Status(code: none, signal: none)
    var seen := false
    all := TmSeenReady | TmSeenOff | TmSeenLeave
    scope s := io.Scope() {
        s.go TmDrainOff(io, &p, "ready", &bits)
        // The child is in raw mode on the alternate screen, with mouse
        // capture on, once its transcript says so.
        seen = TmAwait(sys, path, "raw alt\n")
        child.Signal(proc.Signal.Terminate).Must()
        st = child.Wait(io).Must()
        for _ in 0..200 {
            if bits.Load() == all {
                break
            }
            io.Sleep(TmMs(10)).Must()
        }
        s.Cancel()
    }
    fs.Remove(io, path) else {}
    after := TmOnPty(sys, &p, "probe")
    p.Close()
    // Signalled in raw mode on the alternate screen, not before.
    Assert(seen)
    Assert(st.code.IsNone())
    var by_term := false
    if g := st.signal {
        by_term = match g {
            .Terminate { true }
            _ { false }
        }
    }
    Assert(by_term)
    Assert(before.Len() > 0 && before == after)
    // The fatal-signal path turned capture off, then left the screen.
    Assert(bits.Load() == all)
}
