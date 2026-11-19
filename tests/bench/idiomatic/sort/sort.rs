// sort, Rust, idiomatic std: the task of README.md with the slice's
// unstable sort, <[u64]>::sort_unstable (ipnsort, a pdqsort descendant;
// u64's Ord, inlined). Usage: sort <n> <rounds> <seed>.
fn mix(mut z: u64) -> u64 {
    z = (z ^ (z >> 30)).wrapping_mul(0xbf58476d1ce4e5b9);
    z = (z ^ (z >> 27)).wrapping_mul(0x94d049bb133111eb);
    z ^ (z >> 31)
}

fn check(xs: &[u64]) -> u64 {
    let mut sum: u64 = 0;
    for i in 0..xs.len() {
        if i > 0 && xs[i - 1] > xs[i] {
            return 0xdead;
        }
        sum = sum.wrapping_add(xs[i].wrapping_mul((i % 1000 + 1) as u64));
    }
    sum
}

fn main() {
    let a: Vec<String> = std::env::args().collect();
    if a.len() != 4 {
        eprintln!("usage: sort <n> <rounds> <seed>");
        std::process::exit(2);
    }
    let n: usize = a[1].parse().unwrap();
    let rounds: u64 = a[2].parse().unwrap();
    let mut st: u64 = a[3].parse().unwrap();
    let mut xs = vec![0u64; n];
    let mut total: u64 = 0;
    for _ in 0..rounds {
        for p in 0..4 {
            for i in 0..n {
                xs[i] = match p {
                    0 => {
                        st = st.wrapping_add(0x9e3779b97f4a7c15);
                        mix(st)
                    }
                    1 => (n - i) as u64,
                    2 => (i % 1000) as u64,
                    _ => {
                        st = st.wrapping_add(0x9e3779b97f4a7c15);
                        mix(st) >> 60
                    }
                };
            }
            xs.sort_unstable();
            total = total.wrapping_add(check(&xs));
        }
    }
    println!("{}", total);
}
