// verdict: reject E0603
// note: X10, a trait object with a lifetime: gen dyn.Of(I) makes a view, so DeviceDyn.Of(x) holds the exclusive loan of x and cannot outlive it
import std.dyn

interface Device {
    Write(mut self, data: []u8): int
}

gen dyn.Of(Device)

struct Uart {
    n: int
}

impl Device for Uart {
    Write(mut self, data: []u8): int {
        self.n += data.Len()
        return data.Len()
    }
}

Leak(): DeviceDyn {
    var u := Uart(n: 0)
    return DeviceDyn.Of(mut u) // <- E0603
}
