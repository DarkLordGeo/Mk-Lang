// Why: the kernel must get from QEMU's loader into 64-bit Mk code with no
// assembly file.
//
// What: the PVH note, MkEntry, Entry64 and the boot data (tables, stack,
// GDT).
//
// How: QEMU's -kernel loader finds the PVH note (XEN_ELFNOTE_PHYS32_ENTRY,
// xen/include/public/elfnote.h) and enters MkEntry in 32-bit protected mode
// with paging off and the address of its hvm_start_info in ebx. MkEntry
// builds an identity map of the first GiB, enters long mode and jumps to
// Entry64, which calls KernelMain with that address. The boot data is
// declared here, aligned and placed by the language. Each image's root
// module exports its own KernelMain.

import std.raw

// The PVH note: namesz 4, descsz 4, type 18, "Xen\0", and the 32-bit
// entry's address, which link.ld places first, at 1 MiB.
const PvhNote: [5]u32 = [4, 4, 18, 0x006E6558, 0x100000] section ".note.Xen"

// The boot GDT: null, a 64-bit ring-0 code segment (selector 0x08) and a
// flat data segment (selector 0x10). A var, since sym names no const.
var BootGdt: [3]u64 = [0, 0x00AF9A000000FFFF, 0x00CF92000000FFFF] section ".data.boot"

align(4096) struct BootTable {
    entries: [512]u64
}

align(16) struct BootStack {
    bytes: [65536]u8
}

var BootPml4: BootTable = BootTable(entries: [0; 512])
var BootPdpt: BootTable = BootTable(entries: [0; 512])
var BootPd: BootTable = BootTable(entries: [0; 512])
var Stack: BootStack = BootStack(bytes: [0; 65536])

// Defined by link.ld after the image's last section, page-aligned.
extern var KernelEnd: u8

// The image's main line, which its root module exports. It never returns.
extern KernelMain(start_info: u64)

// The 32-bit entry. It leaves ebx alone, so Entry64 still finds the start
// info's address there. The GDT pointer is built on the boot stack, since
// lgdt needs it in memory and nothing else does.
export extern naked MkEntry() section ".text.boot" {
    unsafe "the PVH loader enters here in 32-bit protected mode; the template runs before any Mk code and writes only the boot tables and the boot stack" {
        asm("""
            .code32
            cli
            movl ${stack}+65536, %esp
            movl ${pdpt}, %eax
            orl $3, %eax
            movl %eax, {pml4}
            movl ${pd}, %eax
            orl $3, %eax
            movl %eax, {pdpt}
            xorl %ecx, %ecx
            1:
            movl %ecx, %eax
            shll $21, %eax
            orl $0x83, %eax
            movl %eax, {pd}(,%ecx,8)
            movl $0, {pd}+4(,%ecx,8)
            incl %ecx
            cmpl $512, %ecx
            jne 1b
            subl $16, %esp
            movw $23, (%esp)
            movl ${gdt}, 2(%esp)
            lgdt (%esp)
            addl $16, %esp
            movl %cr4, %eax
            orl $0x20, %eax
            movl %eax, %cr4
            movl ${pml4}, %eax
            movl %eax, %cr3
            movl $0xC0000080, %ecx
            rdmsr
            orl $0x100, %eax
            wrmsr
            movl %cr0, %eax
            orl $0x80000001, %eax
            movl %eax, %cr0
            ljmp $0x08, ${entry}
            .code64
            """, stack: sym Stack, pml4: sym BootPml4, pdpt: sym BootPdpt, pd: sym BootPd, gdt: sym BootGdt, entry: sym Entry64, options(att))
    }
}

// The 64-bit entry: loads the data selectors and the boot stack and calls
// KernelMain with the start info's address, which the 32-bit entry left in
// ebx. KernelMain never returns; the hlt loop is for a debugger's sake.
naked Entry64() {
    unsafe "the far jump from MkEntry lands here in long mode with the boot identity map loaded" {
        asm("""
            movw $0x10, %ax
            movw %ax, %ds
            movw %ax, %es
            movw %ax, %ss
            xorw %ax, %ax
            movw %ax, %fs
            movw %ax, %gs
            movabsq ${stack}+65536, %rsp
            movl %ebx, %edi
            call {main}
            2:
            hlt
            jmp 2b
            """, stack: sym Stack, main: sym KernelMain, options(att))
    }
}

/// The first address past the kernel image.
export ImageEnd(): u64 {
    unsafe "link.ld defines KernelEnd; only its address is taken" {
        p: *u8 = &KernelEnd
        return u64(raw.Addr(p))
    }
}
