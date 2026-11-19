// trysend, Rust, idiomatic std: std::sync::mpsc::SyncSender::try_send on a
// sync_channel of capacity 1,024, on one thread (README.md): reps rounds of
// 1,040 tries, the last 16 getting TrySendError::Full with the value back,
// then 1,024 recv calls. The checksum adds every value received and every
// value given back, with wrapping adds.
// Usage: trysend <reps>.
use std::sync::mpsc::{TrySendError, sync_channel};

fn main() {
    let args: Vec<String> = std::env::args().collect();
    if args.len() != 2 {
        eprintln!("usage: trysend <reps>");
        std::process::exit(2);
    }
    let reps: u64 = args[1].parse().unwrap();
    let (tx, rx) = sync_channel::<i64>(1024);
    let (mut acc, mut v) = (0i64, 0i64);
    for _ in 0..reps {
        for _ in 0..1040 {
            v = v.wrapping_add(1);
            match tx.try_send(v) {
                Ok(()) => {}
                Err(TrySendError::Full(x)) => acc = acc.wrapping_add(x),
                Err(TrySendError::Disconnected(_)) => unreachable!(),
            }
        }
        for _ in 0..1024 {
            acc = acc.wrapping_add(rx.recv().unwrap());
        }
    }
    println!("{}", acc);
}
