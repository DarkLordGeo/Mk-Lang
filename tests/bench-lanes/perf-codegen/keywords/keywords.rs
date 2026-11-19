// keywords, Rust: README.md's keyword classifier, a chain of str == tests
// against 16 literals per word, as a lexer's keyword match. The words are
// &str views of one String's buffer: the bytes go into a Vec<u8> by a byte
// loop as Mk's, are checked once by std::str::from_utf8 (Mk's str.From),
// and each word is &text[a..b], which checks both offsets' bounds, the range
// and that a and b are char boundaries, as Mk's text[a..b] does. n, rounds,
// the offsets, the lengths and the loop indices are i64 (Mk's int), cast to
// usize where they index, so i % 1000 is a signed remainder as in Mk; the
// splitmix64 state, the classes and the checksum are u64 with wrapping_*
// (overflow class "wraps"), the same in "rust" and "rust-ovf". No unsafe, no
// crates.

fn mix(mut z: u64) -> u64 {
    z = (z ^ (z >> 30)).wrapping_mul(0xbf58476d1ce4e5b9);
    z = (z ^ (z >> 27)).wrapping_mul(0x94d049bb133111eb);
    z ^ (z >> 31)
}

const KEYWORDS: [&str; 16] = [
    "if", "else", "for", "while", "return", "break", "continue", "fn", "let", "var", "const", "match", "struct",
    "enum", "import", "export",
];

// classify is 1 + the index of the keyword w equals, or 0.
fn classify(w: &str) -> u64 {
    if w == "if" {
        return 1;
    }
    if w == "else" {
        return 2;
    }
    if w == "for" {
        return 3;
    }
    if w == "while" {
        return 4;
    }
    if w == "return" {
        return 5;
    }
    if w == "break" {
        return 6;
    }
    if w == "continue" {
        return 7;
    }
    if w == "fn" {
        return 8;
    }
    if w == "let" {
        return 9;
    }
    if w == "var" {
        return 10;
    }
    if w == "const" {
        return 11;
    }
    if w == "match" {
        return 12;
    }
    if w == "struct" {
        return 13;
    }
    if w == "enum" {
        return 14;
    }
    if w == "import" {
        return 15;
    }
    if w == "export" {
        return 16;
    }
    0
}

fn main() {
    let a: Vec<String> = std::env::args().collect();
    if a.len() != 4 {
        eprintln!("usage: keywords <n> <rounds> <seed>");
        std::process::exit(2);
    }
    let n: i64 = a[1].parse().unwrap();
    let rounds: i64 = a[2].parse().unwrap();
    let mut st: u64 = a[3].parse().unwrap();
    let mut buf: Vec<u8> = vec![0; (n * 8) as usize];
    let mut offs: Vec<i64> = vec![0; (n + 1) as usize];
    let mut p: i64 = 0;
    for i in 0..n {
        offs[i as usize] = p;
        st = st.wrapping_add(0x9e3779b97f4a7c15);
        let z = mix(st);
        let kind = z % 4;
        if kind < 2 {
            let k = KEYWORDS[((z >> 8) % 16) as usize].as_bytes();
            for j in 0..k.len() {
                buf[p as usize] = k[j];
                p += 1;
            }
            if kind == 1 {
                buf[(p - 1) as usize] = (0x61 + (z >> 16) % 26) as u8;
            }
        } else {
            let l = 1 + ((z >> 8) % 8) as i64;
            for _ in 0..l {
                st = st.wrapping_add(0x9e3779b97f4a7c15);
                buf[p as usize] = (0x61 + mix(st) % 26) as u8;
                p += 1;
            }
        }
    }
    offs[n as usize] = p;
    let text = std::str::from_utf8(&buf[..p as usize]).unwrap();
    let mut total: u64 = 0;
    for _ in 0..rounds {
        for i in 0..n {
            let w = &text[offs[i as usize] as usize..offs[(i + 1) as usize] as usize];
            total = total.wrapping_add(classify(w).wrapping_mul((i % 1000 + 1) as u64));
        }
    }
    println!("{}", total);
}
