// encoding-utf16enc, Rust, idiomatic std: the line of README.md repeated n
// times, encoded to UTF-16 rounds times with str::encode_utf16 into one
// Vec<u16> reused each round (byte i of the little-endian form is read from
// unit i / 2). The checksum adds, per round, the encoding's length in bytes
// and one byte of it at a moving index.
// Usage: encoding-utf16enc <n> <rounds>.
const LINE: &str = "Hello, w\u{f6}rld! \u{41f}\u{440}\u{438}\u{432}\u{435}\u{442} \u{43c}\u{438}\u{440}. \u{6c34}\u{306f}\u{51b7}\u{305f}\u{3044} \u{1F600}\n";

fn main() {
    let args: Vec<String> = std::env::args().collect();
    let n: usize = args[1].parse().unwrap();
    let rounds: usize = args[2].parse().unwrap();
    let text = LINE.repeat(n);
    let mut out: Vec<u16> = Vec::new();
    let mut total: u64 = 0;
    for r in 0..rounds {
        out.clear();
        out.extend(text.encode_utf16());
        let m = 2 * out.len();
        let k = (r * 7919) % m;
        let byte = (out[k / 2] >> (8 * (k & 1))) as u8;
        total = total.wrapping_add(m as u64).wrapping_add(byte as u64);
    }
    println!("{}", total);
}
