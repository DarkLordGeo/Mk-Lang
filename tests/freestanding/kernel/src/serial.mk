// COM1 output and isa-debug-exit through port I/O:
// in and out instructions in asm, and number formatting with no allocation.

const Com1: u16 = 0x3F8
const DebugExit: u16 = 0xF4
const Digits: str = "0123456789abcdef"

Out8(port: u16, v: u8) {
    unsafe "out writes one byte to an I/O port and touches no memory" {
        // nomem without pure keeps the asm: this line is the
        // boot test's check that codegen does not delete it.
        asm("out dx, al", in("dx") port, in("al") v, options(nomem, nostack, preserves_flags))
    }
}

In8(port: u16): u8 {
    var v: u8 = 0
    unsafe "in reads one byte from an I/O port and touches no memory" {
        asm("in al, dx", out("al") v, in("dx") port, options(nomem, nostack, preserves_flags))
    }
    return v
}

// SerialInit sets COM1 to 115200 8N1 with the FIFO on and interrupts off.
SerialInit() {
    Out8(Com1 + 1, 0x00)
    Out8(Com1 + 3, 0x80)
    Out8(Com1 + 0, 0x01)
    Out8(Com1 + 1, 0x00)
    Out8(Com1 + 3, 0x03)
    Out8(Com1 + 2, 0xC7)
    Out8(Com1 + 4, 0x03)
}

PutByte(b: u8) {
    for In8(Com1 + 5) & 0x20 == 0 {
    }
    Out8(Com1, b)
}

Print(s: str) {
    for b in s.AsBytes() {
        PutByte(b)
    }
}

PrintHex(v: u64) {
    Print("0x")
    var started := false
    for i in 0..16 {
        d := Trunc[int]((v >> Cast[u64](60 - 4 * i)) & 0xF)
        if d != 0 || started || i == 15 {
            started = true
            PutByte(Digits.Byte(d))
        }
    }
}

PrintDec(v: u64) {
    var buf: [20]u8 = [0; 20]
    var n := 0
    var x := v
    for {
        buf[n] = Digits.Byte(Trunc[int](x % 10))
        n += 1
        x = x / 10
        if x == 0 {
            break
        }
    }
    for n > 0 {
        n -= 1
        PutByte(buf[n])
    }
}

// Exit writes code to isa-debug-exit (iobase 0xf4), which ends QEMU with
// status (code << 1) | 1, and halts if the device is absent.
Exit(code: u8): never {
    Out8(DebugExit, code)
    unsafe "the debug-exit device is absent: halt here for good" {
        asm("cli\n3:\nhlt\njmp 3b", options(noreturn, nostack))
    }
}
