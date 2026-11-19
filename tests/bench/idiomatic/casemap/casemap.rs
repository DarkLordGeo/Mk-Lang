// casemap, Rust, idiomatic std: the task of README.md with
// str::to_lowercase (Unicode's full lower-case mapping, which on this
// text's alphabet, no U+0130 and no sigma, maps one scalar to one, as Mk's
// and Go's do), on each line of the generated mixed-script text
// (str::lines), each result a String dropped after it is hashed. The text
// is built once with String::push from splitmix64. Usage: casemap <kib>
// <rounds> <seed>.
const RANGES: [(u32, u32); 11] = [
    (0x41, 0x5A),
    (0x61, 0x7A),
    (0xC0, 0xD6),
    (0xD8, 0xDE),
    (0xE0, 0xF6),
    (0xF8, 0xFE),
    (0x391, 0x3A1),
    (0x3A4, 0x3A9),
    (0x3B1, 0x3C1),
    (0x3C4, 0x3C9),
    (0x410, 0x44F),
];

fn mix(mut z: u64) -> u64 {
    z = (z ^ (z >> 30)).wrapping_mul(0xbf58476d1ce4e5b9);
    z = (z ^ (z >> 27)).wrapping_mul(0x94d049bb133111eb);
    z ^ (z >> 31)
}

fn gen_text(bytes: usize, seed: u64) -> String {
    let mut tab: Vec<char> = Vec::new();
    for (lo, hi) in RANGES {
        for c in lo..=hi {
            tab.push(char::from_u32(c).unwrap());
        }
    }
    let mut t = String::new();
    let mut st = seed;
    while t.len() < bytes {
        st = st.wrapping_add(0x9e3779b97f4a7c15);
        let letters = (mix(st) % 12) as usize + 1;
        for _ in 0..letters {
            st = st.wrapping_add(0x9e3779b97f4a7c15);
            t.push(tab[(mix(st) % tab.len() as u64) as usize]);
        }
        st = st.wrapping_add(0x9e3779b97f4a7c15);
        t.push(if mix(st) % 12 == 0 { '\n' } else { ' ' });
    }
    t.push('\n');
    t
}

fn round(text: &str) -> u64 {
    let mut h: u64 = 0xcbf29ce484222325;
    let mut n: u64 = 0;
    for line in text.lines() {
        let lo = line.to_lowercase();
        for &c in lo.as_bytes() {
            h = (h ^ c as u64).wrapping_mul(0x100000001b3);
        }
        n = n.wrapping_add(lo.len() as u64);
    }
    h.wrapping_add(n)
}

fn main() {
    let a: Vec<String> = std::env::args().collect();
    if a.len() != 4 {
        eprintln!("usage: casemap <kib> <rounds> <seed>");
        std::process::exit(2);
    }
    let kib: usize = a[1].parse().unwrap();
    let rounds: u64 = a[2].parse().unwrap();
    let seed: u64 = a[3].parse().unwrap();
    let text = gen_text(kib * 1024, seed);
    let mut total: u64 = 0;
    for _ in 0..rounds {
        total = total.wrapping_add(round(&text));
    }
    println!("{}", total);
}
