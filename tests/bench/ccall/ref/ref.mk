extern RefAdd(a: i64, b: i64): i64
extern RefAddLoop(n: i64): i64
extern RefStack(kib: i64): i64

export Add(a: i64, b: i64): i64 {
    unsafe "RefAdd is in ccall.ref.ll, linked into the benchmark build; it reads its two integers and nothing else" {
        return RefAdd(a, b)
    }
}

export AddLoop(n: i64): i64 {
    unsafe "RefAddLoop is in ccall.ref.ll; it makes n direct C calls of RefAdd and touches no memory of the caller" {
        return RefAddLoop(n)
    }
}

export Stack(kib: i64): i64 {
    unsafe "RefStack is in ccall.ref.ll; it uses kib KiB of the stack it runs on (a positive multiple of 4) and touches no memory of the caller" {
        return RefStack(kib)
    }
}
