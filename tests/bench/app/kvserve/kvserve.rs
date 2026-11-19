// kvserve, Rust, std plus a hand CRC-32: the task of README.md, the subset
// of kvd's line protocol the load uses (PING, GET, SET, INCR, DEL, QUIT)
// over TCP, every change appended to a write-ahead log in kvd's format
// before it is answered. Usage: kvserve serve --addr HOST:PORT --data DIR
// [--sync always|never|N]. Serves until killed. One std::thread per
// connection (TcpListener::incoming), a 4 KiB BufReader and BufWriter on
// each, the writer flushed after every reply. The store is a
// HashMap<String, String> (std's RandomState, SipHash-1-3) behind an
// RwLock; the log is a File opened to append behind a Mutex, each record
// written whole by one write_all. Rust's std has no CRC, so the CRC-32
// (IEEE, reflected polynomial 0xEDB88320) is written here, a 256-entry
// table built at start: the cell's one hand part. A change takes the log's
// lock, reads the store where it depends on it (DEL, INCR), appends its
// record, syncs it as the policy says, then takes the store's lock and
// applies it, as kvd's Change does, so the log's order is the store's.
use std::collections::HashMap;
use std::fs::{self, File, OpenOptions};
use std::io::{self, BufRead, BufReader, BufWriter, Read, Write};
use std::net::{TcpListener, TcpStream};
use std::path::Path;
use std::process::exit;
use std::sync::{Arc, Mutex, RwLock};
use std::thread;

const MAX_KEY: usize = 256;
const MAX_VALUE: usize = 65536;
const LINE_LIMIT: usize = MAX_KEY + MAX_VALUE + 64;
const OP_SET: u8 = 1;
const OP_DEL: u8 = 2;

// The hand CRC-32: IEEE, reflected, initial and final xor 0xFFFFFFFF.
struct Crc32 {
    table: [u32; 256],
}

impl Crc32 {
    fn new() -> Crc32 {
        let mut table = [0u32; 256];
        for (i, t) in table.iter_mut().enumerate() {
            let mut c = i as u32;
            for _ in 0..8 {
                c = if c & 1 != 0 { 0xEDB8_8320 ^ (c >> 1) } else { c >> 1 };
            }
            *t = c;
        }
        Crc32 { table }
    }

    fn sum(&self, data: &[u8]) -> u32 {
        let mut c = 0xFFFF_FFFFu32;
        for &b in data {
            c = self.table[((c ^ b as u32) & 0xFF) as usize] ^ (c >> 8);
        }
        c ^ 0xFFFF_FFFF
    }
}

struct Log {
    file: File,
    every: usize, // 0: never sync; 1: each change; n: every n changes
    pending: usize,
    rec: Vec<u8>,
}

struct Server {
    store: RwLock<HashMap<String, String>>,
    log: Mutex<Log>,
    crc: Crc32,
}

fn usage(msg: &str) -> ! {
    eprintln!("kvserve: {}", msg);
    exit(2)
}

fn main() {
    let args: Vec<String> = std::env::args().skip(1).collect();
    if args.first().map(|s| s.as_str()) != Some("serve") {
        usage("usage: kvserve serve --addr HOST:PORT --data DIR [--sync always|never|N]");
    }
    let (mut addr, mut dir, mut sync) = ("127.0.0.1:7379".to_string(), "kvd-data".to_string(), "always".to_string());
    let mut i = 1;
    while i < args.len() {
        if i + 1 >= args.len() {
            usage(&format!("{} needs a value", args[i]));
        }
        match args[i].as_str() {
            "--addr" => addr = args[i + 1].clone(),
            "--data" => dir = args[i + 1].clone(),
            "--sync" => sync = args[i + 1].clone(),
            f => usage(&format!("unknown flag {}", f)),
        }
        i += 2;
    }
    let every = match sync.as_str() {
        "always" => 1,
        "never" => 0,
        s => match s.parse::<usize>() {
            Ok(n) if (1..=1_000_000).contains(&n) => n,
            _ => usage("--sync takes always, never or a number of changes"),
        },
    };
    let crc = Crc32::new();
    let file = match open_log(&dir, &crc) {
        Ok(f) => f,
        Err(e) => {
            eprintln!("kvserve: {}: {}", dir, e);
            exit(1)
        }
    };
    let server = Arc::new(Server {
        store: RwLock::new(HashMap::new()),
        log: Mutex::new(Log { file, every, pending: 0, rec: Vec::new() }),
        crc,
    });
    let listener = match TcpListener::bind(&addr) {
        Ok(l) => l,
        Err(e) => {
            eprintln!("kvserve: listen on {}: {}", addr, e);
            exit(1)
        }
    };
    eprintln!("kvserve: listening on {}", addr);
    for stream in listener.incoming() {
        match stream {
            Ok(s) => {
                let _ = s.set_nodelay(true);
                let server = Arc::clone(&server);
                thread::spawn(move || serve(&server, s));
            }
            Err(e) => {
                eprintln!("kvserve: accept: {}", e);
                exit(1)
            }
        }
    }
}

// A fresh log in dir: the 24-byte header of generation 0, not shut down
// cleanly, synced. Recovery is kvd's; a twin starts empty, so it refuses a
// directory that already holds a log.
fn open_log(dir: &str, crc: &Crc32) -> io::Result<File> {
    fs::create_dir_all(dir)?;
    let mut f = OpenOptions::new().append(true).create_new(true).open(Path::new(dir).join("kvd.wal"))?;
    let mut h = [0u8; 24];
    h[..8].copy_from_slice(b"KVDWAL1\n");
    let sum = crc.sum(&h[..16]);
    h[16..20].copy_from_slice(&sum.to_le_bytes());
    f.write_all(&h)?;
    f.sync_all()?;
    Ok(f)
}

impl Server {
    // One record (length, CRC-32 of the payload, payload: operation, key
    // length, key, value), synced as the policy says. The caller holds the
    // log's lock.
    fn append(&self, log: &mut Log, op: u8, key: &str, value: &str) -> io::Result<()> {
        let n = 3 + key.len() + value.len();
        let mut r = std::mem::take(&mut log.rec);
        r.clear();
        r.extend_from_slice(&(n as u32).to_le_bytes());
        r.extend_from_slice(&[0, 0, 0, 0, op]);
        r.extend_from_slice(&(key.len() as u16).to_le_bytes());
        r.extend_from_slice(key.as_bytes());
        r.extend_from_slice(value.as_bytes());
        let sum = self.crc.sum(&r[8..]);
        r[4..8].copy_from_slice(&sum.to_le_bytes());
        let res = log.file.write_all(&r);
        log.rec = r;
        res?;
        if log.every > 0 {
            log.pending += 1;
            if log.pending >= log.every {
                log.pending = 0;
                log.file.sync_data()?;
            }
        }
        Ok(())
    }

    // Logs a change, then applies it to the store; false when the log
    // refused it (the reply is then the error).
    fn apply(&self, log: &mut Log, w: &mut impl Write, op: u8, key: &str, value: &str) -> io::Result<bool> {
        if let Err(e) = self.append(log, op, key, value) {
            writeln!(w, "-ERR {}", e)?;
            return Ok(false);
        }
        let mut store = self.store.write().unwrap();
        if op == OP_DEL {
            store.remove(key);
        } else {
            store.insert(key.to_string(), value.to_string());
        }
        Ok(true)
    }

    // Runs one request and writes its reply; Ok(true) is QUIT.
    fn answer(&self, w: &mut impl Write, text: &str) -> io::Result<bool> {
        let (word, rest) = text.split_once(' ').unwrap_or((text, ""));
        match word {
            "" => w.write_all(b"-ERR Empty\n")?,
            "PING" | "ping" => {
                if !rest.is_empty() {
                    w.write_all(b"-ERR ExtraArgument\n")?;
                } else {
                    w.write_all(b"+PONG\n")?;
                }
            }
            "GET" | "get" => {
                if let Some(key) = key_only(w, rest)? {
                    let store = self.store.read().unwrap();
                    match store.get(key) {
                        Some(v) => {
                            w.write_all(b"$")?;
                            w.write_all(v.as_bytes())?;
                            w.write_all(b"\n")?;
                        }
                        None => w.write_all(b"_\n")?,
                    }
                }
            }
            "SET" | "set" => {
                let (key, value) = rest.split_once(' ').unwrap_or((rest, ""));
                if check_key(w, key)? && check_value(w, value)? {
                    let mut log = self.log.lock().unwrap();
                    if self.apply(&mut log, w, OP_SET, key, value)? {
                        w.write_all(b"+OK\n")?;
                    }
                }
            }
            "INCR" | "incr" => {
                if let Some(key) = key_only(w, rest)? {
                    let mut log = self.log.lock().unwrap();
                    let n = {
                        let store = self.store.read().unwrap();
                        match store.get(key) {
                            None => Some(0i64),
                            Some(v) => v.parse::<i64>().ok(),
                        }
                    };
                    match n {
                        None => w.write_all(b"-ERR NotAnInteger\n")?,
                        Some(n) => {
                            let next = (n + 1).to_string();
                            if self.apply(&mut log, w, OP_SET, key, &next)? {
                                writeln!(w, ":{}", next)?;
                            }
                        }
                    }
                }
            }
            "DEL" | "del" => {
                if let Some(key) = key_only(w, rest)? {
                    let mut log = self.log.lock().unwrap();
                    let found = self.store.read().unwrap().contains_key(key);
                    if !found {
                        w.write_all(b":0\n")?;
                    } else if self.apply(&mut log, w, OP_DEL, key, "")? {
                        w.write_all(b":1\n")?;
                    }
                }
            }
            "QUIT" | "quit" => {
                if !rest.is_empty() {
                    w.write_all(b"-ERR ExtraArgument\n")?;
                } else {
                    w.write_all(b"+BYE\n")?;
                    return Ok(true);
                }
            }
            _ => w.write_all(b"-ERR UnknownCommand\n")?,
        }
        Ok(false)
    }
}

// A command's one key, with nothing after it; None when the reply is an
// error, already written.
fn key_only<'a>(w: &mut impl Write, rest: &'a str) -> io::Result<Option<&'a str>> {
    let (key, extra) = rest.split_once(' ').unwrap_or((rest, ""));
    if !extra.is_empty() {
        w.write_all(b"-ERR ExtraArgument\n")?;
        return Ok(None);
    }
    Ok(if check_key(w, key)? { Some(key) } else { None })
}

// Non-empty, at most MAX_KEY bytes, no space or control byte.
fn check_key(w: &mut impl Write, key: &str) -> io::Result<bool> {
    if key.is_empty() {
        w.write_all(b"-ERR MissingArgument\n")?;
        return Ok(false);
    }
    if key.len() > MAX_KEY {
        writeln!(w, "-ERR KeyTooLong(len: {}, limit: {})", key.len(), MAX_KEY)?;
        return Ok(false);
    }
    if let Some(i) = key.bytes().position(|c| c <= b' ' || c == 0x7F) {
        writeln!(w, "-ERR BadByte(offset: {})", i)?;
        return Ok(false);
    }
    Ok(true)
}

// At most MAX_VALUE bytes, no line break.
fn check_value(w: &mut impl Write, value: &str) -> io::Result<bool> {
    if value.len() > MAX_VALUE {
        writeln!(w, "-ERR ValueTooLong(len: {}, limit: {})", value.len(), MAX_VALUE)?;
        return Ok(false);
    }
    if let Some(i) = value.bytes().position(|c| c == b'\n' || c == b'\r') {
        writeln!(w, "-ERR BadByte(offset: {})", i)?;
        return Ok(false);
    }
    Ok(true)
}

// Answers one connection's lines until it leaves. A line is at most
// LINE_LIMIT bytes with its "\n"; one cut off by the end of the stream is
// never run.
fn serve(server: &Server, stream: TcpStream) {
    let read = match stream.try_clone() {
        Ok(s) => s,
        Err(_) => return,
    };
    let mut r = BufReader::with_capacity(4096, read);
    let mut w = BufWriter::with_capacity(4096, stream);
    let mut line = Vec::with_capacity(256);
    loop {
        line.clear();
        match r.by_ref().take(LINE_LIMIT as u64).read_until(b'\n', &mut line) {
            Ok(0) | Err(_) => return,
            Ok(_) => {}
        }
        if line.last() != Some(&b'\n') {
            if line.len() == LINE_LIMIT {
                let _ = w.write_all(b"-ERR LineTooLong\n");
                let _ = w.flush();
            }
            return;
        }
        line.pop();
        let quit = match std::str::from_utf8(&line) {
            Ok(text) => server.answer(&mut w, text.trim_ascii()),
            Err(e) => writeln!(w, "-ERR BadByte(offset: {})", e.valid_up_to()).map(|_| false),
        };
        match quit {
            Ok(q) => {
                if w.flush().is_err() || q {
                    return;
                }
            }
            Err(_) => return,
        }
    }
}
