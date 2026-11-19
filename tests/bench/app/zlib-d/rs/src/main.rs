// zlib-d, Rust, eco: the task of README.md with flate2 1.1.1 (its default
// rust_backend, miniz_oxide 0.8.8, pure Rust), flate2::Decompress with the
// zlib header (RFC 1950; miniz_oxide checks the trailer's Adler-32 and
// flate2 reports a mismatch as an error). The compressed file is read
// through a BufReader of 64 KiB, each buffer is inflated into a 64 KiB
// buffer, and every buffer is written to the output file. The stream must
// end with Status::StreamEnd: flate2's Read adapters return end of input
// on a truncated stream with no error, so the program drives Decompress
// itself, as the C program drives inflate, and a stream that ends before
// its final block is an error. The program prints the count of
// decompressed bytes.
// Usage: zlibd <in> <out>.
use flate2::{Decompress, FlushDecompress, Status};
use std::fs::File;
use std::io::{BufRead, BufReader, Write};
use std::process::exit;

fn die(what: &str) -> ! {
    eprintln!("zlibd: {what}");
    exit(1);
}

fn main() {
    let args: Vec<String> = std::env::args().collect();
    if args.len() != 3 {
        eprintln!("usage: zlibd <in> <out>");
        exit(2);
    }
    let src = File::open(&args[1]).unwrap_or_else(|e| die(&format!("open the input: {e}")));
    let mut dst = File::create(&args[2]).unwrap_or_else(|e| die(&format!("create the output: {e}")));
    let mut r = BufReader::with_capacity(64 << 10, src);
    let mut z = Decompress::new(true);
    let mut out = vec![0u8; 64 << 10];
    loop {
        let input = r.fill_buf().unwrap_or_else(|e| die(&format!("read: {e}")));
        let eof = input.is_empty();
        let (before_in, before_out) = (z.total_in(), z.total_out());
        let status = z
            .decompress(input, &mut out, FlushDecompress::None)
            .unwrap_or_else(|e| die(&format!("inflate: {e}")));
        let used = (z.total_in() - before_in) as usize;
        let made = (z.total_out() - before_out) as usize;
        r.consume(used);
        dst.write_all(&out[..made]).unwrap_or_else(|e| die(&format!("write: {e}")));
        match status {
            Status::StreamEnd => break,
            _ if eof && made == 0 => die("the stream is truncated"),
            _ => {}
        }
    }
    println!("{}", z.total_out());
}
