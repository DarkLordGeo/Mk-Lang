// Why: everything the kernel says and reads goes over COM1, before and
// after the heap exists.
//
// What: Init, Write, ReadLine, the byte counters, and Console, which Print
// formats onto the line.
//
// How: the transmitter is behind a spin lock, so a line goes out whole;
// lines are read from the input ring the serial interrupt fills; Console
// writes through a std.text Sink over the Uart, so it allocates nothing.

import std.atomic
import std.text
import src.arch

/// The longest line ReadLine accepts, not counting its end.
export const MaxLine: int = 120

const Newline: u8 = 0x0A
const Return: u8 = 0x0D
const Backspace: u8 = 0x08
const Delete: u8 = 0x7F

/// A line was longer than MaxLine; the rest of it was read and dropped.
export error LineTooLong(length: int)
/// The line stayed silent for the whole wait.
export error Silent

/// Why ReadLine returned no line.
export type LineError = LineTooLong | Silent

var Port: atomic.Spin[arch.Uart] = atomic.Spin[arch.Uart].New(arch.Uart.New(arch.Com1))

/// Programs COM1. Called once at boot, before any output.
export Init() {
    guard := Port.Lock()
    guard.Ref().Init()
    guard.Unlock()
}

/// Sends `s` while holding the port, so no other writer's bytes land
/// inside it. O(len(s)) port writes.
export Write(s: str) {
    var guard := Port.Lock()
    guard.Mut().Write(s.AsBytes())
    guard.Unlock()
}

/// Bytes sent on COM1 since boot or since ResetCounters, and bytes the
/// line delivered since boot and the ones lost to a full input ring.
export Counters(): (sent: u64, received: u64, dropped: u64) {
    guard := Port.Lock()
    sent := guard.Ref().Sent()
    guard.Unlock()
    arch.DisableInterrupts()
    input := arch.InputCounters()
    arch.EnableInterrupts()
    return (sent, received: input.received, dropped: input.dropped)
}

/// Sets the sent counter back to zero.
export ResetCounters() {
    var guard := Port.Lock()
    guard.Store(arch.Uart.New(arch.Com1))
    guard.Unlock()
}

/// Waits for the next byte from the line, sleeping until each interrupt;
/// none once `ticks` timer interrupts pass with no byte.
export ReadByte(ticks: i64): ?u8 {
    deadline := arch.TimerTicks() + ticks
    for {
        arch.DisableInterrupts()
        if b := arch.TakeInput() {
            arch.EnableInterrupts()
            return some(b)
        }
        if arch.TimerTicks() >= deadline {
            arch.EnableInterrupts()
            return none
        }
        arch.SleepUntilInterrupt()
    }
}

/// Reads one line into `buf` and returns its length. A line ends at `\n`
/// or `\r`; a `\n` right after `\r` ends nothing, so `\r\n` is one end.
/// Backspace and DEL remove the last byte. Fails with LineTooLong, after
/// reading the rest of the line, when it holds more than MaxLine bytes, and
/// with Silent when `ticks` timer interrupts pass with no byte.
export ReadLine(buf: mut [120]u8, ticks: i64): LineError!int {
    var n := 0
    var dropped := 0
    var last: u8 = 0
    for {
        b := ReadByte(ticks) else {
            fail Silent
        }
        if b == Newline && last == Return {
            last = 0
            continue
        }
        last = b
        if b == Newline || b == Return {
            break
        }
        if b == Backspace || b == Delete {
            if n > 0 {
                n -= 1
            }
            continue
        }
        if n == MaxLine {
            dropped += 1
            continue
        }
        buf[n] = b
        n += 1
    }
    if dropped > 0 {
        fail LineTooLong(length: n + dropped)
    }
    return n
}

/// Sends a run of bytes on `port`: the function of the Sink that every
/// Writer over a Uart goes through. A Uart takes every byte, so it never
/// fails.
export UartSend(port: mut arch.Uart, b: []u8): WriteError!void {
    port.Write(b)
}

/// The Sink of every Writer over a Uart: the console's, and the panic
/// hook's over a Uart of its own.
export const UartSink = text.Sink[arch.Uart].New(UartSend)

/// The bytes a Writer over the port gathers before it hands them on.
const SayBuffer: int = 64

/// What Print formats onto COM1. It holds nothing: each call takes the
/// port's lock and writes through a Writer over the locked Uart, so a line
/// goes out whole, nothing is allocated, and it works before the heap does.
export struct Console {}

/// The console. Allocates nothing.
export Console.New(): Console {
    return Console()
}

/// Sends `bytes` and a newline as one unit. Fails with WriteError, sending
/// nothing, when they are not UTF-8. O(len(bytes)).
export Console.Echo(mut self, bytes: []u8): WriteError!void {
    _ = str.From(bytes) else {
        fail WriteError
    }
    var guard := Port.Lock()
    guard.Mut().Write(bytes)
    guard.Mut().Write("\n".AsBytes())
    guard.Unlock()
}

/// Formats `args` by `f` and sends the result as one unit, holding the port
/// while it formats. Nothing it formats may write to the console, which
/// would wait for its own lock. O(output).
export Console.Say[A: Fields[Format] + Plain](mut self, const f: str, args: A) {
    var buf: [SayBuffer]u8 = [0; SayBuffer]
    var guard := Port.Lock()
    var w := (&UartSink).Writer(guard.Mut(), mut buf[..])
    Print(mut w, f, args) else {
    }
    w.Flush() else {
    }
    guard.Unlock()
}
