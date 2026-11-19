// hash-sip13-short, Rust: std::hash::DefaultHasher (SipHash-1-3 under the
// key (0, 0)), a new hasher, one write and finish per 16-byte key, over the
// input and passes of mk/hash-sip13-short.mk's comment. No unsafe, no crates.
use std::hash::{DefaultHasher, Hasher};

fn mix(mut z: u64) -> u64 {
    z = (z ^ (z >> 30)).wrapping_mul(0xbf58476d1ce4e5b9);
    z = (z ^ (z >> 27)).wrapping_mul(0x94d049bb133111eb);
    z ^ (z >> 31)
}

fn main() {
    let a: Vec<String> = std::env::args().collect();
    if a.len() != 4 {
        eprintln!("usage: hash-sip13-short <mib> <reps> <seed>");
        std::process::exit(2);
    }
    let mib: usize = a[1].parse().unwrap();
    let reps: usize = a[2].parse().unwrap();
    let mut st: u64 = a[3].parse().unwrap();
    let n = mib * 1048576;
    let mut buf: Vec<u8> = vec![0; n];
    let mut i = 0;
    while i < n {
        st = st.wrapping_add(0x9e3779b97f4a7c15);
        buf[i..i + 8].copy_from_slice(&mix(st).to_le_bytes());
        i += 8;
    }
    let mut sum: u64 = 0;
    for r in 0..reps {
        let mut j = 0;
        while j < n {
            let mut h = DefaultHasher::new();
            h.write(&buf[j..j + 16]);
            sum ^= h.finish();
            j += 16;
        }
        buf[r % n] ^= sum as u8;
    }
    println!("{}", sum);
}
