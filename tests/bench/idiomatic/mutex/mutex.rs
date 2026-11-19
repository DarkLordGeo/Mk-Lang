// mutex, Rust, idiomatic std: the task of README.md with
// std::sync::Mutex<i64> over t OS threads of std::thread::scope (labelled
// threads).
// Each thread does n times: lock(), add 1 through the guard, drop the
// guard. Prints the count (t * n). Usage: mutex <n> <t>.
use std::sync::Mutex;

fn main() {
    let args: Vec<String> = std::env::args().collect();
    if args.len() != 3 {
        eprintln!("usage: mutex <n> <t>");
        std::process::exit(2);
    }
    let n: usize = args[1].parse().unwrap();
    let t: usize = args[2].parse().unwrap();
    let m = Mutex::new(0i64);
    std::thread::scope(|s| {
        for _ in 0..t {
            let m = &m;
            s.spawn(move || {
                for _ in 0..n {
                    *m.lock().unwrap() += 1;
                }
            });
        }
    });
    println!("{}", m.into_inner().unwrap());
}
