// Why: the kernel is driven from the serial line, by a person or by a test.
//
// What: Run, Session (Execute, Exit, Status, Stop) and Argument.
//
// How: a prompt, one line read and echoed, its words listed in a mem.Fixed
// area, one command run. `exit` ends the session; its status tells the
// host whether any command failed.

import std.free
import std.mem
import src.arch
import src.console
import src.mm

/// The status `exit` reports when every command succeeded.
export const OkCode: u8 = 0x10

/// The status `exit` reports when some command failed.
export const ErrorCode: u8 = 0x11

const Usage: str = """
    mk kernel shell. Commands:
      help               this list (also --help, -h)
      mem                the boot memory map and the frame allocator's share
      pages              frame allocator and heap hook counters
      alloc N            take N contiguous pages, 1 to 4096
      free ADDR | all    give back what alloc took
      heap N             sum 0 to N-1 in a HeapList on free.Heap()
      ticks N            wait for N local APIC timer interrupts, 1 to 100
      apic               the local APIC's id and version
      break              raise a breakpoint and count it
      echo WORDS...      print the words, one space apart
      stat [reset]       serial line counters
      crash KIND         panic on purpose: must, bounds, overflow, divide, opcode, page
      bench N            page, heap and lock churn for N rounds, 1 to 100000
      exit [CODE]        stop the kernel; CODE 0 to 127, else 16, or 17 after a failure

    """

/// How long the shell waits for a byte before it stops: 5 s of 10 ms timer
/// interrupts, so an unattended boot ends.
const IdleTicks: i64 = 500

/// The mem.Fixed area one line's words are listed in: 32 words of 16 bytes.
/// The list doubles its buffer as it grows, so the 33rd word needs room for
/// 64 and fails with OutOfMemory.
const WordBytes: int = 512

/// One run of pages `alloc` took and `free` has not given back.
struct Block {
    address: u64
    pages: int
}

/// What a session keeps between commands.
struct Session {
    heap: &Alloc
    blocks: HeapList[Block]
    map: &mm.MemoryMap
    failures: int
}

/// Prints the banner line and runs commands until `exit`. Never returns:
/// `exit` ends QEMU.
export Run(out: mut console.Console, map: &mm.MemoryMap): never {
    heap := free.Heap().Alloc()
    var session := Session(heap: heap, blocks: HeapList[Block].New(heap), map: map, failures: 0)
    var line: [120]u8 = [0; 120]
    var area: [WordBytes]u8 = [0; WordBytes]
    var fixed := mem.Fixed.New(mut area[..])
    out.Say("type help for the commands\n")
    for {
        console.Write("> ")
        n := console.ReadLine(mut line, IdleTicks) else e {
            match e {
                console.Silent {
                    out.Say("\nshell: no input for {seconds} s; stopping\n", (seconds: IdleTicks / 100))
                    session.Stop(out, session.Status())
                }
                console.LineTooLong(length) {
                    out.Say("\nerror: a line of {length} bytes is longer than {max}\n", (length, max: console.MaxLine))
                    session.failures += 1
                    continue
                }
            }
        }
        out.Echo(line[..n]) else {
            console.Write("\nerror: the line is not UTF-8\n")
            session.failures += 1
            continue
        }
        text := str.From(line[..n]) else {
            console.Write("error: the line is not UTF-8\n")
            session.failures += 1
            continue
        }
        a := fixed.Alloc()
        var words := HeapList[str].New(a)
        var listed := true
        for word in text.Words() {
            words.Push(a, word) else {
                listed = false
                break
            }
        }
        if !listed {
            out.Say("error: too many words for the shell's {bytes}-byte word area\n", (bytes: WordBytes))
            session.failures += 1
        } else if words.Len() > 0 {
            session.Execute(out, words[0], &words)
        }
        words.Free(a)
        fixed.Reset()
    }
}

/// Runs one command; `words[0]` is its name.
Session.Execute(mut self, out: mut console.Console, name: str, words: &HeapList[str]) {
    ok := match name {
        "help" | "--help" | "-h" {
            console.Write(Usage)
            true
        }
        "mem" { self.Memory(out) }
        "pages" { Pages(out) }
        "alloc" { self.Alloc(out, words) }
        "free" { self.Free(out, words) }
        "heap" { self.FillHeap(out, words) }
        "ticks" { Ticks(out, words) }
        "apic" { Apic(out) }
        "break" { Break(out) }
        "echo" { Echo(out, words) }
        "stat" { Stat(out, words) }
        "crash" { Crash(out, words) }
        "bench" { self.Bench(out, words) }
        "exit" { self.Exit(out, words) }
        _ {
            out.Say("error: unknown command '{name}'; type help for the commands\n", (name))
            false
        }
    }
    if !ok {
        self.failures += 1
    }
}

/// The number in `words[1]`, between low and high; reports and returns
/// none when it is missing or bad.
Argument(out: mut console.Console, words: &HeapList[str], low: u64, high: u64): ?u64 {
    command := words[0]
    if words.Len() < 2 {
        out.Say("error: {command}: a number from {low} to {high} is needed\n", (command, low, high))
        return none
    }
    if words.Len() > 2 {
        out.Say("error: {command}: one number is expected, not {count} words\n", (command, count: words.Len() - 1))
        return none
    }
    word := words[1]
    value := Number(word, low, high) else e {
        match e {
            NotANumber { out.Say("error: {command}: '{word}' is not a number\n", (command, word)) }
            OutOfBounds(value: given) { out.Say("error: {command}: {given} is outside {low} to {high}\n", (command, given, low, high)) }
        }
        return none
    }
    return some(value)
}

Session.Exit(mut self, out: mut console.Console, words: &HeapList[str]): bool {
    var code := self.Status()
    if words.Len() > 1 {
        value := Argument(out, words, 0, 127) else {
            return false
        }
        code = Trunc[u8](value)
    }
    self.Stop(out, code)
}

// OkCode, or ErrorCode once a command has failed.
Session.Status(&self): u8 {
    return if self.failures > 0 { ErrorCode } else { OkCode }
}

// Gives back every block alloc took, says goodbye and ends QEMU with code.
Session.Stop(mut self, out: mut console.Console, code: u8): never {
    for {
        block := self.blocks.Pop() else {
            break
        }
        mm.Release(block.address, block.pages) else e {
            out.Say("error: exit: {e}\n", (e))
        }
    }
    out.Say("bye: {failures} commands failed, status {code}\n", (failures: self.failures, code))
    arch.Exit(code)
}
