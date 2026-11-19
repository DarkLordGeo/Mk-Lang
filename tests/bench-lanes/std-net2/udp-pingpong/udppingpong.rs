// udp-pingpong, Rust, std::net::UdpSocket: two UDP sockets on 127.0.0.1;
// each of n rounds sends 32 bytes (byte 0 the round's low byte) from a to
// b, receives them on b, sends them back and receives them on a.
// sum = 31 sum + byte 0 of each answer (wrapping). Prints n, sum and the
// failed calls. Usage: udppingpong <n>.
use std::net::UdpSocket;

fn main() {
    let n: u64 = std::env::args().nth(1).unwrap().parse().unwrap();
    let a = UdpSocket::bind("127.0.0.1:0").unwrap();
    let b = UdpSocket::bind("127.0.0.1:0").unwrap();
    let tb = b.local_addr().unwrap();
    let mut msg = [7u8; 32];
    let mut buf = [0u8; 64];
    let mut sum: u64 = 0;
    let mut fails = 0u64;
    for i in 0..n {
        msg[0] = i as u8;
        let ok1 = a.send_to(&msg, tb).is_ok();
        let r1 = b.recv_from(&mut buf);
        let (k1, from) = match r1 {
            Ok((k, f)) => (k, f),
            Err(_) => (0, tb),
        };
        let ok2 = b.send_to(&buf[..32], from).is_ok();
        let k2 = a.recv_from(&mut buf).map(|(k, _)| k).unwrap_or(0);
        if !ok1 || !ok2 || k1 != 32 || k2 != 32 {
            fails += 1;
        }
        sum = sum.wrapping_mul(31).wrapping_add(buf[0] as u64);
    }
    println!("{} {} {}", n, sum, fails);
}
