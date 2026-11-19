// strops-split, Rust, idiomatic std: the task of README.md with str::split('e')
// (empty pieces kept), str::trim on each piece, and str::split_whitespace
// over the text. The file is read once with std::fs::read and checked by
// std::str::from_utf8. Usage: strops-split <file> <rounds>.
fn round(text: &str) -> u64 {
    let mut total: u64 = 0;
    let mut h: u64 = 0xcbf29ce484222325;
    for piece in text.split('e') {
        let t = piece.trim();
        total = total.wrapping_add(1);
        h = (h ^ t.len() as u64).wrapping_mul(0x100000001b3);
    }
    total = total.wrapping_add(h);
    for w in text.split_whitespace() {
        total = total.wrapping_add(w.len() as u64 + 1);
    }
    total
}

fn main() {
    let a: Vec<String> = std::env::args().collect();
    if a.len() != 3 {
        eprintln!("usage: strops-split <file> <rounds>");
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
