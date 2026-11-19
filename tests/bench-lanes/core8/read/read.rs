// read, Rust: read a file through BufReader::with_capacity (64 KiB) over a
// File by Read::read calls of want bytes each, r times; prints the bytes
// read and the wrapping sum of each call's first byte. A want of at least
// the buffer reads straight into the caller's slice when the buffer is
// empty (BufReader's rule). Usage: read <file> <want> <r>.
use std::fs::File;
use std::io::{BufReader, Read};

fn pass(p: &str, d: &mut [u8]) -> (i64, u64) {
    let f = File::open(p).expect("open");
    let mut r = BufReader::with_capacity(65536, f);
    let (mut bytes, mut sum) = (0i64, 0u64);
    loop {
        let k = r.read(d).expect("read");
        if k == 0 {
            break;
        }
        bytes += k as i64;
        sum = sum.wrapping_add(d[0] as u64);
    }
    (bytes, sum)
}

fn main() {
    let args: Vec<String> = std::env::args().collect();
    if args.len() != 4 {
        eprintln!("usage: read <file> <want> <r>");
        std::process::exit(2);
    }
    let want: usize = args[2].parse().expect("want");
    let reps: usize = args[3].parse().expect("r");
    let mut d = vec![0u8; want];
    let (mut bytes, mut sum) = (0i64, 0u64);
    for _ in 0..reps {
        let (a, b) = pass(&args[1], &mut d);
        bytes += a;
        sum = sum.wrapping_add(b);
    }
    println!("{} {}", bytes, sum);
}
