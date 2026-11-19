// verdict: reject E1101
extern Getpid(): i32

Pid(): i32 {
    return Getpid() // <- E1101
}
