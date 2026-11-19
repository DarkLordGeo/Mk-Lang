// tcp-pingpong, Rust, std::net::TcpStream, blocking calls: one loopback
// TCP connection of one thread, both ends with TCP_NODELAY; each of n
// rounds writes 32 bytes (byte 0 the round's low byte) at the dialled end
// with write_all, reads them whole at the accepted end with read_exact,
// writes them back and reads them whole at the dialled end.
// sum = 31 sum + byte 0 of each answer (wrapping). Prints n, sum and the
// failed calls. The mode argument is Mk's and ignored.
// Usage: tcppingpong <n> [mode].
use std::io::{Read, Write};
use std::net::{TcpListener, TcpStream};

fn main() {
    let n: u64 = std::env::args().nth(1).unwrap().parse().unwrap();
    let l = TcpListener::bind("127.0.0.1:0").unwrap();
    let mut d = TcpStream::connect(l.local_addr().unwrap()).unwrap();
    let (mut s, _) = l.accept().unwrap();
    d.set_nodelay(true).unwrap();
    s.set_nodelay(true).unwrap();
    let mut msg = [7u8; 32];
    let mut buf = [0u8; 32];
    let mut sum: u64 = 0;
    let mut fails = 0u64;
    for i in 0..n {
        msg[0] = i as u8;
        let ok = d.write_all(&msg).is_ok()
            && s.read_exact(&mut buf).is_ok()
            && s.write_all(&buf).is_ok()
            && d.read_exact(&mut buf).is_ok();
        if !ok {
            fails += 1;
        }
        sum = sum.wrapping_mul(31).wrapping_add(buf[0] as u64);
    }
    println!("{} {} {}", n, sum, fails);
}
