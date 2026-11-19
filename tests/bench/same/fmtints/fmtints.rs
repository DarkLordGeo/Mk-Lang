// fmtints, Rust: the reference algorithm of README.md. The textbook
// conversion written out (one digit per division by 10 into a [u8; 20]
// temporary, copied out byte by byte), not write!: the idiomatic fmt row
// compares the std formatters. A Vec<u8> output buffer indexed buf[pos]
// (bounds checked), usize positions (the type Rust indexes with) and i64
// sums like C's int64_t (+ wraps in "rust", traps in "rust-ovf"). splitmix64 uses wrapping_*. No unsafe, no crates.

fn mix(mut z: u64) -> u64 {
    z = (z ^ (z >> 30)).wrapping_mul(0xbf58476d1ce4e5b9);
    z = (z ^ (z >> 27)).wrapping_mul(0x94d049bb133111eb);
    z ^ (z >> 31)
}

fn main() {
    let args: Vec<String> = std::env::args().collect();
    if args.len() != 4 {
        eprintln!("usage: fmtints <n> <rounds> <seed>");
        std::process::exit(2);
    }
    let n: usize = args[1].parse().unwrap();
    let rounds: usize = args[2].parse().unwrap();
    let mut st: u64 = args[3].parse().unwrap();
    let mut xs: Vec<i64> = vec![0; n];
    for i in 0..n {
        st = st.wrapping_add(0x9e3779b97f4a7c15);
        xs[i] = (mix(st) % 2000000001) as i64 - 1000000000;
    }
    let mut buf: Vec<u8> = vec![0; n * 12];
    let mut tmp = [0u8; 20];
    let mut total: i64 = 0;
    for _ in 0..rounds {
        let mut pos: usize = 0;
        for i in 0..n {
            let x = xs[i];
            let mut u: u64;
            if x < 0 {
                buf[pos] = 45;
                pos += 1;
                u = (-x) as u64;
            } else {
                u = x as u64;
            }
            let mut k: usize = 20;
            loop {
                k -= 1;
                tmp[k] = 48 + (u % 10) as u8;
                u = u / 10;
                if u == 0 {
                    break;
                }
            }
            for j in k..20 {
                buf[pos] = tmp[j];
                pos += 1;
            }
            buf[pos] = 10;
            pos += 1;
        }
        let mut sum: i64 = 0;
        for i in 0..pos {
            sum += buf[i] as i64;
        }
        total += sum;
    }
    println!("{}", total);
}
