// sortstr, Rust, idiomatic std: the task of README.md with
// <[&str]>::sort_unstable (ipnsort; str's Ord compares bytes) over &str
// views into one buffer, checked once by std::str::from_utf8. Usage:
// sortstr <n> <rounds> <seed>.
fn mix(mut z: u64) -> u64 {
    z = (z ^ (z >> 30)).wrapping_mul(0xbf58476d1ce4e5b9);
    z = (z ^ (z >> 27)).wrapping_mul(0x94d049bb133111eb);
    z ^ (z >> 31)
}

fn fnv(w: &str) -> u64 {
    let mut h: u64 = 0xcbf29ce484222325;
    for &c in w.as_bytes() {
        h = (h ^ c as u64).wrapping_mul(0x100000001b3);
    }
    h
}

fn check(xs: &[&str]) -> u64 {
    let mut sum: u64 = 0;
    for i in 0..xs.len() {
        if i > 0 && xs[i - 1] > xs[i] {
            return 0xdead;
        }
        sum = sum.wrapping_add(fnv(xs[i]).wrapping_mul((i % 1000 + 1) as u64));
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
    let mut st: u64 = a[3].parse().unwrap();
    let mut buf: Vec<u8> = Vec::with_capacity(n * 16);
    let mut offs: Vec<usize> = Vec::with_capacity(n + 1);
    for _ in 0..n {
        offs.push(buf.len());
        st = st.wrapping_add(0x9e3779b97f4a7c15);
        let l = 1 + (mix(st) % 16) as usize;
        for _ in 0..l {
            st = st.wrapping_add(0x9e3779b97f4a7c15);
            buf.push((0x61 + mix(st) % 26) as u8);
        }
    }
    offs.push(buf.len());
    let text = std::str::from_utf8(&buf).unwrap();
    let orig: Vec<&str> = (0..n).map(|i| &text[offs[i]..offs[i + 1]]).collect();
    let mut work: Vec<&str> = orig.clone();
    let mut total: u64 = 0;
    for _ in 0..rounds {
        work.copy_from_slice(&orig);
        work.sort_unstable();
        total = total.wrapping_add(check(&work));
    }
    println!("{}", total);
}
