// barrier, Rust, idiomatic std: the workload of README.md with
// std::sync::Barrier and t OS threads (std::thread::scope; labelled
// threads). Each thread waits r rounds and counts the rounds it led
// (BarrierWaitResult::is_leader). Prints the leaders and r.
// Usage: barrier <r> <t>.
use std::sync::atomic::{AtomicI64, Ordering};
use std::sync::Barrier;

fn main() {
    let args: Vec<String> = std::env::args().collect();
    if args.len() != 3 {
        eprintln!("usage: barrier <r> <t>");
        std::process::exit(2);
    }
    let r: usize = args[1].parse().unwrap();
    let t: usize = args[2].parse().unwrap();
    let b = Barrier::new(t);
    let leaders = AtomicI64::new(0);
    std::thread::scope(|s| {
        for _ in 0..t {
            s.spawn(|| {
                let mut k: i64 = 0;
                for _ in 0..r {
                    if b.wait().is_leader() {
                        k += 1;
                    }
                }
                leaders.fetch_add(k, Ordering::Relaxed);
            });
        }
    });
    println!("{} {}", leaders.load(Ordering::Relaxed), r);
}
