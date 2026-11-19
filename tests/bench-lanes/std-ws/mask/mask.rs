// ws-mask, Rust: RFC 6455 5.3's masking as std.websocket's Mask does it, a
// scalar head to phase 0, 16 bytes a step with std::arch's SSE2
// (_mm_xor_si128 on _mm_loadu_si128 and _mm_storeu_si128, each pointer
// taken from a checked 16-byte slice), a scalar tail. n bytes of
// splitmix64 output (seed 1) in a buffer that starts on a 64-byte
// boundary (BS-4), rounds times masked in place with round r's key (the
// four low bytes of (r + 1) * 0x9E3779B97F4A7C15) from phase r % 4; the
// checksum adds, per round, the byte at (r * 7919) % n and the returned
// phase. Wrapping operators where the task wraps.
// Usage: ws-mask <n> <rounds>.
use std::arch::x86_64::{__m128i, _mm_loadu_si128, _mm_set1_epi32, _mm_storeu_si128, _mm_xor_si128};
use std::env;

fn mask(b: &mut [u8], key: [u8; 4], phase: usize) -> usize {
    let n = b.len();
    let mut i = 0;
    let mut p = phase & 3;
    while i < n && p != 0 {
        b[i] ^= key[p];
        p = (p + 1) & 3;
        i += 1;
    }
    if n - i >= 16 {
        // SSE2 is part of x86-64, so every variant's target has it.
        unsafe {
            let k = _mm_set1_epi32(i32::from_ne_bytes(key));
            while n - i >= 16 {
                let s = &mut b[i..i + 16];
                let v = _mm_loadu_si128(s.as_ptr() as *const __m128i);
                _mm_storeu_si128(s.as_mut_ptr() as *mut __m128i, _mm_xor_si128(v, k));
                i += 16;
            }
        }
    }
    while i < n {
        b[i] ^= key[p];
        p = (p + 1) & 3;
        i += 1;
    }
    p
}

fn main() {
    let args: Vec<String> = env::args().collect();
    if args.len() != 3 {
        eprintln!("usage: ws-mask <n> <rounds>");
        std::process::exit(2);
    }
    let n: usize = args[1].parse().unwrap();
    let rounds: usize = args[2].parse().unwrap();
    // BS-4: the buffer starts on a 64-byte boundary in every column.
    let mut bv = vec![0u8; n + 64];
    let off = bv.as_ptr().align_offset(64);
    let data = &mut bv[off..off + n];
    if data.as_ptr() as usize % 64 != 0 {
        eprintln!("ws-mask: buffer not 64-byte aligned");
        std::process::exit(3);
    }
    let mut st: u64 = 1;
    let mut i = 0;
    while i < n {
        st = st.wrapping_add(0x9E3779B97F4A7C15);
        let mut z = st;
        z = (z ^ (z >> 30)).wrapping_mul(0xBF58476D1CE4E5B9);
        z = (z ^ (z >> 27)).wrapping_mul(0x94D049BB133111EB);
        z ^= z >> 31;
        let mut k = 0;
        while k < 8 && i < n {
            data[i] = (z >> (8 * k)) as u8;
            k += 1;
            i += 1;
        }
    }
    let mut total: u64 = 0;
    for r in 0..rounds {
        let kw = (r as u64).wrapping_add(1).wrapping_mul(0x9E3779B97F4A7C15);
        let key = [kw as u8, (kw >> 8) as u8, (kw >> 16) as u8, (kw >> 24) as u8];
        let p = mask(data, key, r % 4);
        total = total.wrapping_add(data[(r * 7919) % n] as u64).wrapping_add(p as u64);
    }
    println!("{}", total);
}
