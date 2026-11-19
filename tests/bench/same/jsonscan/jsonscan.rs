// jsonscan, Rust: the reference algorithm of README.md.
// The file is read once with std::fs::read into a Vec<u8>, then scanned
// rounds times through &[u8] indexed with b[i] (bounds checked; the optimizer
// may remove the checks it proves). Indices are usize (the type Rust indexes
// with), the string length l is i64 like every column's; the token hash is u64 with wrapping_* (overflow class "wraps"), the same in "rust"
// and "rust-ovf". The if chain is in the README's order; no match. No
// unsafe, no crates.

fn scan(b: &[u8], h0: u64) -> u64 {
    let mut h = h0;
    let n = b.len();
    let mut i = 0usize;
    while i < n {
        let c = b[i];
        if c == b' ' || c == b'\n' || c == b'\r' || c == b'\t' {
            i += 1;
        } else if c == b'"' {
            i += 1;
            let mut l: i64 = 0;
            while i < n && b[i] != b'"' {
                if b[i] == b'\\' {
                    i += 1;
                }
                i += 1;
                l += 1;
            }
            i += 1;
            h = h.wrapping_mul(31).wrapping_add(l as u64);
        } else if c == b'-' || (c >= b'0' && c <= b'9') {
            let mut neg: u64 = 0;
            if c == b'-' {
                neg = 1;
                i += 1;
            }
            let mut v: u64 = 0;
            while i < n && ((b[i] >= b'0' && b[i] <= b'9') || b[i] == b'.') {
                if b[i] != b'.' {
                    v = v.wrapping_mul(10).wrapping_add((b[i] - b'0') as u64);
                }
                i += 1;
            }
            h = h.wrapping_mul(31).wrapping_add(v.wrapping_mul(2).wrapping_add(neg));
        } else if c == b't' {
            h = h.wrapping_mul(31).wrapping_add(1);
            i += 4;
        } else if c == b'f' {
            h = h.wrapping_mul(31).wrapping_add(2);
            i += 5;
        } else if c == b'n' {
            h = h.wrapping_mul(31).wrapping_add(3);
            i += 4;
        } else {
            h = h.wrapping_mul(31).wrapping_add(c as u64);
            i += 1;
        }
    }
    h
}

fn main() {
    let a: Vec<String> = std::env::args().collect();
    if a.len() != 3 {
        eprintln!("usage: jsonscan <file> <rounds>");
        std::process::exit(2);
    }
    let b = std::fs::read(&a[1]).unwrap();
    let rounds: usize = a[2].parse().unwrap();
    let mut total: u64 = 0;
    for _ in 0..rounds {
        total = scan(&b, total);
    }
    println!("{}", total);
}
