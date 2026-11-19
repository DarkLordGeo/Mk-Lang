// The boot path of the QEMU kernel: the PVH note QEMU's -kernel loader
// reads, the 32-bit entry that
// enters long mode, and the 64-bit entry that enables SSE and calls
// KernelMain. The GDT, the boot stack and the boot identity map are
// boot.s's, declared here with extern var.

import std.raw

// XEN_ELFNOTE_PHYS32_ENTRY (type 18, name "Xen"): namesz 4, descsz 4, type
// 18, "Xen\0", and the 32-bit entry's address, which link.ld places first
// at 1 MiB.
const PvhNote: [5]u32 = [4, 4, 18, 0x006E6558, 0x100000] section ".note.Xen"

extern var BootPml4: u8
extern var BootPdpt: u8
extern var BootPd: u8
extern var BootStackTop: u8
extern var BootGdtPtr: u8

// The end of the image, defined by link.ld. It is KernelEnd, not
// __kernel_end: E0207 wants a PascalCase name.
extern var KernelEnd: u8

// The 32-bit entry: PVH enters here in protected mode with paging off. It
// fills the boot identity map (2 MiB pages over the first GiB), loads the
// GDT, enables PAE, sets EFER.LME, loads CR3, enables paging and jumps to
// Entry64 through the 64-bit code selector.
export extern naked MkEntry() section ".text.boot" {
    unsafe "the PVH loader enters here in 32-bit protected mode; the template runs before any Mk code and touches only boot.s's data" {
        asm("""
            .code32
            cli
            movl ${stack}, %esp
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
            lgdt {gdtp}
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
            """, stack: sym BootStackTop, pml4: sym BootPml4, pdpt: sym BootPdpt, pd: sym BootPd, gdtp: sym BootGdtPtr, entry: sym Entry64, options(att))
    }
}

// The 64-bit entry: loads the data selectors and the boot stack, enables
// SSE (CR0.EM = 0, CR0.MP = 1, CR4.OSFXSR, CR4.OSXMMEXCPT) for the one cpu
// "sse2" function, and calls KernelMain, which does not return.
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
            movabsq ${stack}, %rsp
            movq %cr0, %rax
            andq $-5, %rax
            orq $2, %rax
            movq %rax, %cr0
            movq %cr4, %rax
            orq $0x600, %rax
            movq %rax, %cr4
            call {main}
            2:
            hlt
            jmp 2b
            """, stack: sym BootStackTop, main: sym KernelMain, options(att))
    }
}

// ImageEnd is the first address past the image, read through extern var.
ImageEnd(): u64 {
    unsafe "link.ld defines KernelEnd; only its address is taken" {
        p: *u8 = &KernelEnd
        return u64(raw.Addr(p))
    }
}
