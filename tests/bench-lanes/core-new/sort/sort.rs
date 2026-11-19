// sort, Rust, idiomatic std: the task of README.md with slice::sort_unstable
// (ipnsort since Rust 1.81).
fn mix(mut z: u64) -> u64 {
    z = (z ^ (z >> 30)).wrapping_mul(0xbf58476d1ce4e5b9);
    z = (z ^ (z >> 27)).wrapping_mul(0x94d049bb133111eb);
    z ^ (z >> 31)
}

fn fold(xs: &[u64]) -> u64 {
    let n = xs.len();
    let mut sum = xs[n - 1];
    let mut i = 0;
    while i < n {
        sum = sum.wrapping_add(xs[i].wrapping_mul(i as u64));
        i += 1024;
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
    let seed: u64 = a[3].parse().unwrap();
    let mut xs = vec![0u64; n];
    let mut total: u64 = 0;
    for r in 0..rounds {
        let mut st = seed.wrapping_add(r.wrapping_mul(0x5555555555555555));
        for x in xs.iter_mut() {
            st = st.wrapping_add(0x9e3779b97f4a7c15);
            *x = mix(st);
        }
        xs.sort_unstable();
        total = total.wrapping_add(fold(&xs));
    }
    println!("{}", total);
}
