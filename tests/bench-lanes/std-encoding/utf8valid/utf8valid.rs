// encoding-utf8valid, Rust, idiomatic std: the line of README.md repeated n
// times, decoded rounds times by String::from_utf8_lossy into an owned
// String (into_owned: the input is valid, so the lossy call borrows it and
// the String is its copy). The checksum adds, per round, the text's length
// and one byte of it at a moving index.
// Usage: encoding-utf8valid <n> <rounds>.
const LINE: &str = "Hello, w\u{f6}rld! \u{41f}\u{440}\u{438}\u{432}\u{435}\u{442} \u{43c}\u{438}\u{440}. \u{6c34}\u{306f}\u{51b7}\u{305f}\u{3044} \u{1F600}\n";

fn main() {
    let args: Vec<String> = std::env::args().collect();
    let n: usize = args[1].parse().unwrap();
    let rounds: usize = args[2].parse().unwrap();
    let b: Vec<u8> = LINE.repeat(n).into_bytes();
    let mut total: u64 = 0;
    for r in 0..rounds {
        let out: String = String::from_utf8_lossy(&b).into_owned();
        let o = out.as_bytes();
        total = total.wrapping_add(o.len() as u64).wrapping_add(o[(r * 7919) % o.len()] as u64);
    }
    println!("{}", total);
}
