// deadlineread, Rust: n round trips of one byte over a loopback TCP
// connection; an echo thread reads each byte and writes it back, and the
// main thread writes i % 251, then reads the echo. With deadline 1 the
// stream's read timeout is set once to 1 s before the loop
// (set_read_timeout, Rust's std form of a one-operation timeout: it
// persists and bounds every later read, so no read pays a system call for
// it), and it never fires; with 0 the read is bare. sum = 31 sum + the byte read (wrapping). Prints n, sum and
// the failed calls. Usage: deadlineread <n> <deadline>.
use std::io::{Read, Write};
use std::net::{TcpListener, TcpStream};
use std::time::Duration;

fn main() {
    let args: Vec<String> = std::env::args().collect();
    let n: u64 = args[1].parse().unwrap();
    let timed = args[2] == "1";
    let l = TcpListener::bind("127.0.0.1:0").unwrap();
    let addr = l.local_addr().unwrap();
    let echo = std::thread::spawn(move || {
        let (mut s, _) = l.accept().unwrap();
        let mut b = [0u8; 1];
        for _ in 0..n {
            if s.read_exact(&mut b).is_err() || s.write_all(&b).is_err() {
                return;
            }
        }
    });
    let mut c = TcpStream::connect(addr).unwrap();
    let mut sum: u64 = 0;
    let mut fails = 0u64;
    let mut b = [0u8; 1];
    if timed {
        c.set_read_timeout(Some(Duration::from_secs(1))).unwrap();
    }
    for i in 0..n {
        if c.write_all(&[(i % 251) as u8]).is_err() {
            fails += 1;
            break;
        }
        match c.read(&mut b) {
            Ok(1) => {}
            _ => {
                fails += 1;
                break;
            }
        }
        sum = sum.wrapping_mul(31).wrapping_add(b[0] as u64);
    }
    drop(c);
    let _ = echo.join();
    println!("{} {} {}", n, sum, fails);
}
