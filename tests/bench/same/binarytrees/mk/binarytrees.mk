// binarytrees, Mk: the reference algorithm of ../README.md (the Benchmarks
// Game's binary-trees, with the row's bump allocator). Nodes are Node(left, right) of
// two u32 indices in one []Node from the arena, allocated by a u32 bump index
// in a Pool passed as mut, as in every column; index 0 is the null node.
// Every index is bounds checked, every integer + checked.
// Usage: binarytrees <n>. Prints the sum of every check.

struct Node {
    left: u32
    right: u32
}

struct Pool {
    nodes: mut []Node
    top: u32
}

Build(p: mut Pool, depth: int): u32 {
    idx := p.top
    p.top += 1
    if depth > 0 {
        l := Build(p, depth - 1)
        r := Build(p, depth - 1)
        p.nodes[int(idx)] = Node(left: l, right: r)
    } else {
        p.nodes[int(idx)] = Node(left: 0, right: 0)
    }
    return idx
}

Check(nodes: []Node, idx: u32): int {
    n := nodes[int(idx)]
    if n.left == 0 {
        return 1
    }
    return 1 + Check(nodes, n.left) + Check(nodes, n.right)
}

export Main(sys: &System): OutOfMemory|ParseError|WriteError!void {
    n := Cast[int](try sys.args[1].ParseInt())
    min_depth := 4
    var max_depth := n
    if min_depth + 2 > max_depth {
        max_depth = min_depth + 2
    }
    nodes := try sys.arena.Slice[Node]((1 << (max_depth + 2)) + 1)
    var p := Pool(nodes: nodes, top: 1)
    stretch := Build(mut p, max_depth + 1)
    var total := Check(p.nodes[..], stretch)
    p.top = 1
    long := Build(mut p, max_depth)
    mark := p.top
    var d := min_depth
    for d <= max_depth {
        iterations := 1 << (max_depth - d + min_depth)
        for _ in 0..iterations {
            p.top = mark
            t := Build(mut p, d)
            total += Check(p.nodes[..], t)
        }
        d += 2
    }
    total += Check(p.nodes[..], long)
    var out := sys.Stdout()
    try Print(mut out, "{total}\n", (total))
}
