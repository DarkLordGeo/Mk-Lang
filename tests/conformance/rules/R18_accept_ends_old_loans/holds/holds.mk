// A holder of one reference. Clear is a primitive (`ends self` with a store
// into self), so this module is listed in unsafe_modules (05 R18).
export view struct O {
    p: ?&i32
}

export Empty(): O {
    return O(p: none)
}

export O.Hold(mut self, x: &i32 into self) {
    self.p = some(x)
}

export O.Clear(mut self, x: &i32 into self) ends self {
    self.p = some(x)
}

export O.Read(&self): int {
    match self.p {
        some(r) {
            return int(*r)
        }
        none {
            return 0
        }
    }
}
