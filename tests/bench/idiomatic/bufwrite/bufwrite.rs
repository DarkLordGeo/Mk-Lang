// bufwrite, Rust, idiomatic std: the task of README.md with
// File::create in std::env::temp_dir, BufWriter::with_capacity(65536),
// writeln! of each line ("{} {:x} {}"), flush; then std::fs::read of the
// file, FNV-1a over its bytes, and std::fs::remove_file. Usage: bufwrite
// <n>.
use std::fs::File;
use std::io::{BufWriter, Write};

fn main() {
    let a: Vec<String> = std::env::args().collect();
    if a.len() != 2 {
        eprintln!("usage: bufwrite <n>");
        std::process::exit(2);
    }
    let n: u64 = a[1].parse().unwrap();
    let p = std::env::temp_dir().join("mkbench-bufwrite-rust.txt");
    {
        let f = File::create(&p).unwrap();
        let mut w = BufWriter::with_capacity(65536, f);
        for u in 0..n {
            let h = u.wrapping_mul(0x9e3779b97f4a7c15);
            writeln!(w, "{} {:x} {}", u, h, u * 7).unwrap();
        }
        w.flush().unwrap();
    }
    let b = std::fs::read(&p).unwrap();
    let mut h: u64 = 0xcbf29ce484222325;
    for &c in &b {
        h = (h ^ c as u64).wrapping_mul(0x100000001b3);
    }
    let total = (b.len() as u64).wrapping_add(h);
    std::fs::remove_file(&p).unwrap();
    println!("{}", total);
}
