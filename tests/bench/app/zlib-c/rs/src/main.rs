// zlib-c, Rust, eco: the task of README.md with flate2 1.1.1 (its default
// rust_backend, miniz_oxide 0.8.8, pure Rust), write::ZlibEncoder (the zlib
// framing of RFC 1950) at Compression::new(6). The input file is read in
// 64 KiB chunks, each chunk is written to the encoder, which writes through
// a BufWriter of 64 KiB over the output file, and finish writes the final
// block and the Adler-32 trailer. The program prints the count of input
// bytes.
// Usage: zlibc <in> <out>.
use flate2::Compression;
use flate2::write::ZlibEncoder;
use std::fs::File;
use std::io::{BufWriter, Read, Write};
use std::process::exit;

fn die(what: &str, e: std::io::Error) -> ! {
    eprintln!("zlibc: {what}: {e}");
    exit(1);
}

fn main() {
    let args: Vec<String> = std::env::args().collect();
    if args.len() != 3 {
        eprintln!("usage: zlibc <in> <out>");
        exit(2);
    }
    let mut src = File::open(&args[1]).unwrap_or_else(|e| die("open the input", e));
    let dst = File::create(&args[2]).unwrap_or_else(|e| die("create the output", e));
    let mut enc = ZlibEncoder::new(BufWriter::with_capacity(64 << 10, dst), Compression::new(6));
    let mut buf = vec![0u8; 64 << 10];
    let mut total: u64 = 0;
    loop {
        let n = src.read(&mut buf).unwrap_or_else(|e| die("read", e));
        if n == 0 {
            break;
        }
        enc.write_all(&buf[..n]).unwrap_or_else(|e| die("write", e));
        total += n as u64;
    }
    let w = enc.finish().unwrap_or_else(|e| die("finish", e));
    w.into_inner().unwrap_or_else(|e| die("flush", e.into_error()));
    println!("{total}");
}
