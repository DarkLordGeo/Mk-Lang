// binarytrees, Rust: the reference algorithm of README.md (the Benchmarks
// Game's binary-trees, with the row's bump allocator). Nodes are Node { left: u32,
// right: u32 } in one Vec<Node>, allocated by a u32 bump index in a Pool
// passed by &mut; index 0 is the null node. No Box, no per-node allocation.
// nodes[idx as usize] is bounds checked; + wraps in "rust", traps in
// "rust-ovf". No unsafe, no crates.

#[derive(Clone, Copy)]
struct Node {
    left: u32,
    right: u32,
}

struct Pool {
    nodes: Vec<Node>,
    top: u32,
}

fn build(p: &mut Pool, depth: i64) -> u32 {
    let idx = p.top;
    p.top += 1;
    if depth > 0 {
        let l = build(p, depth - 1);
        let r = build(p, depth - 1);
        p.nodes[idx as usize] = Node { left: l, right: r };
    } else {
        p.nodes[idx as usize] = Node { left: 0, right: 0 };
    }
    idx
}

fn check(nodes: &[Node], idx: u32) -> i64 {
    let n = nodes[idx as usize];
    if n.left == 0 {
        return 1;
    }
    1 + check(nodes, n.left) + check(nodes, n.right)
}

fn main() {
    let args: Vec<String> = std::env::args().collect();
    if args.len() != 2 {
        eprintln!("usage: binarytrees <n>");
        std::process::exit(2);
    }
    let n: i64 = args[1].parse().unwrap();
    let min_depth: i64 = 4;
    let mut max_depth = n;
    if min_depth + 2 > max_depth {
        max_depth = min_depth + 2;
    }
    let cap = (1usize << (max_depth + 2)) + 1;
    let mut p = Pool { nodes: vec![Node { left: 0, right: 0 }; cap], top: 1 };
    let t = build(&mut p, max_depth + 1);
    let mut total = check(&p.nodes, t);
    p.top = 1;
    let long_lived = build(&mut p, max_depth);
    let mark = p.top;
    let mut d = min_depth;
    while d <= max_depth {
        let iterations: i64 = 1 << (max_depth - d + min_depth);
        for _ in 0..iterations {
            p.top = mark;
            let t = build(&mut p, d);
            total += check(&p.nodes, t);
        }
        d += 2;
    }
    total += check(&p.nodes, long_lived);
    println!("{}", total);
}
