extern var k_count: u32

export Read(): u32 {
    unsafe "the C library defines k_count as a plain u32" {
        return k_count
    }
}
