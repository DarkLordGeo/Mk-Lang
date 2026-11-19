// Why: the kernel's main line brings each part up in order and tests it
// before the shell starts.
//
// What: KernelMain, SelfTest, AddUsableMemory and the boot-failure codes.
//
// How: the serial console, the boot memory map, the page tables, the frame
// allocator, the interrupt table, the interrupt controller and the timer,
// a self-test of each, the heap, then the shell, which never returns.

import src.arch
import src.boot
import src.console
import src.mm
import src.shell

/// The status for a boot step that failed before the shell started.
const BootFailCode: u8 = 0x13

/// Pages the boot self-test takes from the frame allocator and gives back.
const SelfTestPages: int = 8

BootFailed(what: str): never {
    console.Write("boot failed: ")
    console.Write(what)
    console.Write("\n")
    arch.Exit(BootFailCode)
}

BootErrorText(e: mm.BootError): str {
    return match e {
        mm.NoStartInfo { "the loader passed no start info" }
        mm.BadMagic { "the start info has the wrong magic" }
        mm.NoMemoryMap { "the start info holds no memory map" }
        mm.TooManyRegions { "the memory map has too many regions" }
    }
}

// Hands every usable page from PoolStart up to the frame allocator and
// returns how many there were.
AddUsableMemory(map: &mm.MemoryMap): int {
    var pages := 0
    for i in 0..map.Len() {
        r := map.Get(i) else {
            break
        }
        if !r.Usable() || r.End() <= mm.PoolStart {
            continue
        }
        start := if r.start < mm.PoolStart { mm.PoolStart } else { r.start }
        pages += mm.AddFree(start, r.End())
    }
    return pages
}

// Shows that interrupts, the timer and the frame allocator work before the
// shell depends on them.
SelfTest(out: mut console.Console) {
    arch.WaitTicks(3)
    out.Say("boot: timer: 3 interrupts taken\n")
    arch.Breakpoint()
    out.Say("boot: breakpoint: handled, {hits} so far\n", (hits: arch.BreakpointHits()))
    address := mm.Allocate(SelfTestPages, 1) else {
        BootFailed("the frame allocator has no 8 free pages")
    }
    mm.Release(address, SelfTestPages) else e {
        out.Say("boot failed: the frame allocator refused its own pages: {e}\n", (e))
        arch.Exit(BootFailCode)
    }
    out.Say("boot: frame allocator: took {pages} pages at 0x{address:x} and gave them back\n", (pages: SelfTestPages, address))
}

/// The kernel's entry from the boot path, with the address of the PVH
/// start info.
export extern KernelMain(start_info: u64): never {
    console.Init()
    console.Write("mk kernel: booting\n")
    map := mm.ReadStartInfo(start_info) else e {
        BootFailed(BootErrorText(e))
    }
    arch.MapKernel()
    if boot.ImageEnd() > mm.PoolStart {
        BootFailed("the kernel image reaches into the page pool")
    }
    pages := AddUsableMemory(&map)
    if pages == 0 {
        BootFailed("no usable memory above the page pool's start")
    }
    arch.InstallIdt()
    arch.EnableInterruptController()
    arch.StartTimer(arch.TenMilliseconds)
    arch.EnableInterrupts()
    var out := console.Console.New()
    out.Say("boot: {regions} memory regions, {pages} pages managed from 0x{start:x}\n", (regions: map.Len(), pages, start: mm.PoolStart))
    out.Say("boot: local APIC {id} version 0x{version:x} takes the interrupts\n", (id: arch.ApicId(), version: arch.ApicVersion()))
    SelfTest(mut out)
    shell.Run(mut out, &map)
}
