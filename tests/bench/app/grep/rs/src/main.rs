// grep, Rust, eco: the task of README.md with walkdir 2.5.0 (sort_by_file_name,
// bytewise on these names; filter_entry skips names starting with ".") and
// regex 1.12.3's bytes::Regex for every variant: a literal is escaped, a
// folded literal is (?i-u), which folds ASCII case only, as mgrep's literal
// search does, and -i on a regex is (?i), Unicode simple case folding. A
// file is read whole with std::fs::read. A file with a NUL byte in its first
// 1,024 bytes is skipped as binary; every other file prints "path:count",
// the number of its lines that hold a match. A file that is valid UTF-8
// (std::str::from_utf8) is searched whole: the next match from the current
// line's start is found, its line counted, and the search goes on at the
// next line. A file that is not has its lines judged one by one, and a line
// that is not valid UTF-8 is skipped.
// Usage: grep -c -j 1 --path-separator / [-F|-i] PATTERN DIR.
use regex::bytes::Regex;
use std::io::{BufWriter, Write};
use std::process::exit;
use walkdir::WalkDir;

const SNIFF: usize = 1024;

// The bytes that make a pattern a regex (mgrep's MetaByte).
const META: &[u8] = b"\\.+*?()|[]{}^$";

fn usage() -> ! {
    eprintln!("usage: grep -c -j 1 --path-separator / [-F|-i] PATTERN DIR");
    exit(2);
}

fn main() {
    let args: Vec<String> = std::env::args().skip(1).collect();
    let (mut fixed, mut fold, mut counts) = (false, false, false);
    let mut rest: Vec<&str> = Vec::new();
    let mut i = 0;
    while i < args.len() {
        match args[i].as_str() {
            "-c" => counts = true,
            "-F" => fixed = true,
            "-i" => fold = true,
            "-j" => {
                i += 1;
                if args.get(i).map(String::as_str) != Some("1") {
                    usage();
                }
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
    if !counts || rest.len() != 2 {
        usage();
    }
    let (pat, root) = (rest[0], rest[1]);
    let literal = !pat.is_empty()
        && (fixed || !pat.bytes().any(|b| META.contains(&b)))
        && (!fold || pat.is_ascii());
    let text = if literal {
        let escaped = regex::escape(pat);
        if fold {
            format!("(?i-u){escaped}")
        } else {
            escaped
        }
    } else {
        let p = if fixed {
            regex::escape(pat)
        } else {
            pat.to_string()
        };
        if fold { format!("(?i){p}") } else { p }
    };
    let re = Regex::new(&text).unwrap_or_else(|e| {
        eprintln!("grep: {e}");
        exit(2);
    });
    let shown_root = root.replace('\\', "/");
    let mut out = BufWriter::with_capacity(64 << 10, std::io::stdout().lock());
    let (mut matched, mut failed) = (false, false);
    let walker = WalkDir::new(root)
        .sort_by_file_name()
        .into_iter()
        .filter_entry(|e| e.depth() == 0 || !e.file_name().as_encoded_bytes().starts_with(b"."));
    for entry in walker {
        let entry = match entry {
            Ok(e) => e,
            Err(e) => {
                eprintln!("grep: {e}");
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
        let buf = match std::fs::read(entry.path()) {
            Ok(b) => b,
            Err(e) => {
                eprintln!("grep: {shown}: {e}");
                failed = true;
                continue;
            }
        };
        if buf[..buf.len().min(SNIFF)].contains(&0) {
            continue;
        }
        let n = count(&re, &buf);
        matched |= n > 0;
        if let Err(e) = writeln!(out, "{shown}:{n}") {
            eprintln!("grep: {e}");
            exit(2);
        }
    }
    if let Err(e) = out.flush() {
        eprintln!("grep: {e}");
        exit(2);
    }
    if failed {
        exit(2);
    }
    if !matched {
        exit(1);
    }
}

// The number of lines of buf that hold a match. A buffer that is not valid
// UTF-8 is judged line by line, and its invalid lines are skipped.
fn count(re: &Regex, buf: &[u8]) -> usize {
    if std::str::from_utf8(buf).is_ok() {
        return lines(re, buf);
    }
    let mut n = 0;
    let mut rest = buf;
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
    n
}

// The lines of buf that hold a match: the next match from the start of the
// current line, then on from the line after it. The row's patterns match no
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
