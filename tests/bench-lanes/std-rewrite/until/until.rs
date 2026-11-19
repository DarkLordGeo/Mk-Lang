// until, Rust: split a file into records through BufReader::with_capacity
// (64 KiB) over a File with BufRead::read_until into a reused Vec, once on
// b'\n' and once on b' ', r times; prints the records and the bytes taken
// (each record with its delimiter). Usage: until <file> <r>.
use std::fs::File;
use std::io::{BufRead, BufReader};

fn split(p: &str, delim: u8, rec: &mut Vec<u8>) -> (i64, i64) {
    let f = File::open(p).expect("open");
    let mut r = BufReader::with_capacity(65536, f);
    let (mut n, mut bytes) = (0i64, 0i64);
    loop {
        rec.clear();
        let k = r.read_until(delim, rec).expect("read");
        if k == 0 {
            break;
        }
        n += 1;
        bytes += k as i64;
    }
    (n, bytes)
}

fn main() {
    let args: Vec<String> = std::env::args().collect();
    if args.len() != 3 {
        eprintln!("usage: until <file> <r>");
        std::process::exit(2);
    }
    let reps: usize = args[2].parse().expect("r");
    let mut rec = Vec::with_capacity(65536);
    let (mut n, mut bytes) = (0i64, 0i64);
    for _ in 0..reps {
        for d in [b'\n', b' '] {
            let (a, b) = split(&args[1], d, &mut rec);
            n += a;
            bytes += b;
        }
    }
    println!("{} {}", n, bytes);
}
