// mapstr, Rust, idiomatic std: the task of README.md with
// std::collections::HashMap<&str, u64> whose keys borrow one generated
// String (no copy per key), and its default hasher (SipHash-1-3 under a
// random per-process key, RandomState).
use std::collections::HashMap;

fn mix(mut z: u64) -> u64 {
    z = (z ^ (z >> 30)).wrapping_mul(0xbf58476d1ce4e5b9);
    z = (z ^ (z >> 27)).wrapping_mul(0x94d049bb133111eb);
    z ^ (z >> 31)
}

// The keys and their misses (the first letter in upper case), as one
// String each and the end offset of every key.
fn make_keys(n: usize, seed: u64, min_len: usize, max_len: usize) -> (String, String, Vec<usize>) {
    let mut buf: Vec<u8> = Vec::new();
    let mut miss: Vec<u8> = Vec::new();
    let mut ends = Vec::with_capacity(n);
    let mut st = seed;
    let span = (max_len - min_len + 1) as u64;
    for _ in 0..n {
        st = st.wrapping_add(0x9e3779b97f4a7c15);
        let mut z = mix(st);
        let l = min_len + (z % span) as usize;
        for j in 0..l {
            if j % 8 == 0 {
                z = mix(z.wrapping_add(j as u64));
            }
            let mut c = b'a' + (((z >> (8 * (j % 8))) & 0xFF) % 26) as u8;
            buf.push(c);
            if j == 0 {
                c -= b'a' - b'A';
            }
            miss.push(c);
        }
        ends.push(buf.len());
    }
    (String::from_utf8(buf).unwrap(), String::from_utf8(miss).unwrap(), ends)
}

fn round(ks: &[&str], ms: &[&str], r: u64) -> u64 {
    let mut m: HashMap<&str, u64> = HashMap::new();
    for (i, k) in ks.iter().enumerate() {
        m.insert(*k, (i as u64).wrapping_add(r));
    }
    let mut sum: u64 = 0;
    for (i, k) in ks.iter().enumerate() {
        if let Some(v) = m.get(k) {
            sum = sum.wrapping_add(*v);
        }
        if m.contains_key(ms[i]) {
            sum = sum.wrapping_add(1);
        }
        if i % 2 == 0 {
            m.remove(k);
        }
    }
    for k in ks {
        if m.contains_key(k) {
            sum = sum.wrapping_add(3);
        }
    }
    sum.wrapping_add(m.len() as u64)
}

fn main() {
    let a: Vec<String> = std::env::args().collect();
    if a.len() != 6 {
        eprintln!("usage: mapstr <n> <rounds> <seed> <minlen> <maxlen>");
        std::process::exit(2);
    }
    let n: usize = a[1].parse().unwrap();
    let rounds: u64 = a[2].parse().unwrap();
    let seed: u64 = a[3].parse().unwrap();
    let min_len: usize = a[4].parse().unwrap();
    let max_len: usize = a[5].parse().unwrap();
    let (all, all_miss, ends) = make_keys(n, seed, min_len, max_len);
    let mut ks: Vec<&str> = Vec::with_capacity(n);
    let mut ms: Vec<&str> = Vec::with_capacity(n);
    let mut start = 0;
    for &e in &ends {
        ks.push(&all[start..e]);
        ms.push(&all_miss[start..e]);
        start = e;
    }
    let mut total: u64 = 0;
    for r in 0..rounds {
        total = total.wrapping_add(round(&ks, &ms, r));
    }
    println!("{}", total);
}
