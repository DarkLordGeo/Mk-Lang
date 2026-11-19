// textmap, Rust, idiomatic std: the task of README.md with
// std::collections::HashMap<String, u64> and its default hasher
// (SipHash-1-3 under RandomState's random key). A key is allocated only
// when it is new: get_mut first, then insert(k.to_owned()), as TextMap
// copies only a new key. Keys pass std::str::from_utf8, as Mk's str.From.
use std::collections::HashMap;

fn mix(mut z: u64) -> u64 {
    z = (z ^ (z >> 30)).wrapping_mul(0xbf58476d1ce4e5b9);
    z = (z ^ (z >> 27)).wrapping_mul(0x94d049bb133111eb);
    z ^ (z >> 31)
}

fn digits(buf: &mut [u8; 20], x: u64) -> usize {
    let mut i = buf.len() - 1;
    let mut v = x;
    buf[i] = (v % 10) as u8 + b'0';
    v /= 10;
    while v > 0 {
        i -= 1;
        buf[i] = (v % 10) as u8 + b'0';
        v /= 10;
    }
    i
}

fn round(n: usize, seed: u64) -> u64 {
    let mut m: HashMap<String, u64> = HashMap::new();
    let u = (n / 4 + 1) as u64;
    let mut buf = [0u8; 20];
    let mut st = seed;
    for i in 0..n {
        st = st.wrapping_add(0x9e3779b97f4a7c15);
        let p = digits(&mut buf, mix(st) % u);
        let k = std::str::from_utf8(&buf[p..]).unwrap();
        if let Some(v) = m.get_mut(k) {
            *v = i as u64;
        } else {
            m.insert(k.to_owned(), i as u64);
        }
    }
    let mut sum: u64 = 0;
    st = seed ^ 0x5555555555555555;
    for i in 0..n {
        st = st.wrapping_add(0x9e3779b97f4a7c15);
        let p = digits(&mut buf, mix(st) % u);
        let k = std::str::from_utf8(&buf[p..]).unwrap();
        if let Some(v) = m.get_mut(k) {
            sum = sum.wrapping_add(std::mem::replace(v, i as u64));
        } else {
            m.insert(k.to_owned(), i as u64);
            sum = sum.wrapping_add(1);
        }
    }
    sum.wrapping_add(m.len() as u64)
}

fn main() {
    let a: Vec<String> = std::env::args().collect();
    if a.len() != 4 {
        eprintln!("usage: textmap <n> <rounds> <seed>");
        std::process::exit(2);
    }
    let n: usize = a[1].parse().unwrap();
    let rounds: u64 = a[2].parse().unwrap();
    let seed: u64 = a[3].parse().unwrap();
    let mut total: u64 = 0;
    for r in 0..rounds {
        total = total.wrapping_add(round(n, seed.wrapping_add(r)));
    }
    println!("{}", total);
}
