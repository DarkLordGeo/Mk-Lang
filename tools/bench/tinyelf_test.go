// Why: the peak-memory floor rule needs a test program smaller than any Go
// spawner, made without a C compiler or a checked-in binary.
// What: tinyELFBytes, a 129-byte static x86-64 Linux executable, and
// TestTinyELF, which checks it parses as ELF.
// How: the ELF header, one program header and nine bytes of code built
// with encoding/binary.

package main

import (
	"bytes"
	"debug/elf"
	"encoding/binary"
	"testing"
)

// tinyELFBytes is a 129-byte static x86-64 Linux executable whose whole
// text is exit(0): one PT_LOAD page and the kernel's stack, so its own
// resident set is a few pages, under any Go spawner's floor. Through a sound
// spawner its reading is at or under the floor (TestSpawnPeakLinux).
func tinyELFBytes() []byte {
	const base = 0x400000
	code := []byte{0xb8, 0x3c, 0, 0, 0, 0x31, 0xff, 0x0f, 0x05} // mov eax, 60; xor edi, edi; syscall (exit 0)
	size := 64 + 56 + len(code)
	le := binary.LittleEndian
	b := []byte{0x7f, 'E', 'L', 'F', 2, 1, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0}
	b = le.AppendUint16(b, 2)            // e_type ET_EXEC
	b = le.AppendUint16(b, 0x3e)         // e_machine EM_X86_64
	b = le.AppendUint32(b, 1)            // e_version
	b = le.AppendUint64(b, base+64+56)   // e_entry
	b = le.AppendUint64(b, 64)           // e_phoff
	b = le.AppendUint64(b, 0)            // e_shoff
	b = le.AppendUint32(b, 0)            // e_flags
	b = le.AppendUint16(b, 64)           // e_ehsize
	b = le.AppendUint16(b, 56)           // e_phentsize
	b = le.AppendUint16(b, 1)            // e_phnum
	b = le.AppendUint16(b, 0)            // e_shentsize
	b = le.AppendUint16(b, 0)            // e_shnum
	b = le.AppendUint16(b, 0)            // e_shstrndx
	b = le.AppendUint32(b, 1)            // p_type PT_LOAD
	b = le.AppendUint32(b, 5)            // p_flags R|X
	b = le.AppendUint64(b, 0)            // p_offset
	b = le.AppendUint64(b, base)         // p_vaddr
	b = le.AppendUint64(b, base)         // p_paddr
	b = le.AppendUint64(b, uint64(size)) // p_filesz
	b = le.AppendUint64(b, uint64(size)) // p_memsz
	b = le.AppendUint64(b, 0x1000)       // p_align
	return append(b, code...)
}

// TestTinyELF checks tinyELFBytes with debug/elf on every platform, so the
// Linux check that runs it starts a well-formed executable.
func TestTinyELF(t *testing.T) {
	b := tinyELFBytes()
	f, err := elf.NewFile(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	if f.Class != elf.ELFCLASS64 || f.Data != elf.ELFDATA2LSB || f.Machine != elf.EM_X86_64 || f.Type != elf.ET_EXEC || len(f.Progs) != 1 {
		t.Fatalf("header: %+v, %d program headers", f.FileHeader, len(f.Progs))
	}
	p := f.Progs[0]
	if p.Type != elf.PT_LOAD || p.Off != 0 || p.Vaddr != 0x400000 || p.Filesz != uint64(len(b)) || p.Memsz != uint64(len(b)) || f.Entry != 0x400000+120 {
		t.Fatalf("segment %+v, entry %#x, size %d", p.ProgHeader, f.Entry, len(b))
	}
	if len(b) != 129 || !bytes.Equal(b[120:], []byte{0xb8, 0x3c, 0, 0, 0, 0x31, 0xff, 0x0f, 0x05}) {
		t.Fatalf("%d bytes, code % x", len(b), b[120:])
	}
}
