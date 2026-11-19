// Why: Print, ==, sorting and hash maps work on primitive values out of the
// box only because std.core gives every primitive the prelude interfaces.
//
// What: the built-in impls of Format (every integer, both floats, bool,
// char, str and &T), Eq and Ord (numbers, char and str; bool gets Eq only),
// Hash (integers, bool, char, str, &T), and Format for ?T and []T.
//
// How: integers hash as U64 of the sign-extended value, so a key hashes the
// same at every width the table sees. Eq, Ord and Hash hold for &T whenever
// they hold for T, on the referent, so Eq agrees with Hash and with T's own
// order.

impl Format for i8 {
    Format(&self, w: mut Writer): WriteError!void {
        try FmtI64(w, i64(*self))
    }
}

impl Format for i16 {
    Format(&self, w: mut Writer): WriteError!void {
        try FmtI64(w, i64(*self))
    }
}

impl Format for i32 {
    Format(&self, w: mut Writer): WriteError!void {
        try FmtI64(w, i64(*self))
    }
}

impl Format for i64 {
    Format(&self, w: mut Writer): WriteError!void {
        try FmtI64(w, i64(*self))
    }
}

impl Format for u8 {
    Format(&self, w: mut Writer): WriteError!void {
        try FmtU64(w, u64(*self), false)
    }
}

impl Format for u16 {
    Format(&self, w: mut Writer): WriteError!void {
        try FmtU64(w, u64(*self), false)
    }
}

impl Format for u32 {
    Format(&self, w: mut Writer): WriteError!void {
        try FmtU64(w, u64(*self), false)
    }
}

impl Format for u64 {
    Format(&self, w: mut Writer): WriteError!void {
        try FmtU64(w, u64(*self), false)
    }
}

impl Format for int {
    Format(&self, w: mut Writer): WriteError!void {
        try FmtI64(w, i64(*self))
    }
}

impl Format for uint {
    Format(&self, w: mut Writer): WriteError!void {
        try FmtU64(w, u64(*self), false)
    }
}

impl Format for f32 {
    Format(&self, w: mut Writer): WriteError!void {
        try FmtF32(w, *self, 0)
    }
}

impl Format for f64 {
    Format(&self, w: mut Writer): WriteError!void {
        try FmtF64(w, *self, 0)
    }
}

impl Format for bool {
    Format(&self, w: mut Writer): WriteError!void {
        if *self {
            try w.Str("true")
        } else {
            try w.Str("false")
        }
    }
}

impl Format for char {
    Format(&self, w: mut Writer): WriteError!void {
        var buf: [4]u8 = [0; 4]
        n := EncodeChar(*self, mut buf)
        try w.Bytes(buf[0..n])
    }
}

impl Format for str {
    Format(&self, w: mut Writer): WriteError!void {
        try w.Str(*self)
    }
}

impl Eq for i8 {
    Eq(&self, other: &i8): bool {
        return *self == *other
    }
}

impl Eq for i16 {
    Eq(&self, other: &i16): bool {
        return *self == *other
    }
}

impl Eq for i32 {
    Eq(&self, other: &i32): bool {
        return *self == *other
    }
}

impl Eq for i64 {
    Eq(&self, other: &i64): bool {
        return *self == *other
    }
}

impl Eq for u8 {
    Eq(&self, other: &u8): bool {
        return *self == *other
    }
}

impl Eq for u16 {
    Eq(&self, other: &u16): bool {
        return *self == *other
    }
}

impl Eq for u32 {
    Eq(&self, other: &u32): bool {
        return *self == *other
    }
}

impl Eq for u64 {
    Eq(&self, other: &u64): bool {
        return *self == *other
    }
}

impl Eq for int {
    Eq(&self, other: &int): bool {
        return *self == *other
    }
}

impl Eq for uint {
    Eq(&self, other: &uint): bool {
        return *self == *other
    }
}

impl Eq for f32 {
    Eq(&self, other: &f32): bool {
        return *self == *other
    }
}

impl Eq for f64 {
    Eq(&self, other: &f64): bool {
        return *self == *other
    }
}

impl Eq for bool {
    Eq(&self, other: &bool): bool {
        return *self == *other
    }
}

impl Eq for char {
    Eq(&self, other: &char): bool {
        return *self == *other
    }
}

impl Eq for str {
    Eq(&self, other: &str): bool {
        return *self == *other
    }
}

impl Ord for i8 {
    Less(&self, other: &i8): bool {
        return *self < *other
    }
}

impl Ord for i16 {
    Less(&self, other: &i16): bool {
        return *self < *other
    }
}

impl Ord for i32 {
    Less(&self, other: &i32): bool {
        return *self < *other
    }
}

impl Ord for i64 {
    Less(&self, other: &i64): bool {
        return *self < *other
    }
}

impl Ord for u8 {
    Less(&self, other: &u8): bool {
        return *self < *other
    }
}

impl Ord for u16 {
    Less(&self, other: &u16): bool {
        return *self < *other
    }
}

impl Ord for u32 {
    Less(&self, other: &u32): bool {
        return *self < *other
    }
}

impl Ord for u64 {
    Less(&self, other: &u64): bool {
        return *self < *other
    }
}

impl Ord for int {
    Less(&self, other: &int): bool {
        return *self < *other
    }
}

impl Ord for uint {
    Less(&self, other: &uint): bool {
        return *self < *other
    }
}

impl Ord for f32 {
    Less(&self, other: &f32): bool {
        return *self < *other
    }
}

impl Ord for f64 {
    Less(&self, other: &f64): bool {
        return *self < *other
    }
}

impl Ord for char {
    Less(&self, other: &char): bool {
        return *self < *other
    }
}

impl Ord for str {
    Less(&self, other: &str): bool {
        return *self < *other
    }
}

impl Hash for i8 {
    Hash(&self, h: mut Hasher) {
        h.U64(Trunc[u64](i64(*self)))
    }
}

impl Hash for i16 {
    Hash(&self, h: mut Hasher) {
        h.U64(Trunc[u64](i64(*self)))
    }
}

impl Hash for i32 {
    Hash(&self, h: mut Hasher) {
        h.U64(Trunc[u64](i64(*self)))
    }
}

impl Hash for i64 {
    Hash(&self, h: mut Hasher) {
        h.U64(Trunc[u64](i64(*self)))
    }
}

impl Hash for int {
    Hash(&self, h: mut Hasher) {
        h.U64(Trunc[u64](i64(*self)))
    }
}

impl Hash for u8 {
    Hash(&self, h: mut Hasher) {
        h.U64(u64(*self))
    }
}

impl Hash for u16 {
    Hash(&self, h: mut Hasher) {
        h.U64(u64(*self))
    }
}

impl Hash for u32 {
    Hash(&self, h: mut Hasher) {
        h.U64(u64(*self))
    }
}

impl Hash for u64 {
    Hash(&self, h: mut Hasher) {
        h.U64(u64(*self))
    }
}

impl Hash for uint {
    Hash(&self, h: mut Hasher) {
        h.U64(u64(*self))
    }
}

impl Hash for bool {
    Hash(&self, h: mut Hasher) {
        if *self {
            h.U64(1)
        } else {
            h.U64(0)
        }
    }
}

impl Hash for char {
    Hash(&self, h: mut Hasher) {
        h.U64(u64(u32(*self)))
    }
}

// A str hashes its bytes, then one 0xFF byte, so compound keys cannot
// collide by concatenation.
impl Hash for str {
    Hash(&self, h: mut Hasher) {
        h.Bytes(self.AsBytes())
        end: [1]u8 = [0xFF]
        h.Bytes(end[..])
    }
}

// The &T impls reach T's impl through **self, a place of type T: a call on
// *self, a &T, would name the &T impl itself.
impl[T: Format] Format for &T {
    Format(&self, w: mut Writer): WriteError!void {
        try (**self).Format(w)
    }
}

// An optional prints some(v) or none, a slice and the lists print
// [a, b, c] ([] when empty), each element through its own Format, so a str
// inside prints as written, as derive.Format prints a str field. The List
// and HeapList impls are next to their types (coll_list.mk, coll_heap.mk,
// layer 1), since they call the layer-1 Items.
impl[T: Format] Format for ?T {
    Format(&self, w: mut Writer): WriteError!void {
        match self {
            some(v) {
                try w.Str("some(")
                try v.Format(w)
                try w.Str(")")
            }
            none {
                try w.Str("none")
            }
        }
    }
}

impl[T: Format] Format for []T {
    Format(&self, w: mut Writer): WriteError!void {
        try FmtElems(w, *self)
    }
}

// FmtElems writes xs as [a, b, c].
FmtElems[T: Format](w: mut Writer, xs: []T): WriteError!void {
    try w.Str("[")
    for i, x in &xs {
        if i > 0 {
            try w.Str(", ")
        }
        try x.Format(w)
    }
    try w.Str("]")
}

// Equality of references is equality of their referents.
impl[T: Eq] Eq for &T {
    Eq(&self, other: &&T): bool {
        return (**self).Eq(*other)
    }
}

// References order as their referents do.
impl[T: Ord] Ord for &T {
    Less(&self, other: &&T): bool {
        return (**self).Less(*other)
    }
}

// A reference hashes as its referent, so equal referents hash alike.
impl[T: Hash] Hash for &T {
    Hash(&self, h: mut Hasher) {
        (**self).Hash(h)
    }
}

/// Writes c's UTF-8 encoding into buf and returns its length in bytes, 1 to
/// 4. O(1).
export EncodeChar(c: char, buf: mut [4]u8): int {
    v := u32(c)
    if v < 0x80 {
        buf[0] = Trunc[u8](v)
        return 1
    }
    if v < 0x800 {
        buf[0] = Trunc[u8](0xC0 | (v >> 6))
        buf[1] = Trunc[u8](0x80 | (v & 0x3F))
        return 2
    }
    if v < 0x10000 {
        buf[0] = Trunc[u8](0xE0 | (v >> 12))
        buf[1] = Trunc[u8](0x80 | ((v >> 6) & 0x3F))
        buf[2] = Trunc[u8](0x80 | (v & 0x3F))
        return 3
    }
    buf[0] = Trunc[u8](0xF0 | (v >> 18))
    buf[1] = Trunc[u8](0x80 | ((v >> 12) & 0x3F))
    buf[2] = Trunc[u8](0x80 | ((v >> 6) & 0x3F))
    buf[3] = Trunc[u8](0x80 | (v & 0x3F))
    return 4
}
