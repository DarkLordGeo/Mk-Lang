// chunks, Rust, idiomatic std: the task of README.md with slice::chunks_mut
// and slice::chunks over a Vec<u64> of n splitmix64 values.
fn mix(mut z: u64) -> u64 {
    z = (z ^ (z >> 30)).wrapping_mul(0xbf58476d1ce4e5b9);
    z = (z ^ (z >> 27)).wrapping_mul(0x94d049bb133111eb);
    z ^ (z >> 31)
}

fn round(xs: &mut [u64], k: usize) -> u64 {
    let mut j: u64 = 0;
    for c in xs.chunks_mut(k) {
        for e in c.iter_mut() {
            *e = e.wrapping_mul(3).wrapping_add(j);
        }
        j = j.wrapping_add(1);
    }
    let mut sum: u64 = 0;
    for c in xs.chunks(k) {
        sum = sum.wrapping_add(c[0] ^ c[c.len() - 1]);
        for &x in c {
            sum = sum.wrapping_add(x);
        }
    }
    sum
}

fn main() {
    let a: Vec<String> = std::env::args().collect();
    if a.len() != 4 {
        eprintln!("usage: chunks <n> <rounds> <seed>");
        std::process::exit(2);
    }
    let n: usize = a[1].parse().unwrap();
    let rounds: usize = a[2].parse().unwrap();
    let seed: u64 = a[3].parse().unwrap();
    let mut xs: Vec<u64> = Vec::with_capacity(n);
    let mut st = seed;
    for _ in 0..n {
        st = st.wrapping_add(0x9e3779b97f4a7c15);
        xs.push(mix(st));
    }
    let mut total: u64 = 0;
    for r in 0..rounds {
        let k = (r % 16) * 4 + 1;
        total = total.wrapping_add(round(&mut xs, k));
    }
    println!("{}", total);
}
