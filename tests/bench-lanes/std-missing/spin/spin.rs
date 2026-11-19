// spin, Rust, idiomatic std: the workload of README.md with
// std::sync::Mutex (Rust's std has no spin lock; its futex Mutex spins
// briefly before it parks) and t OS threads (std::thread::scope; labelled
// threads). Each thread takes the lock n times and adds one to the shared
// counter. Prints the counter. Usage: spin <n> <t>.
use std::sync::Mutex;

fn main() {
    let args: Vec<String> = std::env::args().collect();
    if args.len() != 3 {
        eprintln!("usage: spin <n> <t>");
        std::process::exit(2);
    }
    let n: usize = args[1].parse().unwrap();
    let t: usize = args[2].parse().unwrap();
    let l = Mutex::new(0u64);
    std::thread::scope(|s| {
        for _ in 0..t {
            s.spawn(|| {
                for _ in 0..n {
                    *l.lock().unwrap() += 1;
                }
            });
        }
    });
    println!("{}", l.into_inner().unwrap());
}
