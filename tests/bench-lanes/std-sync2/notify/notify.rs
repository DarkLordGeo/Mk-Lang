// notify, Rust, idiomatic std: the workload of README.md with a
// std::sync::Mutex holding whose turn it is and one std::sync::Condvar per
// side, over two OS threads (std::thread::scope; labelled threads). Each
// side, n times: lock, wait while it is not its turn, give the turn to the
// other, unlock, notify_one the other's Condvar. Prints the total rounds
// (2 * n). Usage: notify <n>.
use std::sync::atomic::{AtomicI64, Ordering};
use std::sync::{Condvar, Mutex};

fn main() {
    let args: Vec<String> = std::env::args().collect();
    if args.len() != 2 {
        eprintln!("usage: notify <n>");
        std::process::exit(2);
    }
    let n: usize = args[1].parse().unwrap();
    let turn = Mutex::new(0usize);
    let cv = [Condvar::new(), Condvar::new()];
    let total = AtomicI64::new(0);
    std::thread::scope(|s| {
        for me in 0..2usize {
            let (turn, cv, total) = (&turn, &cv, &total);
            s.spawn(move || {
                let mut k: i64 = 0;
                for _ in 0..n {
                    let mut g = turn.lock().unwrap();
                    while *g != me {
                        g = cv[me].wait(g).unwrap();
                    }
                    *g = 1 - me;
                    drop(g);
                    cv[1 - me].notify_one();
                    k += 1;
                }
                total.fetch_add(k, Ordering::Relaxed);
            });
        }
    });
    println!("{}", total.load(Ordering::Relaxed));
}
