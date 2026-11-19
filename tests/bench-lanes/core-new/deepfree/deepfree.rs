// deepfree, Rust, idiomatic std: the task of README.md with one
// Vec<String> per round, built by push of to_owned slices, read back once,
// then dropped, which drops every String before the buffer.
fn mix(mut z: u64) -> u64 {
    z = (z ^ (z >> 30)).wrapping_mul(0xbf58476d1ce4e5b9);
    z = (z ^ (z >> 27)).wrapping_mul(0x94d049bb133111eb);
    z ^ (z >> 31)
}

fn round(src: &str, n: usize, seed: u64) -> u64 {
    let mut l: Vec<String> = Vec::new();
    let mut st = seed;
    for _ in 0..n {
        st = st.wrapping_add(0x9e3779b97f4a7c15);
        let v = mix(st);
        let k = (v % 64) as usize;
        let off = ((v >> 16) % 4032) as usize;
        l.push(src[off..off + k].to_owned());
    }
    let mut sum: u64 = 0;
    for t in &l {
        let s = t.as_bytes();
        sum = sum.wrapping_add(s.len() as u64);
        if !s.is_empty() {
            sum = sum
                .wrapping_add((s[0] as u64).wrapping_mul(7))
                .wrapping_add(s[s.len() - 1] as u64);
        }
    }
    drop(l);
    sum
}

fn main() {
    let a: Vec<String> = std::env::args().collect();
    if a.len() != 4 {
        eprintln!("usage: deepfree <n> <rounds> <seed>");
        std::process::exit(2);
    }
    let n: usize = a[1].parse().unwrap();
    let rounds: u64 = a[2].parse().unwrap();
    let seed: u64 = a[3].parse().unwrap();
    let mut buf = [0u8; 4096];
    let mut st = seed ^ 0x5555555555555555;
    for b in buf.iter_mut() {
        st = st.wrapping_add(0x9e3779b97f4a7c15);
        *b = (mix(st) % 26) as u8 + b'a';
    }
    let src = std::str::from_utf8(&buf).unwrap();
    let mut total: u64 = 0;
    for r in 0..rounds {
        total = total.wrapping_add(round(src, n, seed.wrapping_add(r)));
    }
    println!("{}", total);
}
