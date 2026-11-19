// relock, Rust, idiomatic std (../README.md): the task of mk/relock.mk with
// std::fs::File::lock (flock on Linux, LockFileEx on Windows). Each round a
// peer thread holds the file's lock on a handle of its own for hold ms and
// then releases it once (relock 0), or releases and retakes it at once
// every hold ms (relock 1) until told to stop or two seconds pass. The main
// thread's waiter calls File::lock on another handle, on a thread of its
// own, since File::lock takes no deadline; a round counts when it took the
// lock within one second (recv_timeout). Prints the rounds and the rounds
// won. Usage: relock <rounds> <hold ms> <relock>.

use std::env;
use std::fs::{self, File, OpenOptions};
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::{mpsc, Arc};
use std::thread;
use std::time::Duration;

fn open(path: &str) -> File {
    OpenOptions::new().read(true).write(true).open(path).expect("open")
}

fn round(path: &str, hold: u64, relock: bool) -> bool {
    let p = open(path);
    let w = open(path);
    p.lock().expect("peer lock");
    let stop = Arc::new(AtomicBool::new(false));
    let ps = stop.clone();
    let peer = thread::spawn(move || {
        for i in 0..400u64 {
            thread::sleep(Duration::from_millis(hold));
            let _ = p.unlock();
            if !relock || ps.load(Ordering::SeqCst) || i * hold >= 2000 {
                break;
            }
            if p.lock().is_err() || ps.load(Ordering::SeqCst) {
                let _ = p.unlock();
                break;
            }
        }
    });
    let (tx, rx) = mpsc::channel();
    let waiter = thread::spawn(move || {
        let ok = w.lock().is_ok();
        let _ = tx.send(ok);
        let _ = w.unlock();
    });
    let won = matches!(rx.recv_timeout(Duration::from_secs(1)), Ok(true));
    stop.store(true, Ordering::SeqCst);
    waiter.join().expect("waiter");
    peer.join().expect("peer");
    won
}

fn main() {
    let a: Vec<String> = env::args().collect();
    let rounds: u64 = a[1].parse().expect("rounds");
    let hold: u64 = a[2].parse().expect("hold");
    let relock = a[3] == "1";
    let path = env::temp_dir().join("mkbench-relock-rust.lock");
    let path = path.to_str().expect("path").to_string();
    File::create(&path).expect("create");
    let mut won = 0u64;
    for _ in 0..rounds {
        if round(&path, hold, relock) {
            won += 1;
        }
    }
    let _ = fs::remove_file(&path);
    println!("{} {}", rounds, won);
}
