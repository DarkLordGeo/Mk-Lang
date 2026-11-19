// search, Rust, idiomatic std: the task of README.md with the slice's
// binary search, <[u64]>::binary_search (u64's Ord, inlined), which returns
// Ok(index) for a present key and Err(insertion point) otherwise; the keys
// are distinct, so Ok has one index. Indexing is checked. Usage: search
// <n> <lookups> <seed>.
fn mix(mut z: u64) -> u64 {
    z = (z ^ (z >> 30)).wrapping_mul(0xbf58476d1ce4e5b9);
    z = (z ^ (z >> 27)).wrapping_mul(0x94d049bb133111eb);
    z ^ (z >> 31)
}

fn main() {
    let a: Vec<String> = std::env::args().collect();
    if a.len() != 4 {
        eprintln!("usage: search <n> <lookups> <seed>");
        std::process::exit(2);
    }
    let n: usize = a[1].parse().unwrap();
    let lookups: u64 = a[2].parse().unwrap();
    let mut st: u64 = a[3].parse().unwrap();
    let xs: Vec<u64> = (0..n).map(|i| 2 * i as u64 + 1).collect();
    let span = 2 * n as u64 + 1;
    let mut total: u64 = 0;
    for _ in 0..lookups {
        st = st.wrapping_add(0x9e3779b97f4a7c15);
        let k = mix(st) % span;
        match xs.binary_search(&k) {
            Ok(i) => total = total.wrapping_add(i as u64 + 1),
            Err(_) => total = total.wrapping_add(1),
        }
    }
    println!("{}", total);
}
