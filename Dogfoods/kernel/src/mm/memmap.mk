// Why: the kernel must know which physical memory it may use.
//
// What: MemoryMap, Region, ReadStartInfo and the boot errors.
//
// How: reads the hvm_start_info QEMU's PVH loader leaves in memory
// (xen/include/public/arch-x86/hvm/start_info.h, version 1): magic,
// version, flags, module count, three addresses, then the memory map's
// address and entry count at offsets 40 and 48. Each entry is an address,
// a size and an E820 type, 24 bytes.

import std.raw

const StartMagic: u32 = 0x336EC578
const EntrySize: u64 = 24

/// The most regions the kernel keeps; QEMU reports about eight.
export const MaxRegions: int = 32

/// The loader passed no start info.
export error NoStartInfo
/// The start info does not begin with the PVH magic.
export error BadMagic(found: u32)
/// The start info is version 0, which has no memory map.
export error NoMemoryMap(version: u32)
/// The memory map has more entries than MaxRegions.
export error TooManyRegions(count: u32)

/// Why the boot memory map could not be read.
export type BootError = NoStartInfo | BadMagic | NoMemoryMap | TooManyRegions

/// One range of physical memory and its E820 type.
export struct Region {
    export start: u64
    export size: u64
    export kind: u32
}

/// The first address past the region.
export Region.End(&self): u64 {
    return self.start + self.size
}

/// Whether the kernel may hand the region's pages out: E820 type 1.
export Region.Usable(&self): bool {
    return self.kind == 1
}

/// The E820 type's name, as the ACPI specification's table 15-6 gives it.
export Region.KindName(&self): str {
    return match self.kind {
        1 { "usable" }
        2 { "reserved" }
        3 { "acpi reclaimable" }
        4 { "acpi nvs" }
        5 { "unusable" }
        7 { "persistent" }
        _ { "unknown" }
    }
}

/// The regions of the boot memory map, in the loader's order.
export struct MemoryMap {
    regions: [32]Region
    count: int
}

/// The number of regions.
export MemoryMap.Len(&self): int {
    return self.count
}

/// The region at `i`, or none past the end.
export MemoryMap.Get(&self, i: int): ?Region {
    if i < 0 || i >= self.count {
        return none
    }
    return some(self.regions[i])
}

/// The bytes of every usable region together.
export MemoryMap.UsableBytes(&self): u64 {
    var total: u64 = 0
    for r in self.regions[..self.count] {
        if r.Usable() {
            total += r.size
        }
    }
    return total
}

Read32(address: u64): u32 {
    unsafe "the start info and its map lie in the identity-mapped first GiB, where the loader wrote them" {
        return *raw.FromAddr[u32](uint(address))
    }
}

Read64(address: u64): u64 {
    unsafe "the start info and its map lie in the identity-mapped first GiB, where the loader wrote them" {
        return *raw.FromAddr[u64](uint(address))
    }
}

/// Reads the memory map from the start info at `address`. Fails with
/// NoStartInfo for a zero address, BadMagic and NoMemoryMap for a start
/// info this kernel cannot read, and TooManyRegions past MaxRegions.
/// O(regions); allocates nothing.
export ReadStartInfo(address: u64): BootError!MemoryMap {
    if address == 0 {
        fail NoStartInfo
    }
    magic := Read32(address)
    if magic != StartMagic {
        fail BadMagic(found: magic)
    }
    version := Read32(address + 4)
    if version < 1 {
        fail NoMemoryMap(version: version)
    }
    table := Read64(address + 40)
    count := Read32(address + 48)
    if table == 0 || count == 0 {
        fail NoMemoryMap(version: version)
    }
    if count > Cast[u32](MaxRegions) {
        fail TooManyRegions(count: count)
    }
    var map := MemoryMap(regions: [Region(start: 0, size: 0, kind: 0); 32], count: 0)
    for i in 0..int(count) {
        entry := table + Cast[u64](i) * EntrySize
        map.regions[i] = Region(start: Read64(entry), size: Read64(entry + 8), kind: Read32(entry + 16))
    }
    map.count = int(count)
    return map
}
