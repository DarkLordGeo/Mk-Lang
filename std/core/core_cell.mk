// Why: some state must change through a shared reference (a counter in a
// structure many places read); Cell gives that for plain values without a
// lock or an atomic.
//
// What: Cell[T: Plain] with New, Load and Store.
//
// How: T is stored inline. Load and Store read and write through &self with
// raw pointers; a write through a raw pointer to memory inside a Cell is
// defined while only shared loans cover it. T is plain, so nothing is
// dropped on Store and no reference into the cell can be handed out.

import std.raw

/// A plain value that can be read and replaced through a shared reference,
/// with Load and Store, without a lock or an atomic. A Cell is not Share, so
/// a &Cell[T] cannot cross to another task: use Mutex, Chan or std.atomic
/// there.
export struct Cell[T: Plain] {
    value: T
}

/// A cell holding x. O(1); allocates nothing.
export Cell[T].New(x: T): Cell[T] {
    return Cell[T](value: x)
}

/// The value in the cell. O(1).
export Cell[T].Load(&self): T {
    unsafe "a Cell's memory is written only through Store" {
        return raw.Read(raw.Of(&self.value))
    }
}

/// Puts x in the cell; the old value, which is plain, is overwritten. O(1).
export Cell[T].Store(&self, x: T into self) {
    unsafe "a write inside a Cell under shared loans is defined" {
        raw.Write(raw.Of(&self.value), x)
    }
}
