// sortstr, Rust, idiomatic std: the task of README.md with
// slice::sort_unstable (ipnsort) over a Vec<&str> of 16-byte views of one
// buffer, compared by str's byte order.
const HEX: &[u8; 16] = b"0123456789abcdef";

fn mix(mut z: u64) -> u64 {
    z = (z ^ (z >> 30)).wrapping_mul(0xbf58476d1ce4e5b9);
    z = (z ^ (z >> 27)).wrapping_mul(0x94d049bb133111eb);
    z ^ (z >> 31)
}

fn unhex(s: &str) -> u64 {
    let mut v = 0u64;
    for &c in s.as_bytes() {
        let d = if c <= b'9' { c - b'0' } else { c - b'a' + 10 };
        v = (v << 4) | d as u64;
    }
    v
}

fn fold(xs: &[&str]) -> u64 {
    let n = xs.len();
    let mut sum = unhex(xs[n - 1]);
    let mut i = 0;
    while i < n {
        sum = sum.wrapping_add(unhex(xs[i]).wrapping_mul(i as u64));
        i += 1024;
    }
    sum
}

fn main() {
    let a: Vec<String> = std::env::args().collect();
    if a.len() != 4 {
        eprintln!("usage: sortstr <n> <rounds> <seed>");
        std::process::exit(2);
    }
    let n: usize = a[1].parse().unwrap();
    let rounds: u64 = a[2].parse().unwrap();
    let seed: u64 = a[3].parse().unwrap();
    let mut buf = vec![0u8; n * 16];
    let mut total: u64 = 0;
    for r in 0..rounds {
        let mut st = seed.wrapping_add(r.wrapping_mul(0x5555555555555555));
        for key in buf.chunks_exact_mut(16) {
            st = st.wrapping_add(0x9e3779b97f4a7c15);
            let z = mix(st);
            for (j, c) in key.iter_mut().enumerate() {
                *c = HEX[((z >> (60 - 4 * j)) & 15) as usize];
            }
        }
        let mut xs: Vec<&str> = buf.chunks_exact(16).map(|k| std::str::from_utf8(k).unwrap()).collect();
        xs.sort_unstable();
        total = total.wrapping_add(fold(&xs));
    }
    println!("{}", total);
}
