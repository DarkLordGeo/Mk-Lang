// Why: the B-tree's nodes are raw memory, but its algorithm should hold no
// unsafe block; this mem_ file is the raw half of btree.mk.
//
// What: a node's byte layout (BtKeyOff, BtValOff, BtEdgeOff and the node
// sizes), header accessors (BtLen, BtIsLeaf, BtParent, BtPidx and setters),
// node allocation (BtAllocNode, BtFreeNode) and the free lists (TreeGive,
// TreeTake).
//
// How: a node is one allocation: a header, then the keys, the values and,
// in inner nodes, the edges, each region aligned for its type. Every layout
// value is a function of the types alone, which release builds fold to a
// constant. Every node
// a Tree reaches is live and initialized; the callers keep that so.

import std.core
import std.raw

// The byte layout of a node. Each is a function of the types alone, which
// release builds fold to a constant.
BtAlignUp(x: int, a: int): int {
    return (x + a - 1) & (0 - a)
}

BtAlign[K, V](): int {
    var a := AlignOf[BtNode[K, V]]()
    if AlignOf[K]() > a {
        a = AlignOf[K]()
    }
    if AlignOf[V]() > a {
        a = AlignOf[V]()
    }
    return a
}

BtKeyOff[K, V](): int {
    return BtAlignUp(SizeOf[BtNode[K, V]](), AlignOf[K]())
}

BtValOff[K, V](): int {
    return BtAlignUp(BtKeyOff[K, V]() + BtCap[K, V]() * SizeOf[K](), AlignOf[V]())
}

BtLeafBytes[K, V](): int {
    return BtAlignUp(BtValOff[K, V]() + BtCap[K, V]() * SizeOf[V](), BtAlign[K, V]())
}

BtEdgeOff[K, V](): int {
    return BtAlignUp(BtValOff[K, V]() + BtCap[K, V]() * SizeOf[V](), AlignOf[*BtNode[K, V]]())
}

BtInnerBytes[K, V](): int {
    return BtAlignUp(BtEdgeOff[K, V]() + (BtCap[K, V]() + 1) * SizeOf[*BtNode[K, V]](), BtAlign[K, V]())
}

BtRegion[K, V](n: *BtNode[K, V], off: int): *u8 {
    unsafe "off is one of the region offsets of a node allocated with that layout" {
        return raw.Offset(raw.PtrCast[BtNode[K, V], u8](n), off)
    }
}

BtKeys[K, V](n: *BtNode[K, V]): *K {
    unsafe "the key region of a node, aligned for K by BtKeyOff" {
        return raw.PtrCast[u8, K](BtRegion(n, BtKeyOff[K, V]()))
    }
}

BtVals[K, V](n: *BtNode[K, V]): *V {
    unsafe "the value region of a node, aligned for V by BtValOff" {
        return raw.PtrCast[u8, V](BtRegion(n, BtValOff[K, V]()))
    }
}

BtEdges[K, V](n: *BtNode[K, V]): **BtNode[K, V] {
    unsafe "the edge region of an internal node, aligned by BtEdgeOff" {
        return raw.PtrCast[u8, *BtNode[K, V]](BtRegion(n, BtEdgeOff[K, V]()))
    }
}

// The header fields, read and written through the node pointer; every
// node a Tree reaches is live and initialized.
BtLen[K, V](n: *BtNode[K, V]): int {
    unsafe "n is a live node of a tree" {
        return (*n).len
    }
}

BtSetLen[K, V](n: *BtNode[K, V], len: int) {
    unsafe "n is a live node of a tree, which the caller borrows mutably" {
        (*n).len = len
    }
}

BtIsLeaf[K, V](n: *BtNode[K, V]): bool {
    unsafe "n is a live node of a tree" {
        return (*n).leaf
    }
}

BtParent[K, V](n: *BtNode[K, V]): *BtNode[K, V] {
    unsafe "n is a live node of a tree" {
        return (*n).parent
    }
}

BtPidx[K, V](n: *BtNode[K, V]): int {
    unsafe "n is a live node of a tree" {
        return (*n).pidx
    }
}

// BtSetParent records p and edge index j in c's header.
BtSetParent[K, V](c: *BtNode[K, V], p: *BtNode[K, V], j: int) {
    unsafe "c is a live node of the tree the caller borrows mutably" {
        (*c).parent = p
        (*c).pidx = j
    }
}

// BtAllocNode is a fresh node from a, null when a is out of memory.
BtAllocNode[K, V](a: &Alloc, leaf: bool): *BtNode[K, V] {
    size := if leaf { BtLeafBytes[K, V]() } else { BtInnerBytes[K, V]() }
    p := core.AllocBytes(a, size, BtAlign[K, V]())
    if raw.IsNull(p) {
        return raw.Null[BtNode[K, V]]()
    }
    unsafe "a pointer cast; a allocated p for a node of this layout" {
        n := raw.PtrCast[u8, BtNode[K, V]](p)
        raw.Write(n, BtNode[K, V](parent: raw.Null[BtNode[K, V]](), pidx: 0, len: 0, leaf: leaf))
        return n
    }
}

BtFreeNode[K, V](a: &Alloc, n: *BtNode[K, V]) {
    size := if BtIsLeaf(n) { BtLeafBytes[K, V]() } else { BtInnerBytes[K, V]() }
    var p := raw.Null[u8]()
    unsafe "a pointer cast; nothing is dereferenced" {
        p = raw.PtrCast[BtNode[K, V], u8](n)
    }
    core.FreeBytes(a, p, size, BtAlign[K, V]())
}

// TreeGive puts an emptied node on its free list.
TreeGive[K, V](t: mut Tree[K, V], n: *BtNode[K, V]) {
    unsafe "n left the tree; only its header is written" {
        if (*n).leaf {
            (*n).parent = t.free
            t.free = n
            t.nfree += 1
        } else {
            (*n).parent = t.ifree
            t.ifree = n
            t.nifree += 1
        }
    }
}

// TreeTake is a spare node with an empty header; TreeReserve made sure
// the list holds one.
TreeTake[K, V](t: mut Tree[K, V], leaf: bool): *BtNode[K, V] {
    n := if leaf { t.free } else { t.ifree }
    unsafe "n is a spare node of the tree" {
        if leaf {
            t.free = (*n).parent
            t.nfree -= 1
        } else {
            t.ifree = (*n).parent
            t.nifree -= 1
        }
        (*n).parent = raw.Null[BtNode[K, V]]()
        (*n).pidx = 0
        (*n).len = 0
    }
    return n
}
