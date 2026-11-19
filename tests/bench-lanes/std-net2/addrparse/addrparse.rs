// addrparse, Rust, std::net: Display and FromStr of SocketAddr round trips
// of splitmix64 addresses (README.md). Usage: addrparse <n>.
use std::fmt::Write;
use std::net::{IpAddr, Ipv4Addr, Ipv6Addr, SocketAddr};

fn main() {
    let n: u64 = std::env::args().nth(1).unwrap().parse().unwrap();
    let mut s: u64 = 1;
    let mut next = || {
        s = s.wrapping_add(0x9E3779B97F4A7C15);
        let mut z = s;
        z = (z ^ (z >> 30)).wrapping_mul(0xBF58476D1CE4E5B9);
        z = (z ^ (z >> 27)).wrapping_mul(0x94D049BB133111EB);
        z ^ (z >> 31)
    };
    let mut sum: u64 = 0;
    let mut t = String::new();
    for _ in 0..n {
        let x = next();
        let ip = if x & 1 == 0 {
            IpAddr::V4(Ipv4Addr::new((x >> 8) as u8, (x >> 16) as u8, (x >> 24) as u8, (x >> 32) as u8))
        } else {
            let y = next();
            let z = next();
            let zero = (x >> 8) & (x >> 16);
            let mut b = [0u8; 16];
            for k in 0..8 {
                let w = if k >= 4 { z } else { y };
                let mut g = (w >> (16 * (k % 4))) & 0xFFFF;
                if (zero >> k) & 1 == 1 {
                    g = 0;
                }
                if k == 0 {
                    g |= 0x2000;
                }
                b[2 * k] = (g >> 8) as u8;
                b[2 * k + 1] = g as u8;
            }
            IpAddr::V6(Ipv6Addr::from(b))
        };
        let a = SocketAddr::new(ip, ((x >> 40) & 0xFFFF) as u16);
        t.clear();
        write!(t, "{}", a).unwrap();
        let p: SocketAddr = t.parse().unwrap();
        sum = sum.wrapping_mul(31).wrapping_add(p.port() as u64);
        match p.ip() {
            IpAddr::V4(v) => {
                for b in v.octets() {
                    sum = sum.wrapping_mul(31).wrapping_add(b as u64);
                }
            }
            IpAddr::V6(v) => {
                for b in v.octets() {
                    sum = sum.wrapping_mul(31).wrapping_add(b as u64);
                }
            }
        }
    }
    println!("{} {}", n, sum);
}
