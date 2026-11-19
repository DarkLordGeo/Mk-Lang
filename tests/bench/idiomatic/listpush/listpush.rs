// listpush, Rust, idiomatic std: the task of README.md with Vec<u64> over
// the global allocator (System: HeapAlloc on Windows): per round a Vec from
// Vec::new() (no capacity), n single pushes, one extend_from_slice of n/4,
// a read of every element, then the drop. Usage: listpush <n> <rounds>
// <seed>.
fn mix(mut z: u64) -> u64 {
    z = (z ^ (z >> 30)).wrapping_mul(0xbf58476d1ce4e5b9);
    z = (z ^ (z >> 27)).wrapping_mul(0x94d049bb133111eb);
    z ^ (z >> 31)
}

fn round(n: usize, ext: &[u64], st: &mut u64) -> u64 {
    let mut l: Vec<u64> = Vec::new();
    for _ in 0..n {
        *st = st.wrapping_add(0x9e3779b97f4a7c15);
        l.push(mix(*st));
    }
    l.extend_from_slice(ext);
    let mut sum: u64 = 0;
    for &x in &l {
        sum = sum.wrapping_add(x);
    }
    sum.wrapping_add(l.len() as u64)
}

fn main() {
    let a: Vec<String> = std::env::args().collect();
    if a.len() != 4 {
        eprintln!("usage: listpush <n> <rounds> <seed>");
        std::process::exit(2);
    }
    let n: usize = a[1].parse().unwrap();
    let rounds: u64 = a[2].parse().unwrap();
    let mut st: u64 = a[3].parse().unwrap();
    let ext: Vec<u64> = (0..n / 4).map(|i| 3 * i as u64 + 1).collect();
    let mut total: u64 = 0;
    for _ in 0..rounds {
        total = total.wrapping_add(round(n, &ext, &mut st));
    }
    println!("{}", total);
}
