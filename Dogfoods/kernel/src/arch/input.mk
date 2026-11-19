// Why: serial input must not be lost while the kernel is busy.
//
// What: InputRing, OnSerial, TakeInput and InputCounters.
//
// How: COM1 raises line 4 of the master PIC when a byte arrives, and the
// handler moves every waiting byte into a ring before the UART's 16-byte
// FIFO overflows. The ring's spin lock is taken by the handler, and by the
// kernel only with interrupts disabled, so on one CPU they never wait for
// each other.

import std.atomic

/// The vector the master PIC's line 4, COM1, arrives on.
export const SerialVector: u8 = 0x24

const RingBytes: int = 512

struct InputRing {
    bytes: [512]u8
    head: int
    count: int
    received: u64
    dropped: u64
}

var Input: atomic.Spin[InputRing] = atomic.Spin[InputRing].New(InputRing(bytes: [0; 512], head: 0, count: 0, received: 0, dropped: 0))

InputRing.Push(mut self, b: u8) {
    self.received += 1
    if self.count == RingBytes {
        self.dropped += 1
        return
    }
    self.bytes[(self.head + self.count) % RingBytes] = b
    self.count += 1
}

InputRing.Pop(mut self): ?u8 {
    if self.count == 0 {
        return none
    }
    b := self.bytes[self.head]
    self.head = (self.head + 1) % RingBytes
    self.count -= 1
    return some(b)
}

// Runs in the serial stub with interrupts disabled.
OnSerial() {
    var uart := Uart.New(Com1)
    var guard := Input.Lock()
    ring := guard.Mut()
    for {
        b := uart.Poll() else {
            break
        }
        ring.Push(b)
    }
    guard.Unlock()
    EndOfLegacyInterrupt()
}

/// The oldest byte the line delivered and nobody has taken, or none. Call
/// it with interrupts disabled.
export TakeInput(): ?u8 {
    var guard := Input.Lock()
    b := guard.Mut().Pop()
    guard.Unlock()
    return b
}

/// Bytes the line delivered since boot, and how many of them were lost
/// because the ring was full. Call it with interrupts disabled.
export InputCounters(): (received: u64, dropped: u64) {
    guard := Input.Lock()
    ring := guard.Ref()
    counters := (received: ring.received, dropped: ring.dropped)
    guard.Unlock()
    return counters
}
