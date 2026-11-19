// notifypermit, Rust, idiomatic std: the workload of README.md with
// std::thread::park and Thread::unpark, Rust std's permit primitive (unpark
// stores one token, park consumes it), over two OS threads
// (std::thread::scope; labelled threads). park may also return spuriously,
// so each side waits on its own AtomicBool flag, the pattern of park's
// documentation: notify = flag[other].store(true, Release), then unpark;
// wait = while !flag[me].swap(false, Acquire) { park() }. The first side
// notifies then waits, the second waits then notifies, n times each.
// Prints the total rounds (2 * n). Usage: notifypermit <n>.
use std::sync::atomic::{AtomicBool, AtomicI64, Ordering};
use std::sync::OnceLock;
use std::thread::{self, Thread};

fn main() {
    let args: Vec<String> = std::env::args().collect();
    if args.len() != 2 {
        eprintln!("usage: notifypermit <n>");
        std::process::exit(2);
    }
    let n: usize = args[1].parse().unwrap();
    let handles: [OnceLock<Thread>; 2] = [OnceLock::new(), OnceLock::new()];
    let flag = [AtomicBool::new(false), AtomicBool::new(false)];
    let total = AtomicI64::new(0);
    thread::scope(|s| {
        for me in 0..2usize {
            let (handles, flag, total) = (&handles, &flag, &total);
            s.spawn(move || {
                handles[me].set(thread::current()).unwrap();
                // Startup only: wait until the other side has published its
                // handle.
                let other = loop {
                    if let Some(t) = handles[1 - me].get() {
                        break t.clone();
                    }
                    thread::yield_now();
                };
                let notify = || {
                    flag[1 - me].store(true, Ordering::Release);
                    other.unpark();
                };
                let wait = || {
                    while !flag[me].swap(false, Ordering::Acquire) {
                        thread::park();
                    }
                };
                let mut k: i64 = 0;
                for _ in 0..n {
                    if me == 0 {
                        notify();
                        wait();
                    } else {
                        wait();
                        notify();
                    }
                    k += 1;
                }
                total.fetch_add(k, Ordering::Relaxed);
            });
        }
    });
    println!("{}", total.load(Ordering::Relaxed));
}
