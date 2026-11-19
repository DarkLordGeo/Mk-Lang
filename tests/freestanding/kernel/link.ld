/* The QEMU kernel's layout: the 32-bit entry first,
 * at 1 MiB, which is the address the PVH note in src/boot.mk names; then
 * the rest, and KernelEnd after the last byte the image occupies. */
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
