// jsonl, Rust, eco serde 1 and serde_json 1 (Cargo.lock pins them): the
// task of ../../README.md. The file is read through a BufReader with a
// 64 KiB buffer, one line at a time with read_line into one reused String;
// serde_json::from_str decodes each line into an Event, whose
// #[derive(Deserialize)] reads the members by name. Kept events are grouped
// by user in a std HashMap<String, Group> (its default hasher), the groups
// are sorted with sort_by, and the Report is written by
// serde_json::to_writer (compact JSON, members in field order) to a
// BufWriter over stdout, then a line feed.
// Usage: jsonl <file> [<lines>].
use serde::{Deserialize, Serialize};
use std::collections::HashMap;
use std::fs::File;
use std::io::{BufRead, BufReader, BufWriter, Write};

#[derive(Deserialize)]
struct Event {
    id: i64,
    user: String,
    event: String,
    status: i64,
    bytes: i64,
    ms: i64,
    delta: i64,
    ok: bool,
    tags: Vec<String>,
    msg: String,
}

#[derive(Serialize)]
struct Group {
    user: String,
    count: i64,
    first: i64,
    bytes: i64,
    ms_min: i64,
    ms_max: i64,
    delta: i64,
    tags: i64,
    msg: i64,
    buys: i64,
    fails: i64,
}

#[derive(Serialize)]
struct Report {
    lines: i64,
    kept: i64,
    dropped: i64,
    groups: Vec<Group>,
}

impl Group {
    fn add(&mut self, e: &Event) {
        self.count += 1;
        self.bytes += e.bytes;
        self.ms_min = self.ms_min.min(e.ms);
        self.ms_max = self.ms_max.max(e.ms);
        self.delta += e.delta;
        self.tags += e.tags.len() as i64;
        self.msg += e.msg.len() as i64;
        if e.event == "buy" {
            self.buys += 1;
        }
        if e.status >= 400 {
            self.fails += 1;
        }
    }
}

fn run() -> Result<(), Box<dyn std::error::Error>> {
    let args: Vec<String> = std::env::args().collect();
    if args.len() < 2 || args.len() > 3 {
        return Err("usage: jsonl <file> [<lines>]".into());
    }
    let limit: i64 = if args.len() == 3 {
        args[2].parse()?
    } else {
        -1
    };
    let mut r = BufReader::with_capacity(64 << 10, File::open(&args[1])?);
    let mut groups: HashMap<String, Group> = HashMap::new();
    let mut line = String::new();
    let (mut n, mut kept) = (0i64, 0i64);
    while n != limit {
        line.clear();
        if r.read_line(&mut line)? == 0 {
            break;
        }
        n += 1;
        let text = line.strip_suffix('\n').unwrap_or(&line);
        let text = text.strip_suffix('\r').unwrap_or(text);
        let e: Event = serde_json::from_str(text).map_err(|err| format!("line {n}: {err}"))?;
        if !e.ok {
            continue;
        }
        kept += 1;
        if let Some(g) = groups.get_mut(e.user.as_str()) {
            g.add(&e);
        } else {
            let mut g = Group {
                user: e.user.clone(),
                count: 0,
                first: e.id,
                bytes: 0,
                ms_min: e.ms,
                ms_max: e.ms,
                delta: 0,
                tags: 0,
                msg: 0,
                buys: 0,
                fails: 0,
            };
            g.add(&e);
            groups.insert(e.user, g);
        }
    }
    let mut gs: Vec<Group> = groups.into_values().collect();
    gs.sort_by(|a, b| {
        b.count
            .cmp(&a.count)
            .then(b.bytes.cmp(&a.bytes))
            .then_with(|| a.user.cmp(&b.user))
    });
    let mut w = BufWriter::with_capacity(64 << 10, std::io::stdout().lock());
    serde_json::to_writer(
        &mut w,
        &Report {
            lines: n,
            kept,
            dropped: n - kept,
            groups: gs,
        },
    )?;
    w.write_all(b"\n")?;
    w.flush()?;
    Ok(())
}

fn main() {
    if let Err(e) = run() {
        eprintln!("jsonl: {e}");
        std::process::exit(1);
    }
}
