// conn-churn, Rust, std::net, blocking calls: n loopback TCP connections,
// one after another, of one thread. Each round connects to the listener
// (TcpStream::connect), accepts the connection with the peer's address
// from accept itself (TcpListener::accept), writes two bytes (the round's
// low byte twice) at the accepted end, reads one at the connected end, and
// drops the connected end, then the accepted one. The connected end closes
// with a byte unread, so its TCP resets the connection (RFC 1122 4.2.2.13)
// and no TIME_WAIT entry is left. sum = 31 sum + each byte read
// (wrapping); a round whose peer is not 127.0.0.1 with a port, or whose
// call failed, counts as failed. Prints n, sum and the failed rounds.
// Usage: connchurn <n>.
use std::io::{Read, Write};
use std::net::{IpAddr, Ipv4Addr, TcpListener, TcpStream};

fn main() {
    let n: u64 = std::env::args().nth(1).unwrap().parse().unwrap();
    let l = TcpListener::bind("127.0.0.1:0").unwrap();
    let at = l.local_addr().unwrap();
    let mut sum: u64 = 0;
    let mut fails = 0u64;
    for i in 0..n {
        let mut d = match TcpStream::connect(at) {
            Ok(d) => d,
            Err(_) => {
                fails += 1;
                continue;
            }
        };
        let (mut s, p) = match l.accept() {
            Ok(x) => x,
            Err(_) => {
                fails += 1;
                continue;
            }
        };
        if p.ip() != IpAddr::V4(Ipv4Addr::LOCALHOST) || p.port() == 0 {
            fails += 1;
        }
        let two = [i as u8, i as u8];
        let mut buf = [0u8; 1];
        if s.write_all(&two).is_err() {
            fails += 1;
        }
        match d.read(&mut buf) {
            Ok(1) => {}
            _ => fails += 1,
        }
        sum = sum.wrapping_mul(31).wrapping_add(buf[0] as u64);
        drop(d);
        drop(s);
    }
    println!("{} {} {}", n, sum, fails);
}
