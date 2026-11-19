// fs, Rust: the tree operations of tests/bench-lanes/std-fs2/README.md
// through std::fs. The tree is made under std::env::temp_dir in
// mkbench-fs2-rust: 10 directories d<i>, each with 10 directories s<j> of
// 10 one-byte files f<k>, 1,110 entries.
// Usage: fs walk <reps> | fs mkrm <reps> | fs copy <file> <reps>.
//   walk: makes the tree, lists it reps times by recursion over
//         fs::read_dir and DirEntry::file_type (std has no walker; the root
//         not counted), removes it; prints reps, directories, files and
//         name bytes, summed over the walks.
//   mkrm: reps times makes the tree (fs::create_dir_all and fs::write) and
//         removes it with fs::remove_dir_all; prints reps, entries made and
//         the removals that left the root behind.
//   copy: copies file reps times with fs::copy onto one destination;
//         prints reps and the bytes copied.
use std::fs;
use std::path::Path;

fn make_tree(root: &Path) -> u64 {
    let mut n = 0;
    for i in 0..10 {
        for j in 0..10 {
            let d = root.join(format!("d{i}")).join(format!("s{j}"));
            fs::create_dir_all(&d).unwrap();
            for k in 0..10 {
                fs::write(d.join(format!("f{k}")), b"x").unwrap();
            }
            n += 11;
        }
        n += 1;
    }
    n
}

fn walk(p: &Path, t: &mut (u64, u64, u64)) {
    for e in fs::read_dir(p).unwrap() {
        let e = e.unwrap();
        t.2 += e.file_name().len() as u64;
        if e.file_type().unwrap().is_dir() {
            t.0 += 1;
            walk(&e.path(), t);
        } else {
            t.1 += 1;
        }
    }
}

fn main() {
    let args: Vec<String> = std::env::args().collect();
    let root = std::env::temp_dir().join("mkbench-fs2-rust");
    if args[1] == "copy" {
        let n: u64 = args[3].parse().unwrap();
        let dst = std::env::temp_dir().join("mkbench-fs2-rust-copy");
        let mut bytes: u64 = 0;
        for _ in 0..n {
            bytes += fs::copy(&args[2], &dst).unwrap();
        }
        fs::remove_file(&dst).unwrap();
        println!("{} {}", n, bytes);
        return;
    }
    let n: u64 = args[2].parse().unwrap();
    if root.exists() {
        fs::remove_dir_all(&root).unwrap();
    }
    if args[1] == "walk" {
        make_tree(&root);
        let mut t = (0, 0, 0);
        for _ in 0..n {
            walk(&root, &mut t);
        }
        fs::remove_dir_all(&root).unwrap();
        println!("{} {} {} {}", n, t.0, t.1, t.2);
        return;
    }
    let (mut made, mut fails) = (0, 0);
    for _ in 0..n {
        made += make_tree(&root);
        fs::remove_dir_all(&root).unwrap();
        if fs::symlink_metadata(&root).is_ok() {
            fails += 1;
        }
    }
    println!("{} {} {}", n, made, fails);
}
