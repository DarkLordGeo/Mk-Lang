import std.mmio

struct Pair {
    a: u32
    b: u32
}

Both(p: *Pair): Pair {
    unsafe "p is a device register pair" {
        return mmio.Load(p) // <- E0701
    }
}

Level(p: *f64, x: f64) {
    unsafe "p is a device register" {
        mmio.Store(p, x) // <- E0701
    }
}

export Use() {
}
