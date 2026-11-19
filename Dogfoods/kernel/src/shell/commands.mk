// Why: the shell's commands exercise each part of the kernel from the
// serial line.
//
// What: Session.Memory, Pages, Session.Alloc, Session.Free,
// Session.FillHeap, Ticks, Apic, Break, Echo, Stat and Session.Bench.
//
// How: each reports its own errors on the console and returns whether it
// succeeded.

import std.atomic
import src.arch
import src.console
import src.mm

Session.Memory(mut self, out: mut console.Console): bool {
    out.Say("memory map: {count} regions, {usable} bytes usable\n", (count: self.map.Len(), usable: self.map.UsableBytes()))
    for i in 0..self.map.Len() {
        if r := self.map.Get(i) {
            out.Say("  {start:016x} - {end:016x}  {kind}\n", (start: r.start, end: r.End(), kind: r.KindName()))
        }
    }
    usage := mm.Usage()
    out.Say("frame allocator: {usage.total} pages managed, {usage.used} in use\n", (usage))
    return true
}

Pages(out: mut console.Console): bool {
    usage := mm.Usage()
    calls := mm.HookCalls()
    out.Say("pages: {usage.total} managed, {usage.used} in use, {free} free\n", (usage, free: usage.total - usage.used))
    out.Say("heap hooks: {calls.allocations} runs taken, {calls.releases} given back\n", (calls))
    return true
}

Session.Alloc(mut self, out: mut console.Console, words: &HeapList[str]): bool {
    pages := Argument(out, words, 1, 4096) else {
        return false
    }
    count := Cast[int](pages)
    address := mm.Allocate(count, 1) else {
        out.Say("error: alloc: no run of {count} free pages\n", (count))
        return false
    }
    self.blocks.Push(self.heap, Block(address: address, pages: count)) else {
        mm.Release(address, count) else e {
            out.Say("error: alloc: {e}\n", (e))
        }
        out.Say("error: alloc: OutOfMemory while recording the block\n")
        return false
    }
    out.Say("alloc: {count} pages at 0x{address:x}\n", (count, address))
    return true
}

Session.Free(mut self, out: mut console.Console, words: &HeapList[str]): bool {
    if words.Len() == 2 && words[1] == "all" {
        var pages := 0
        var blocks := 0
        for {
            block := self.blocks.Pop() else {
                break
            }
            mm.Release(block.address, block.pages) else e {
                out.Say("error: free: {e}\n", (e))
                return false
            }
            pages += block.pages
            blocks += 1
        }
        out.Say("free: {pages} pages given back from {blocks} runs\n", (blocks, pages))
        return true
    }
    address := Argument(out, words, 0, 0xFFFFFFFFFFFFFFFF) else {
        return false
    }
    for i in 0..self.blocks.Len() {
        if self.blocks[i].address == address {
            block := self.blocks.Remove(i)
            mm.Release(block.address, block.pages) else e {
                out.Say("error: free: {e}\n", (e))
                return false
            }
            out.Say("free: {pages} pages at 0x{address:x}\n", (pages: block.pages, address))
            return true
        }
    }
    out.Say("error: free: nothing was allocated at 0x{address:x}\n", (address))
    return false
}

Session.FillHeap(mut self, out: mut console.Console, words: &HeapList[str]): bool {
    n := Argument(out, words, 0, 100000000) else {
        return false
    }
    before := mm.Usage().used
    var values := HeapList[u64].New(self.heap)
    var sum: u64 = 0
    for i in 0..n {
        values.Push(self.heap, i) else {
            out.Say("error: heap: OutOfMemory after {count} values\n", (count: values.Len()))
            values.Free(self.heap)
            return false
        }
    }
    for v in values.Items() {
        sum += v
    }
    taken := mm.Usage().used - before
    values.Free(self.heap)
    out.Say("heap: {n} values, sum {sum}, {taken} pages at the peak\n", (n, sum, taken))
    return true
}

Ticks(out: mut console.Console, words: &HeapList[str]): bool {
    n := Argument(out, words, 1, 100) else {
        return false
    }
    arch.WaitTicks(Cast[i64](n))

    out.Say("timer: {n} interrupts taken\n", (n))
    return true
}

Apic(out: mut console.Console): bool {
    out.Say("apic: id {id}, version 0x{version:x}\n", (id: arch.ApicId(), version: arch.ApicVersion()))
    return true
}

Break(out: mut console.Console): bool {
    arch.Breakpoint()
    out.Say("breakpoint: handled, {hits} since boot\n", (hits: arch.BreakpointHits()))
    return true
}

Echo(out: mut console.Console, words: &HeapList[str]): bool {
    for i in 1..words.Len() {
        if i > 1 {
            console.Write(" ")
        }
        console.Write(words[i])
    }
    console.Write("\n")
    return true
}

Stat(out: mut console.Console, words: &HeapList[str]): bool {
    if words.Len() == 2 && words[1] == "reset" {
        console.ResetCounters()
        out.Say("stat: counters reset\n")
        return true
    }
    if words.Len() > 1 {
        out.Say("error: stat: the only argument is reset\n")
        return false
    }
    counters := console.Counters()
    out.Say("serial: {counters.sent} bytes sent, {counters.received} received, {counters.dropped} dropped\n", (counters))
    return true
}

// Each round takes a run of 1 to 8 pages and gives it back, fills and
// frees a HeapList, and bumps a spin-locked tally; the checksum mixes the
// addresses and sums so a change in any of them shows.
Session.Bench(mut self, out: mut console.Console, words: &HeapList[str]): bool {
    rounds := Argument(out, words, 1, 100000) else {
        return false
    }
    tally := atomic.Spin[u64].New(0)
    var check: u64 = 0
    for round in 0..rounds {
        pages := Cast[int](round % 8 + 1)
        address := mm.Allocate(pages, 1) else {
            out.Say("error: bench: no run of {pages} pages in round {round}\n", (pages, round))
            return false
        }
        mm.Release(address, pages) else e {
            out.Say("error: bench: {e}\n", (e))
            return false
        }
        var values := HeapList[u64].New(self.heap)
        for i in 0..64 {
            values.Push(self.heap, round +% Cast[u64](i)) else {
                values.Free(self.heap)
                out.Say("error: bench: OutOfMemory in round {round}\n", (round))
                return false
            }
        }
        var sum: u64 = 0
        for v in values.Items() {
            sum +%= v
        }
        values.Free(self.heap)
        var guard := tally.Lock()
        *guard.Mut() +%= sum
        guard.Unlock()
        check = (check *% 31) +% (address >> 12) +% sum
    }
    total := tally.Into()
    out.Say("bench: {rounds} rounds, tally {total}, checksum {check:016x}\n", (rounds, total, check))
    return true
}
