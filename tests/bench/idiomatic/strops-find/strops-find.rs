// strops-find, Rust, idiomatic std: the task of README.md with
// str::find(&str) (the Two-Way searcher) on &line[p..], over str::lines.
// The file is read once with std::fs::read and checked by
// std::str::from_utf8. Usage: strops-find <file> <rounds>.
const NEEDLES: [&str; 8] = [
    "the",
    "ing",
    "qzx",
    "e a",
    "tion",
    "s an d",
    "abcdefgh",
    "xylophone quartz",
];

fn round(text: &str) -> u64 {
    let mut total: u64 = 0;
    for line in text.lines() {
        for nd in NEEDLES {
            let mut p = 0;
            while let Some(q) = line[p..].find(nd) {
                let at = p + q;
                total = total.wrapping_add(at as u64 + 1);
                p = at + nd.len();
            }
        }
    }
    total
}

fn main() {
    let a: Vec<String> = std::env::args().collect();
    if a.len() != 3 {
        eprintln!("usage: strops-find <file> <rounds>");
        std::process::exit(2);
    }
    let b = std::fs::read(&a[1]).unwrap();
    let text = std::str::from_utf8(&b).unwrap();
    let rounds: u64 = a[2].parse().unwrap();
    let mut total: u64 = 0;
    for _ in 0..rounds {
        total = total.wrapping_add(round(text));
    }
    println!("{}", total);
}
