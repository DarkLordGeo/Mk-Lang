# The boot data of the QEMU kernel: the GDT the
# 32-bit entry loads before it enters long mode, the boot stack, and the
# boot identity map (PML4, PDPT and PD, 2 MiB pages over the first GiB),
# which MkEntry fills. Mk declares each symbol with `extern var` and names
# it with `sym` operands; nothing here is code.

    .section .bss.boot,"aw",@nobits
    .balign 4096
    .globl BootPml4
BootPml4:
    .skip 4096
    .globl BootPdpt
BootPdpt:
    .skip 4096
    .globl BootPd
BootPd:
    .skip 4096
    .balign 16
BootStack:
    .skip 65536
    .globl BootStackTop
BootStackTop:

    .section .rodata.boot,"a",@progbits
    .balign 16
    .globl BootGdt
BootGdt:
    .quad 0
    .quad 0x00AF9A000000FFFF    # 0x08: 64-bit code, ring 0
    .quad 0x00CF92000000FFFF    # 0x10: flat data
BootGdtEnd:
    .balign 4
    .globl BootGdtPtr
BootGdtPtr:
    .word BootGdtEnd - BootGdt - 1
    .quad BootGdt
