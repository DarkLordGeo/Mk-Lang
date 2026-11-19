// ws-frames, Rust: std.websocket's receive path written once per language
// (README.md): decode a buffer of masked client frames into messages,
// fragments joined, each payload unmasked a byte at a time into one
// message buffer, each text message checked as UTF-8 by RFC 3629's table
// (a branch per lead byte), rounds passes. The buffer is batches copies of
// one batch of seven messages (binary 0, 125, 65,535 and 70,000 bytes; text
// 126 bytes, 30,000 bytes in three fragments split inside a character, and
// 1,000 bytes ending in a byte that is not UTF-8), each frame masked with
// the four low bytes of (f + 1) * 0x9E3779B97F4A7C15, f the frame's index
// in the buffer; binary payloads are splitmix64 bytes (seed 1), text the
// 10-byte unit "a", U+00E9, U+20AC, U+1F600 repeated. Per message the
// checksum adds its length, 7 when it is binary or valid text, and its
// byte at (r * 7919 + m) % length, m its index in the pass. Wrapping
// operators where the task wraps. Usage: ws-frames <batches> <rounds>.
use std::env;

struct Message {
    op: u8,
    n: usize,
    frags: usize,
}

const BATCH: [Message; 7] = [
    Message { op: 2, n: 0, frags: 1 },
    Message { op: 2, n: 125, frags: 1 },
    Message { op: 1, n: 126, frags: 1 },
    Message { op: 2, n: 65535, frags: 1 },
    Message { op: 2, n: 70000, frags: 1 },
    Message { op: 1, n: 30000, frags: 3 },
    Message { op: 1, n: 1000, frags: 1 },
];

const UNIT: [u8; 10] = [0x61, 0xC3, 0xA9, 0xE2, 0x82, 0xAC, 0xF0, 0x9F, 0x98, 0x80];

const MAX_MSG: usize = 70000;

// build is the frames of batches copies of the batch.
fn build(batches: usize) -> Vec<u8> {
    let mut pool = vec![0u8; MAX_MSG];
    let mut st: u64 = 1;
    let mut i = 0;
    while i < MAX_MSG {
        st = st.wrapping_add(0x9E3779B97F4A7C15);
        let mut z = st;
        z = (z ^ (z >> 30)).wrapping_mul(0xBF58476D1CE4E5B9);
        z = (z ^ (z >> 27)).wrapping_mul(0x94D049BB133111EB);
        z ^= z >> 31;
        let mut k = 0;
        while k < 8 && i < MAX_MSG {
            pool[i] = (z >> (8 * k)) as u8;
            k += 1;
            i += 1;
        }
    }
    let mut text = vec![0u8; MAX_MSG];
    for i in 0..MAX_MSG {
        text[i] = UNIT[i % 10];
    }
    let mut payload = vec![0u8; MAX_MSG];
    let mut out = vec![0u8; batches * (166786 + 9 * 14)];
    let mut o = 0;
    let mut f: u64 = 0;
    for _ in 0..batches {
        for m in BATCH.iter() {
            let src = if m.op == 1 { &text } else { &pool };
            for i in 0..m.n {
                payload[i] = src[i];
            }
            if m.n == 1000 {
                payload[999] = 0xFF;
            }
            // Fragments: the first n/3 - 1 bytes, the next n/3 + 1, the rest.
            let mut at = 0;
            for j in 0..m.frags {
                let mut l = m.n - at;
                if m.frags == 3 && j == 0 {
                    l = m.n / 3 - 1;
                } else if m.frags == 3 && j == 1 {
                    l = m.n / 3 + 1;
                }
                let mut b0: u8 = 0;
                if j == 0 {
                    b0 = m.op;
                }
                if j == m.frags - 1 {
                    b0 |= 0x80;
                }
                out[o] = b0;
                o += 1;
                if l <= 125 {
                    out[o] = 0x80 | l as u8;
                    o += 1;
                } else if l <= 65535 {
                    out[o] = 0x80 | 126;
                    out[o + 1] = (l >> 8) as u8;
                    out[o + 2] = l as u8;
                    o += 3;
                } else {
                    out[o] = 0x80 | 127;
                    out[o + 1] = 0;
                    out[o + 2] = 0;
                    out[o + 3] = 0;
                    out[o + 4] = 0;
                    out[o + 5] = (l >> 24) as u8;
                    out[o + 6] = (l >> 16) as u8;
                    out[o + 7] = (l >> 8) as u8;
                    out[o + 8] = l as u8;
                    o += 9;
                }
                let kw = (f.wrapping_add(1)).wrapping_mul(0x9E3779B97F4A7C15);
                let key = [kw as u8, (kw >> 8) as u8, (kw >> 16) as u8, (kw >> 24) as u8];
                for k in 0..4 {
                    out[o + k] = key[k];
                }
                o += 4;
                for i in 0..l {
                    out[o + i] = payload[at + i] ^ key[i & 3];
                }
                o += l;
                at += l;
                f += 1;
            }
        }
    }
    out.truncate(o);
    out
}

// valid_utf8 reports whether s is UTF-8 by RFC 3629's table of well-formed
// sequences: a branch per lead byte, the second byte's range by the lead,
// the rest continuation bytes.
fn valid_utf8(s: &[u8]) -> bool {
    let n = s.len();
    let mut i = 0;
    while i < n {
        let c = s[i];
        if c < 0x80 {
            i += 1;
            continue;
        }
        let need;
        let mut lo: u8 = 0x80;
        let mut hi: u8 = 0xBF;
        if c < 0xC2 {
            return false;
        } else if c < 0xE0 {
            need = 1;
        } else if c < 0xF0 {
            need = 2;
            if c == 0xE0 {
                lo = 0xA0;
            } else if c == 0xED {
                hi = 0x9F;
            }
        } else if c < 0xF5 {
            need = 3;
            if c == 0xF0 {
                lo = 0x90;
            } else if c == 0xF4 {
                hi = 0x8F;
            }
        } else {
            return false;
        }
        if i + need >= n {
            return false;
        }
        let b = s[i + 1];
        if b < lo || b > hi {
            return false;
        }
        let mut k = 2;
        while k <= need {
            if s[i + k] & 0xC0 != 0x80 {
                return false;
            }
            k += 1;
        }
        i += need + 1;
    }
    true
}

// decode is one pass over buf: the checksum it adds for round r.
fn decode(buf: &[u8], msg: &mut [u8], r: u64) -> u64 {
    let len = buf.len();
    let mut sum: u64 = 0;
    let mut pos = 0;
    let mut mlen = 0;
    let mut mop: u8 = 0;
    let mut count: u64 = 0;
    while pos + 2 <= len {
        let b0 = buf[pos];
        let b1 = buf[pos + 1];
        let mut l = (b1 & 0x7F) as usize;
        let mut hl = 2;
        if l == 126 {
            if pos + 4 > len {
                return sum.wrapping_add(1000003);
            }
            l = (buf[pos + 2] as usize) << 8 | buf[pos + 3] as usize;
            hl = 4;
        } else if l == 127 {
            if pos + 10 > len {
                return sum.wrapping_add(1000003);
            }
            let mut x: u64 = 0;
            for k in 2..10 {
                x = x << 8 | buf[pos + k] as u64;
            }
            if x > MAX_MSG as u64 {
                return sum.wrapping_add(1000003);
            }
            l = x as usize;
            hl = 10;
        }
        let op = b0 & 0x0F;
        if b0 & 0x70 != 0 || b1 & 0x80 == 0 || l > len - pos - hl - 4 {
            return sum.wrapping_add(1000003);
        }
        if op != 0 {
            mop = op;
            mlen = 0;
        }
        if l > MAX_MSG - mlen {
            return sum.wrapping_add(1000003);
        }
        let k = pos + hl;
        let key = [buf[k], buf[k + 1], buf[k + 2], buf[k + 3]];
        pos = k + 4;
        for i in 0..l {
            msg[mlen + i] = buf[pos + i] ^ key[i & 3];
        }
        mlen += l;
        pos += l;
        if b0 & 0x80 != 0 {
            sum = sum.wrapping_add(mlen as u64);
            if mop != 1 || valid_utf8(&msg[..mlen]) {
                sum = sum.wrapping_add(7);
            }
            if mlen > 0 {
                let at = (r.wrapping_mul(7919).wrapping_add(count) % mlen as u64) as usize;
                sum = sum.wrapping_add(msg[at] as u64);
            }
            count += 1;
        }
    }
    sum
}

fn main() {
    let args: Vec<String> = env::args().collect();
    if args.len() != 3 {
        eprintln!("usage: ws-frames <batches> <rounds>");
        std::process::exit(2);
    }
    let batches: usize = args[1].parse().unwrap();
    let rounds: u64 = args[2].parse().unwrap();
    let buf = build(batches);
    let mut msg = vec![0u8; MAX_MSG];
    let mut total: u64 = 0;
    for r in 0..rounds {
        total = total.wrapping_add(decode(&buf, &mut msg, r));
    }
    println!("{}", total);
}
