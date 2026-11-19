// Why: the kernel's crash path (fault stub, handler, Panic, panic hook) only
// runs when something goes wrong, so it needs an image that goes wrong on
// purpose to be tested at all.
//
// What: crashtest, a second image over the same modules as the kernel, with
// its own KernelMain and NoFaultCode, the exit status when no fault came.
//
// How: it boots, maps the kernel and installs the IDT, then reads the first
// address past the kernel's page tables. The page fault travels the naked
// fault stub, the fault handler, Panic and the console's MkPanicHook, which
// reports the free.PanicInfo and the fault and ends QEMU with the panic
// status.

import src.arch
import src.boot
import src.console

/// The status when the read past the map did not fault.
const NoFaultCode: u8 = 0x11

/// The crash test's main line, which the boot module calls.
export extern KernelMain(start_info: u64): never {
    console.Init()
    console.Write("mk crashtest: booting\n")
    if boot.ImageEnd() <= 0x100000 {
        console.Write("crashtest: the image ends before its load address\n")
        arch.Exit(NoFaultCode)
    }
    arch.MapKernel()
    arch.InstallIdt()
    console.Write("crashtest: reading the first address past the kernel's map\n")
    _ = arch.ReadUnmapped()
    console.Write("crashtest: the read did not fault\n")
    arch.Exit(NoFaultCode)
}
