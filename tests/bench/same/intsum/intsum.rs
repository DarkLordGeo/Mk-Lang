// intsum, Rust: the reference algorithm of README.md.
// Vec<i64> indexed with xs[i] (bounds checked; the optimizer may remove the
// check where it proves i < len, as it may in every checked column). The
// sum's + is plain i64 +: it wraps in the "rust" column (overflow-checks off
// at opt-level=3) and traps in "rust-ovf" (-C overflow-checks=on). The fill
// uses wrapping_* because splitmix64 wraps by design. No unsafe, no crates.

fn mix(mut z: u64) -> u64 {
    z = (z ^ (z >> 30)).wrapping_mul(0xbf58476d1ce4e5b9);
    z = (z ^ (z >> 27)).wrapping_mul(0x94d049bb133111eb);
    z ^ (z >> 31)
}

fn main() {
    let a: Vec<String> = std::env::args().collect();
    if a.len() != 4 {
        eprintln!("usage: intsum <n> <rounds> <seed>");
        std::process::exit(2);
    }
    let n: usize = a[1].parse().unwrap();
    let rounds: usize = a[2].parse().unwrap();
    let mut st: u64 = a[3].parse().unwrap();
    let mut xs: Vec<i64> = vec![0; n];
    for i in 0..n {
        st = st.wrapping_add(0x9e3779b97f4a7c15);
        xs[i] = (mix(st) & 1023) as i64;
    }
    let mut total: i64 = 0;
    for r in 0..rounds {
        let mut s: i64 = 0;
        for i in 0..n {
            s += xs[i];
        }
        total += s;
        xs[r % n] = s & 1023;
    }
    println!("{}", total);
}
