// Why: a driver talks to a device through memory-mapped registers, and
// every access must happen exactly as written: never merged, split,
// elided or reordered.
//
// What: Load, Store, Fence and Reg[T] (New, Read, Write): volatile access
// for memory-mapped devices.
//
// How: every function is bodiless, an intrinsic compiled inline as one
// access of exactly T's width (load volatile, store volatile, fence
// seq_cst), never merged, split, elided or reordered against another
// volatile access. Every call needs unsafe (E1101), T is an integer type,
// int, uint or a raw pointer (E0701), and the evaluator refuses them
// (E0901, refused: volatile). Layer 0: a freestanding program may import it.
// Port I/O is asm (in, out).

/// Load reads *p as one volatile access of exactly T's width, never merged,
/// split, elided or reordered against another volatile access. It needs unsafe
/// (E1101); T is an integer type, int, uint or a raw pointer (E0701). Not
/// available at compile time (E0901).
export Load[T: Plain](p: *T): T

/// Store writes x to *p as one volatile access of exactly T's width, never
/// merged, split, elided or reordered against another volatile access. It needs
/// unsafe (E1101); T is an integer type, int, uint or a raw pointer (E0701).
/// Not available at compile time (E0901).
export Store[T: Plain](p: *T, x: T)

/// Fence orders every memory access before it against every one after it, as a
/// sequentially consistent fence. It needs unsafe (E1101).
export Fence()

/// Reg is a device register of type T at a fixed address. Each of its methods
/// needs unsafe (E1101), as Load and Store do.
export struct Reg[T: Plain] {
    p: *T
}

/// New is the register at address addr.
export Reg[T].New(addr: uint): Reg[T]

/// Read is one volatile load of the register, as Load.
export Reg[T].Read(&self): T

/// Write is one volatile store of x to the register, as Store.
export Reg[T].Write(&self, x: T)
