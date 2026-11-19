// pipepair, Rust, std::io::pipe: one process, one pipe; a thread writes n
// bytes of a splitmix64 stream (each word's 8 bytes, little endian, from
// state 0) to the PipeWriter with write_all, 64 KiB at a time, then drops
// it; the main thread reads the PipeReader into a 64 KiB buffer and
// prints the bytes read and their sum. Usage: pipepair <n>.
use std::io::{Read, Write};

const CHUNK: usize = 65536;

fn fill(b: &mut [u8], mut x: u64) -> u64 {
    for w in b.chunks_exact_mut(8) {
        x = x.wrapping_add(0x9e3779b97f4a7c15);
        let mut z = x;
        z = (z ^ (z >> 30)).wrapping_mul(0xbf58476d1ce4e5b9);
        z = (z ^ (z >> 27)).wrapping_mul(0x94d049bb133111eb);
        z ^= z >> 31;
        w.copy_from_slice(&z.to_le_bytes());
    }
    x
}

fn main() {
    let n: usize = std::env::args().nth(1).unwrap().parse().unwrap();
    let (mut r, mut w) = std::io::pipe().unwrap();
    let t = std::thread::spawn(move || {
        let mut b = vec![0u8; CHUNK];
        fill(&mut b, 0);
        let mut left = n;
        while left > 0 {
            let k = left.min(CHUNK);
            w.write_all(&b[..k]).unwrap();
            left -= k;
        }
    });
    let mut b = vec![0u8; CHUNK];
    let (mut total, mut sum) = (0u64, 0u64);
    loop {
        let k = r.read(&mut b).unwrap();
        if k == 0 {
            break;
        }
        total += k as u64;
        for &v in &b[..k] {
            sum = sum.wrapping_add(v as u64);
        }
    }
    t.join().unwrap();
    println!("{} {}", total, sum);
}
