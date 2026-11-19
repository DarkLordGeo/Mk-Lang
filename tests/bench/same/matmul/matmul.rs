// matmul, Rust: the reference algorithm of README.md.
// Three flat Vec<f64> of n * n, row-major, the i-k-j loop indexed
// c[i * n + j] (bounds checked), repeated `rounds` times into the same c.
// usize indices: + and * wrap in "rust" and trap in "rust-ovf". The fill uses
// wrapping_* because splitmix64 wraps by design. No unsafe, no crates.

fn mix(mut z: u64) -> u64 {
    z = (z ^ (z >> 30)).wrapping_mul(0xbf58476d1ce4e5b9);
    z = (z ^ (z >> 27)).wrapping_mul(0x94d049bb133111eb);
    z ^ (z >> 31)
}

fn main() {
    let args: Vec<String> = std::env::args().collect();
    if args.len() != 4 {
        eprintln!("usage: matmul <n> <rounds> <seed>");
        std::process::exit(2);
    }
    let n: usize = args[1].parse().unwrap();
    let rounds: usize = args[2].parse().unwrap();
    let mut st: u64 = args[3].parse().unwrap();
    let mut a: Vec<f64> = vec![0.0; n * n];
    let mut b: Vec<f64> = vec![0.0; n * n];
    let mut c: Vec<f64> = vec![0.0; n * n];
    for i in 0..n * n {
        st = st.wrapping_add(0x9e3779b97f4a7c15);
        a[i] = (mix(st) & 255) as f64 / 256.0;
        st = st.wrapping_add(0x9e3779b97f4a7c15);
        b[i] = (mix(st) & 255) as f64 / 256.0;
    }
    for _ in 0..rounds {
        for i in 0..n {
            for k in 0..n {
                let aik = a[i * n + k];
                for j in 0..n {
                    c[i * n + j] = c[i * n + j] + aik * b[k * n + j];
                }
            }
        }
    }
    let mut total = 0.0;
    for i in 0..n * n {
        total = total + c[i];
    }
    println!("{}", (total * 65536.0) as i64);
}
