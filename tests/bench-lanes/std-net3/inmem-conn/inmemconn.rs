// inmem-conn, Rust: a connection over an in-memory transport, as a
// protocol layer sees one. The transport keeps one 32-byte message under
// its own std::sync::Mutex; the connection holds it as a
// Box<dyn Transport + Send + Sync> and takes a read or a write Mutex of
// its own around each call (one virtual call per operation). Each of n
// rounds writes 32 bytes (byte 0 the round's low byte) and reads them
// back. sum = 31 sum + byte 0 of each read (wrapping). The transport is
// picked at run time from the mode, as in every cell: Null for null (every
// read is the end of the stream), else Loop; Mk's direct mode is Loop
// here. Prints n, sum and the failed calls.
// Usage: inmemconn <n> [mode].
use std::io;
use std::sync::Mutex;

trait Transport {
    fn read(&self, buf: &mut [u8]) -> io::Result<usize>;
    fn write_all(&self, data: &[u8]) -> io::Result<()>;
}

struct Loop {
    held: Mutex<([u8; 32], usize)>,
}

impl Transport for Loop {
    fn read(&self, buf: &mut [u8]) -> io::Result<usize> {
        let mut g = self.held.lock().unwrap();
        let n = g.1.min(buf.len());
        buf[..n].copy_from_slice(&g.0[..n]);
        g.1 = 0;
        Ok(n)
    }
    fn write_all(&self, data: &[u8]) -> io::Result<()> {
        let mut g = self.held.lock().unwrap();
        let n = data.len().min(32);
        g.0[..n].copy_from_slice(&data[..n]);
        g.1 = n;
        Ok(())
    }
}

// Null reads the end of the stream and drops what is written.
struct Null;

impl Transport for Null {
    fn read(&self, _buf: &mut [u8]) -> io::Result<usize> {
        Ok(0)
    }
    fn write_all(&self, _data: &[u8]) -> io::Result<()> {
        Ok(())
    }
}

struct Conn {
    reads: Mutex<()>,
    writes: Mutex<()>,
    t: Box<dyn Transport + Send + Sync>,
}

impl Conn {
    fn read(&self, buf: &mut [u8]) -> io::Result<usize> {
        let _g = self.reads.lock().unwrap();
        self.t.read(buf)
    }
    fn write_all(&self, data: &[u8]) -> io::Result<()> {
        let _g = self.writes.lock().unwrap();
        self.t.write_all(data)
    }
}

fn main() {
    let n: u64 = std::env::args().nth(1).unwrap().parse().unwrap();
    let mode = std::env::args().nth(2).unwrap_or_default();
    let t: Box<dyn Transport + Send + Sync> = if mode == "null" {
        Box::new(Null)
    } else {
        Box::new(Loop { held: Mutex::new(([0; 32], 0)) })
    };
    let c = Conn {
        reads: Mutex::new(()),
        writes: Mutex::new(()),
        t,
    };
    let mut msg = [7u8; 32];
    let mut buf = [0u8; 32];
    let mut sum: u64 = 0;
    let mut fails = 0u64;
    for i in 0..n {
        msg[0] = i as u8;
        let ok = c.write_all(&msg).is_ok();
        let k = c.read(&mut buf).unwrap_or(0);
        if !ok || k != 32 {
            fails += 1;
        }
        sum = sum.wrapping_mul(31).wrapping_add(buf[0] as u64);
    }
    println!("{} {} {}", n, sum, fails);
}
