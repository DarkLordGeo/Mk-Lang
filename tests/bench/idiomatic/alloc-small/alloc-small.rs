// alloc-small, Rust, idiomatic std: the task of README.md with the global
// allocator (System: HeapAlloc on Windows): each object is a Box<[u8]> made
// by Box::from(&src[off..off + l]) (one allocation and the copy), kept in a
// Vec ring of 4,096; each step reads the oldest, puts a new one in its slot
// with std::mem::replace, and drops the old one. At the end every live
// object is read and the ring dropped. Usage: alloc-small <steps> <seed>.
const LIVE: usize = 4096;

fn mix(mut z: u64) -> u64 {
    z = (z ^ (z >> 30)).wrapping_mul(0xbf58476d1ce4e5b9);
    z = (z ^ (z >> 27)).wrapping_mul(0x94d049bb133111eb);
    z ^ (z >> 31)
}

fn make(src: &[u8], st: &mut u64) -> Box<[u8]> {
    *st = st.wrapping_add(0x9e3779b97f4a7c15);
    let v = mix(*st);
    let l = (v % 249) as usize + 8;
    let off = ((v >> 16) % 3840) as usize;
    Box::from(&src[off..off + l])
}

fn read(o: &[u8]) -> u64 {
    let n = o.len();
    (n as u64)
        .wrapping_add(o[0] as u64)
        .wrapping_add(o[n - 1] as u64)
}

fn main() {
    let a: Vec<String> = std::env::args().collect();
    if a.len() != 3 {
        eprintln!("usage: alloc-small <steps> <seed>");
        std::process::exit(2);
    }
    let steps: u64 = a[1].parse().unwrap();
    let mut st: u64 = a[2].parse().unwrap();
    let mut buf = [0u8; 4096];
    let mut gs = st ^ 0x5555555555555555;
    for b in buf.iter_mut() {
        gs = gs.wrapping_add(0x9e3779b97f4a7c15);
        *b = (mix(gs) % 26) as u8 + b'a';
    }
    let mut ring: Vec<Box<[u8]>> = Vec::with_capacity(LIVE);
    for _ in 0..LIVE {
        ring.push(make(&buf, &mut st));
    }
    let mut total: u64 = 0;
    for j in 0..steps {
        let slot = (j % LIVE as u64) as usize;
        total = total.wrapping_add(read(&ring[slot]));
        let o = make(&buf, &mut st);
        drop(std::mem::replace(&mut ring[slot], o));
    }
    for o in &ring {
        total = total.wrapping_add(read(o));
    }
    drop(ring);
    println!("{}", total);
}
