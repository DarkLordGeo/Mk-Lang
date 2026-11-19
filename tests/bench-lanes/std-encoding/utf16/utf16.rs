// encoding-utf16, Rust, idiomatic std: the UTF-16LE form of the line of
// README.md repeated n times, decoded to UTF-8 rounds times with
// char::decode_utf16 over the units read from the bytes, into one String
// reused each round. The checksum adds, per round, the text's length and
// one byte of it at a moving index.
// Usage: encoding-utf16 <n> <rounds>.
const LINE: &str = "Hello, w\u{f6}rld! \u{41f}\u{440}\u{438}\u{432}\u{435}\u{442} \u{43c}\u{438}\u{440}. \u{6c34}\u{306f}\u{51b7}\u{305f}\u{3044} \u{1F600}\n";

fn main() {
    let args: Vec<String> = std::env::args().collect();
    let n: usize = args[1].parse().unwrap();
    let rounds: usize = args[2].parse().unwrap();
    let text = LINE.repeat(n);
    let input: Vec<u8> = text.encode_utf16().flat_map(|u| u.to_le_bytes()).collect();
    let mut out = String::new();
    let mut total: u64 = 0;
    for r in 0..rounds {
        out.clear();
        let units = input.chunks_exact(2).map(|p| u16::from_le_bytes([p[0], p[1]]));
        out.extend(char::decode_utf16(units).map(|c| c.unwrap_or(char::REPLACEMENT_CHARACTER)));
        let b = out.as_bytes();
        total = total.wrapping_add(b.len() as u64).wrapping_add(b[(r * 7919) % b.len()] as u64);
    }
    println!("{}", total);
}
