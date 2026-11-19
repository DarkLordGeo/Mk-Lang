// grep-j8, Rust, eco: the task of README.md, app/grep's re variant searched
// by -j OS threads, with walkdir 2.5.0 (sort_by_file_name, bytewise on these
// names; filter_entry skips names starting with ".") and regex 1.12.3's
// bytes::Regex, (?i) for -i (Unicode simple case folding). The walk lists
// every file first, in mgrep's order. Then -j threads of std::thread::scope
// take file indexes from an AtomicUsize; each reads its file whole with
// std::fs::read, skips it as binary when a NUL byte is in its first 1,024
// bytes, and counts the lines that hold a match. A file that is valid UTF-8
// (std::str::from_utf8) is searched whole: the next match from the current
// line's start is found, its line counted, and the search goes on at the
// next line. A file that is not has its lines judged one by one, and a line
// that is not valid UTF-8 is skipped. When every thread is done, the
// results are put in walk order and "path:count" is printed for every file
// not skipped.
// Usage: grepj8 -c -j N --path-separator / -i PATTERN DIR.
use regex::bytes::Regex;
use std::io::{BufWriter, Write};
use std::process::exit;
use std::sync::atomic::{AtomicUsize, Ordering};
use walkdir::WalkDir;

const SNIFF: usize = 1024;

// One file's outcome: its count, or None when it was skipped as binary, or
// the error that stopped it.
type Outcome = Result<Option<usize>, std::io::Error>;

fn usage() -> ! {
    eprintln!("usage: grepj8 -c -j N --path-separator / -i PATTERN DIR");
    exit(2);
}

fn main() {
    let args: Vec<String> = std::env::args().skip(1).collect();
    let (mut fold, mut counts) = (false, false);
    let mut threads = 0usize;
    let mut rest: Vec<&str> = Vec::new();
    let mut i = 0;
    while i < args.len() {
        match args[i].as_str() {
            "-c" => counts = true,
            "-i" => fold = true,
            "-j" => {
                i += 1;
                threads = match args.get(i).and_then(|s| s.parse().ok()) {
                    Some(n) if (1..=256).contains(&n) => n,
                    _ => usage(),
                };
            }
            "--path-separator" => {
                i += 1;
                if args.get(i).map(String::as_str) != Some("/") {
                    usage();
                }
            }
            a if a.starts_with('-') => usage(),
            a => rest.push(a),
        }
        i += 1;
    }
    if !counts || threads == 0 || rest.len() != 2 {
        usage();
    }
    let (pat, root) = (rest[0], rest[1]);
    let text = if fold {
        format!("(?i){pat}")
    } else {
        pat.to_string()
    };
    let re = Regex::new(&text).unwrap_or_else(|e| {
        eprintln!("grepj8: {e}");
        exit(2);
    });
    let shown_root = root.replace('\\', "/");
    let mut failed = false;
    let mut files: Vec<(std::path::PathBuf, String)> = Vec::new();
    let walker = WalkDir::new(root)
        .sort_by_file_name()
        .into_iter()
        .filter_entry(|e| e.depth() == 0 || !e.file_name().as_encoded_bytes().starts_with(b"."));
    for entry in walker {
        let entry = match entry {
            Ok(e) => e,
            Err(e) => {
                eprintln!("grepj8: {e}");
                failed = true;
                continue;
            }
        };
        if entry.file_type().is_dir() {
            continue;
        }
        let rel = entry.path().strip_prefix(root).unwrap_or(entry.path());
        let mut shown = shown_root.clone();
        for part in rel.components() {
            if !shown.ends_with('/') {
                shown.push('/');
            }
            shown.push_str(&part.as_os_str().to_string_lossy());
        }
        files.push((entry.into_path(), shown));
    }
    let next = AtomicUsize::new(0);
    let mut results: Vec<(usize, Outcome)> = std::thread::scope(|s| {
        let workers: Vec<_> = (0..threads)
            .map(|_| {
                s.spawn(|| {
                    let mut mine = Vec::new();
                    loop {
                        let i = next.fetch_add(1, Ordering::Relaxed);
                        if i >= files.len() {
                            return mine;
                        }
                        mine.push((i, search(&re, &files[i].0)));
                    }
                })
            })
            .collect();
        workers
            .into_iter()
            .flat_map(|w| w.join().unwrap_or_else(|_| exit(2)))
            .collect()
    });
    results.sort_by_key(|r| r.0);
    let mut out = BufWriter::with_capacity(64 << 10, std::io::stdout().lock());
    let mut matched = false;
    for (i, r) in results {
        match r {
            Err(e) => {
                eprintln!("grepj8: {}: {e}", files[i].1);
                failed = true;
            }
            Ok(None) => {}
            Ok(Some(n)) => {
                matched |= n > 0;
                if let Err(e) = writeln!(out, "{}:{n}", files[i].1) {
                    eprintln!("grepj8: {e}");
                    exit(2);
                }
            }
        }
    }
    if let Err(e) = out.flush() {
        eprintln!("grepj8: {e}");
        exit(2);
    }
    if failed {
        exit(2);
    }
    if !matched {
        exit(1);
    }
}

// Reads one file and counts its lines that hold a match; None for a binary
// file.
fn search(re: &Regex, path: &std::path::Path) -> Outcome {
    let buf = std::fs::read(path)?;
    if buf[..buf.len().min(SNIFF)].contains(&0) {
        return Ok(None);
    }
    if std::str::from_utf8(&buf).is_ok() {
        return Ok(Some(lines(re, &buf)));
    }
    let mut n = 0;
    let mut rest = &buf[..];
    while !rest.is_empty() {
        let (mut line, next) = match rest.iter().position(|&b| b == b'\n') {
            Some(i) => (&rest[..i], i + 1),
            None => (rest, rest.len()),
        };
        if let [head @ .., b'\r'] = line {
            line = head;
        }
        if std::str::from_utf8(line).is_ok() && lines(re, line) > 0 {
            n += 1;
        }
        rest = &rest[next..];
    }
    Ok(Some(n))
}

// The lines of buf that hold a match: the next match from the start of the
// current line, then on from the line after it. The row's pattern matches no
// line break, so a match never spans two lines.
fn lines(re: &Regex, buf: &[u8]) -> usize {
    let (mut n, mut pos) = (0, 0);
    while pos < buf.len() {
        let Some(m) = re.find_at(buf, pos) else { break };
        n += 1;
        match buf[m.start()..].iter().position(|&b| b == b'\n') {
            Some(j) => pos = m.start() + j + 1,
            None => break,
        }
    }
    n
}
