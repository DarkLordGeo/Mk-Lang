/* The kernel's layout. QEMU's PVH loader reads the ELF note in .note.Xen
 * and enters MkEntry in 32-bit protected mode, so the image is linked at
 * its load address, 1 MiB, with the 32-bit entry first. KernelEnd is the
 * first page past the image: the page allocator hands out nothing below it. */
ENTRY(MkEntry)

SECTIONS
{
    . = 0x100000;
    .text.boot : { KEEP(*(.text.boot)) }
    .text : { *(.text .text.*) }
    .note.Xen : { KEEP(*(.note.Xen)) }
    .rodata : { *(.rodata .rodata.*) }
    .data : { *(.data .data.*) }
    .bss : { *(.bss .bss.*) *(COMMON) }
    . = ALIGN(4096);
    KernelEnd = .;
}
