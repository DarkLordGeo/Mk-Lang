extern Tick(n: i32): i32

export Next(n: i32): i32 {
    unsafe "Tick has no preconditions" {
        return Tick(n)
    }
}
