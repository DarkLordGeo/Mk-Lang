// xoshiro, Rust: xoshiro256** seeded with four SplitMix64 outputs and
// Lemire's bounded draw with u128, the same steps as every other column of
// README.md. Wrapping operators where the algorithm wraps, so the
// overflow-checked build (rust-ovf) has nothing to trap on.
struct X {
    s: [u64; 4],
}

fn splitmix(x: &mut u64) -> u64 {
    *x = x.wrapping_add(0x9E3779B97F4A7C15);
    let mut z = *x;
    z = (z ^ (z >> 30)).wrapping_mul(0xBF58476D1CE4E5B9);
    z = (z ^ (z >> 27)).wrapping_mul(0x94D049BB133111EB);
    z ^ (z >> 31)
}

impl X {
    #[inline]
    fn next(&mut self) -> u64 {
        let s = &mut self.s;
        let result = s[1].wrapping_mul(5).rotate_left(7).wrapping_mul(9);
        let t = s[1] << 17;
        s[2] ^= s[0];
        s[3] ^= s[1];
        s[1] ^= s[2];
        s[0] ^= s[3];
        s[2] ^= t;
        s[3] = s[3].rotate_left(45);
        result
    }

    #[inline]
    fn below(&mut self, n: u64) -> u64 {
        let mut m = (self.next() as u128) * (n as u128);
        if (m as u64) < n {
            let t = n.wrapping_neg() % n;
            while (m as u64) < t {
                m = (self.next() as u128) * (n as u128);
            }
        }
        (m >> 64) as u64
    }
}

fn main() {
    let args: Vec<String> = std::env::args().collect();
    if args.len() != 3 {
        eprintln!("usage: xoshiro <n> <seed>");
        std::process::exit(2);
    }
    let n: i64 = args[1].parse().unwrap();
    let mut x: u64 = args[2].parse().unwrap();
    let mut g = X { s: [0; 4] };
    for i in 0..4 {
        g.s[i] = splitmix(&mut x);
    }
    let mut total: u64 = 0;
    for _ in 0..n {
        total = total.wrapping_add(g.next());
        total = total.wrapping_add(g.below(1000));
    }
    println!("{}", total);
}
