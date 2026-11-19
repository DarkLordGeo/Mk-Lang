// ws-extend, Rust: Vec<u8>::extend_from_slice on the global allocator.
// n bytes of splitmix64 output (seed 1); per round r a new, empty growable
// byte list gets the n bytes appended in order, piece j of 1 + (r + j) % piece
// bytes (the last cut at n), then is freed; the checksum adds, per round,
// the byte at (r * 7919) % n and the number of pieces. Usage: ws-extend <n>
// <piece> <rounds>.
use std::env;

fn main() {
    let args: Vec<String> = env::args().collect();
    if args.len() != 4 {
        eprintln!("usage: ws-extend <n> <piece> <rounds>");
        std::process::exit(2);
    }
    let n: usize = args[1].parse().unwrap();
    let piece: usize = args[2].parse().unwrap();
    let rounds: usize = args[3].parse().unwrap();
    let mut data = vec![0u8; n];
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
        let mut v: Vec<u8> = Vec::new();
        let mut at = 0;
        let mut j = 0;
        while at < n {
            let k = (1 + (r + j) % piece).min(n - at);
            v.extend_from_slice(&data[at..at + k]);
            at += k;
            j += 1;
        }
        total = total.wrapping_add(v[(r * 7919) % n] as u64).wrapping_add(j as u64);
    }
    println!("{}", total);
}
