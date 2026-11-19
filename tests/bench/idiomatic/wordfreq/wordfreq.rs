// wordfreq, Rust, idiomatic std: the task of README.md. std::fs::read and
// std::str::from_utf8 once; each round splits the text with
// str::split_whitespace and counts every word in a HashMap<&str, u64> with
// the default hasher (RandomState: SipHash-1-3 under a random per-process
// key, DoS resistant), through entry().or_insert(0); the keys borrow the
// text, and the map grows from empty. Then the checksum over the entries:
// sum of FNV-1a 64 of the word times its count, plus the number of distinct
// words, all wrapping. Usage: wordfreq <file> <rounds>.
use std::collections::HashMap;

fn fnv(w: &str) -> u64 {
    let mut h: u64 = 0xcbf29ce484222325;
    for &c in w.as_bytes() {
        h = (h ^ c as u64).wrapping_mul(0x100000001b3);
    }
    h
}

fn round(text: &str) -> u64 {
    let mut m: HashMap<&str, u64> = HashMap::new();
    for w in text.split_whitespace() {
        *m.entry(w).or_insert(0) += 1;
    }
    let mut sum: u64 = 0;
    for (k, v) in &m {
        sum = sum.wrapping_add(fnv(k).wrapping_mul(*v));
    }
    sum.wrapping_add(m.len() as u64)
}

fn main() {
    let a: Vec<String> = std::env::args().collect();
    if a.len() != 3 {
        eprintln!("usage: wordfreq <file> <rounds>");
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
