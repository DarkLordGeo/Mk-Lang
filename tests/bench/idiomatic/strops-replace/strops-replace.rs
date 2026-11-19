// strops-replace, Rust, idiomatic std: the task of README.md with
// str::replace (non-overlapping, left to right, a new String each call),
// three in turn on each line of str::lines. The file is read once with
// std::fs::read and checked by std::str::from_utf8. Usage: strops-replace
// <file> <rounds>.
fn round(text: &str) -> u64 {
    let mut h: u64 = 0xcbf29ce484222325;
    let mut n: u64 = 0;
    for line in text.lines() {
        let a = line.replace("e", "EEE");
        let b = a.replace("s ", "S");
        let c = b.replace(" a", "_A");
        for &x in c.as_bytes() {
            h = (h ^ x as u64).wrapping_mul(0x100000001b3);
        }
        n = n.wrapping_add(c.len() as u64);
    }
    h.wrapping_add(n)
}

fn main() {
    let a: Vec<String> = std::env::args().collect();
    if a.len() != 3 {
        eprintln!("usage: strops-replace <file> <rounds>");
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
