// Why: the serial line is the kernel's only console.
//
// What: Uart with New, Init, Put, Write, Poll, Sent and Received.
//
// How: a 16550 UART driven by polling, at the 16550's register offsets
// (data 0, interrupt enable 1, FIFO control 2, line control 3, modem
// control 4, line status 5).

/// COM1's I/O port base on a PC.
export const Com1: u16 = 0x3F8

const LineStatus: u16 = 5
const DataReady: u8 = 0x01
const TransmitEmpty: u8 = 0x20

/// One UART and the bytes it has moved since `New`. Plain, so it can sit
/// in a `Spin` and be copied out for a report.
export struct Uart {
    base: u16
    bytes_sent: u64
    bytes_received: u64
}

/// The UART at I/O port `base`, with its counters at zero. Touches no
/// hardware; `Init` programs it.
export Uart.New(base: u16): Uart {
    return Uart(base: base, bytes_sent: 0, bytes_received: 0)
}

/// Programs the UART for 115200 baud, 8 data bits, no parity, one stop
/// bit and FIFOs on with a 14-byte trigger level (the received-data
/// interrupt fires at 14 bytes, or when fewer have waited four character
/// times); OUT2 set lets it reach the PIC's line 4.
export Uart.Init(&self) {
    Out8(self.base + 1, 0x00)
    Out8(self.base + 3, 0x80)
    Out8(self.base + 0, 0x01)
    Out8(self.base + 1, 0x00)
    Out8(self.base + 3, 0x03)
    Out8(self.base + 2, 0xC7)
    Out8(self.base + 4, 0x0B)
    Out8(self.base + 1, 0x01)
}

/// Sends one byte, spinning until the transmitter has room.
export Uart.Put(mut self, b: u8) {
    for In8(self.base + LineStatus) & TransmitEmpty == 0 {
    }
    Out8(self.base, b)
    self.bytes_sent += 1
}

/// Sends every byte of `s`.
export Uart.Write(mut self, s: []u8) {
    for b in s {
        self.Put(b)
    }
}

/// The next received byte, or none when nothing is waiting.
export Uart.Poll(mut self): ?u8 {
    if In8(self.base + LineStatus) & DataReady == 0 {
        return none
    }
    self.bytes_received += 1
    return some(In8(self.base))
}

/// Bytes sent since `New`.
export Uart.Sent(&self): u64 {
    return self.bytes_sent
}

/// Bytes received since `New`.
export Uart.Received(&self): u64 {
    return self.bytes_received
}
