// atomicmax, Rust, idiomatic std: the workload of README.md with two
// std::sync::atomic::AtomicI64 over t OS threads (std::thread::scope;
// labelled threads). Each thread takes a ticket n times with
// fetch_add(1, Relaxed) and raises a shared maximum to it with a
// compare_exchange_weak(Relaxed, Relaxed) loop (AtomicI64::fetch_max would
// be the shortcut; the loop is the workload). Prints the counter and the
// maximum (t * n and t * n - 1). Usage: atomicmax <n> <t>.
use std::sync::atomic::{AtomicI64, Ordering};

fn main() {
    let args: Vec<String> = std::env::args().collect();
    if args.len() != 3 {
        eprintln!("usage: atomicmax <n> <t>");
        std::process::exit(2);
    }
    let n: usize = args[1].parse().unwrap();
    let t: usize = args[2].parse().unwrap();
    let c = AtomicI64::new(0);
    let m = AtomicI64::new(0);
    std::thread::scope(|s| {
        for _ in 0..t {
            let (c, m) = (&c, &m);
            s.spawn(move || {
                for _ in 0..n {
                    let x = c.fetch_add(1, Ordering::Relaxed);
                    let mut cur = m.load(Ordering::Relaxed);
                    while x > cur {
                        match m.compare_exchange_weak(cur, x, Ordering::Relaxed, Ordering::Relaxed) {
                            Ok(_) => break,
                            Err(v) => cur = v,
                        }
                    }
                }
            });
        }
    });
    println!("{} {}", c.load(Ordering::Relaxed), m.load(Ordering::Relaxed));
}
