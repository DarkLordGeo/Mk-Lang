// line-echo, Rust: a buffered line echo through std::io::BufReader and
// BufWriter (4096-byte buffers at each end). Each of n rounds writes 16
// lines through the client's BufWriter and flushes, reads each line at
// the server with read_until(b'\n') into a reused Vec and writes it back
// through the server's BufWriter, flushes, and reads the 16 lines back at
// the client with read_until. A line is 32 bytes: byte 0 is 32 + k mod 64
// for the k-th line sent, then 30 'x' and a newline. sum = 31 sum + byte
// 0 + 256 * length of each line read back at the client (wrapping). Mode
// tcp runs over one loopback std::net::TcpStream (blocking, TCP_NODELAY
// at both ends; &TcpStream is Read and Write); mode inmem over two
// in-memory pipes of 4096 bytes, each under its own std::sync::Mutex, one
// connection per end that holds its transport as a Box<dyn Transport +
// Send + Sync> and takes a read or a write Mutex of its own around each
// call. The in-memory transport is picked at run time from the mode, as
// in every cell: Null for null (every read is the end of the stream), else
// End. Prints n, sum and the failed calls.
// Usage: lineecho <n> tcp|inmem|null.
use std::io::{self, BufRead, BufReader, BufWriter, Read, Write};
use std::net::{TcpListener, TcpStream};
use std::sync::{Arc, Mutex};

const LINES: usize = 16;

// Pipe holds the bytes written to one direction and not yet read.
struct Pipe {
    data: [u8; 4096],
    start: usize,
    end: usize,
}

trait Transport {
    fn read(&self, buf: &mut [u8]) -> io::Result<usize>;
    fn write_all(&self, data: &[u8]) -> io::Result<()>;
}

// End is one end of the in-memory duplex: it reads rx and writes tx.
struct End {
    rx: Arc<Mutex<Pipe>>,
    tx: Arc<Mutex<Pipe>>,
}

impl Transport for End {
    fn read(&self, buf: &mut [u8]) -> io::Result<usize> {
        let mut p = self.rx.lock().unwrap();
        let n = (p.end - p.start).min(buf.len());
        let s = p.start;
        buf[..n].copy_from_slice(&p.data[s..s + n]);
        p.start += n;
        if p.start == p.end {
            p.start = 0;
            p.end = 0;
        }
        Ok(n)
    }
    fn write_all(&self, data: &[u8]) -> io::Result<()> {
        let mut p = self.tx.lock().unwrap();
        let e = p.end;
        if data.len() > 4096 - e {
            panic!("lineecho: a pipe overflowed");
        }
        p.data[e..e + data.len()].copy_from_slice(data);
        p.end += data.len();
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

impl Read for &Conn {
    fn read(&mut self, buf: &mut [u8]) -> io::Result<usize> {
        let _g = self.reads.lock().unwrap();
        self.t.read(buf)
    }
}

impl Write for &Conn {
    fn write(&mut self, data: &[u8]) -> io::Result<usize> {
        let _g = self.writes.lock().unwrap();
        self.t.write_all(data)?;
        Ok(data.len())
    }
    fn write_all(&mut self, data: &[u8]) -> io::Result<()> {
        let _g = self.writes.lock().unwrap();
        self.t.write_all(data)
    }
    fn flush(&mut self) -> io::Result<()> {
        Ok(())
    }
}

fn echo<C: Read + Write, S: Read + Write>(n: u64, c: C, cr: C, s: S, sr: S) -> (u64, u64) {
    let mut cw = BufWriter::with_capacity(4096, c);
    let mut cr = BufReader::with_capacity(4096, cr);
    let mut sw = BufWriter::with_capacity(4096, s);
    let mut sr = BufReader::with_capacity(4096, sr);
    let mut line = [b'x'; 32];
    line[31] = b'\n';
    let mut got: Vec<u8> = Vec::with_capacity(64);
    let mut sum: u64 = 0;
    let mut fails = 0u64;
    let mut k = 0usize;
    for _ in 0..n {
        for _ in 0..LINES {
            line[0] = (32 + k % 64) as u8;
            k += 1;
            if cw.write_all(&line).is_err() {
                fails += 1;
            }
        }
        if cw.flush().is_err() {
            fails += 1;
        }
        for _ in 0..LINES {
            got.clear();
            if sr.read_until(b'\n', &mut got).is_err() {
                fails += 1;
            }
            if sw.write_all(&got).is_err() {
                fails += 1;
            }
        }
        if sw.flush().is_err() {
            fails += 1;
        }
        for _ in 0..LINES {
            got.clear();
            let m = cr.read_until(b'\n', &mut got).unwrap_or(0);
            if m != 32 {
                fails += 1;
            }
            let b0 = got.first().copied().unwrap_or(0) as u64;
            sum = sum.wrapping_mul(31).wrapping_add(b0).wrapping_add(256 * m as u64);
        }
    }
    (sum, fails)
}

fn main() {
    let n: u64 = std::env::args().nth(1).unwrap().parse().unwrap();
    let mode = std::env::args().nth(2).unwrap_or_default();
    let (sum, fails) = if mode == "inmem" || mode == "null" {
        let pipe = || Arc::new(Mutex::new(Pipe { data: [0; 4096], start: 0, end: 0 }));
        let (up, down) = (pipe(), pipe());
        let open = |rx: Arc<Mutex<Pipe>>, tx: Arc<Mutex<Pipe>>| -> Box<dyn Transport + Send + Sync> {
            if mode == "null" {
                Box::new(Null)
            } else {
                Box::new(End { rx, tx })
            }
        };
        let c = Conn {
            reads: Mutex::new(()),
            writes: Mutex::new(()),
            t: open(down.clone(), up.clone()),
        };
        let s = Conn {
            reads: Mutex::new(()),
            writes: Mutex::new(()),
            t: open(up, down),
        };
        echo(n, &c, &c, &s, &s)
    } else {
        let l = TcpListener::bind("127.0.0.1:0").unwrap();
        let d = TcpStream::connect(l.local_addr().unwrap()).unwrap();
        let (a, _) = l.accept().unwrap();
        d.set_nodelay(true).unwrap();
        a.set_nodelay(true).unwrap();
        echo(n, &d, &d, &a, &a)
    };
    println!("{} {} {}", n, sum, fails);
}
