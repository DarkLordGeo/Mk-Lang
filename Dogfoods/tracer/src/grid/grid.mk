// Why: Testing every ray against every triangle of a mesh is too slow for real
// models.
//
// What: A hashed uniform grid over the triangles of the scene's meshes.
//
// How: Cells are hashed into a power-of-two table instead of stored densely, so
// a fine grid over a sparse mesh costs memory in proportion to the triangles,
// not to the cells (Teschner et al., "Optimized Spatial Hashing for Collision
// Detection of Deformable Objects", VMV 2003). Rays walk the cells with the
// traversal of Amanatides and Woo ("A Fast Voxel Traversal Algorithm",
// Eurographics 1987).

import std.math
import src.geom

/// The requested resolution has more cells than the grid allows.
export error TooLarge(res: int)

/// What building a grid can fail with.
export type Error = TooLarge | OutOfMemory

// The most cells a grid may span, and the largest hash table it builds.
const MaxCells: i64 = 1 << 24
const MaxTableBits: u32 = 22

/// Triangle indices bucketed by the hash of each cell a triangle's bounds
/// overlap. Owns two heap lists.
export linear view struct Grid {
    lo: geom.Vec
    cell: geom.Vec
    res: int
    mask: u64
    shift: int
    starts: HeapList[i32]
    items: HeapList[i32]
}

/// The smallest power of two at least `n`, for `n` from 1 to 2^62. O(1).
export PowerOfTwoAtLeast(n: u64): u64 {
    if n <= 1 {
        return 1
    }
    return 1 << (64 - math.LeadingZeros64(n - 1))
}

/// Picks the resolution for `n` triangles: about twice the cube root of `n`
/// cells per axis, as a power of two from 1 to 64. O(1).
export AutoResolution(n: int): int {
    if n == 0 {
        return 1
    }
    want := math.Ceil(2.0 * math.Cbrt(Cast[f64](n)))
    return Cast[int](PowerOfTwoAtLeast(Cast[u64](math.Min(want, 64.0))))
}

Grid.Hash(&self, ix: int, iy: int, iz: int): u64 {
    h := Cast[u64](ix) *% 0x9E3779B185EBCA87 ^ math.RotateLeft64(Cast[u64](iy) *% 0xC2B2AE3D27D4EB4F, 21) ^ math.RotateLeft64(Cast[u64](iz) *% 0x165667B19E3779F9, 42)
    return (h ^ (h >> self.shift)) & self.mask
}

Grid.CellOf(&self, p: geom.Vec, axis: int): int {
    k := math.Floor((p.Axis(axis) - self.lo.Axis(axis)) / self.cell.Axis(axis))
    if !(k > 0.0) {
        return 0
    }
    return if k >= Cast[f64](self.res - 1) { self.res - 1 } else { Cast[int](k) }
}

/// Buckets `tris` into a grid of `res` cells per axis. Fails with `TooLarge`
/// when `res` cubed passes 2^24 or overflows, and with `OutOfMemory` when `a`
/// runs out. O(n + c), c the cells the triangles' bounds overlap; allocates
/// 4 bytes per table slot and per overlapped cell in `a`.
export Grid.Build(a: &Alloc, tris: []geom.Tri, res: int): Error!Grid from a {
    if res < 1 {
        fail TooLarge(res: res)
    }
    cells := math.CheckedPow(Cast[i64](res), 3) else { fail TooLarge(res: res) }
    if cells > MaxCells {
        fail TooLarge(res: res)
    }
    var lo := geom.Splat(math.Inf)
    var hi := geom.Splat(-math.Inf)
    for t in &tris {
        b := t.Bounds()
        lo = lo.Min(b.lo)
        hi = hi.Max(b.hi)
    }
    if tris.Len() == 0 {
        lo = geom.Splat(0.0)
        hi = geom.Splat(1.0)
    }
    // A flat mesh still gets cells of positive size on every axis.
    extent := hi.Sub(lo).Max(geom.Splat(1e-6))
    // About four slots per triangle, so most buckets hold one cell's list.
    want := PowerOfTwoAtLeast(Cast[u64](tris.Len()) * 4 + 16)
    bits := Cast[u32](math.TrailingZeros64(want))
    size := math.CheckedPowU64(2, if bits > MaxTableBits { MaxTableBits } else { bits }) else { fail TooLarge(res: res) }
    var g := Grid(lo: lo, cell: extent.Scale(1.0 / Cast[f64](res)), res: res, mask: size - 1, shift: math.TrailingZeros64(size), starts: HeapList[i32].New(a), items: HeapList[i32].New(a))
    onerr g.Free(a)
    slots := Cast[int](size)
    for _ in 0..slots + 1 {
        try g.starts.Push(a, 0)
    }
    // Count, prefix-sum, then place: a counting sort by bucket.
    for t in &tris {
        b := t.Bounds()
        for ix in g.CellOf(b.lo, 0)..g.CellOf(b.hi, 0) + 1 {
            for iy in g.CellOf(b.lo, 1)..g.CellOf(b.hi, 1) + 1 {
                for iz in g.CellOf(b.lo, 2)..g.CellOf(b.hi, 2) + 1 {
                    g.starts[g.Hash(ix, iy, iz) + 1] += 1
                }
            }
        }
    }
    for i in 0..slots {
        g.starts[i + 1] += g.starts[i]
    }
    var cursor := HeapList[i32].New(a)
    defer cursor.Free(a)
    for i in 0..slots {
        try cursor.Push(a, g.starts[i])
    }
    for _ in 0..g.starts[slots] {
        try g.items.Push(a, 0)
    }
    for k, t in &tris {
        b := t.Bounds()
        for ix in g.CellOf(b.lo, 0)..g.CellOf(b.hi, 0) + 1 {
            for iy in g.CellOf(b.lo, 1)..g.CellOf(b.hi, 1) + 1 {
                for iz in g.CellOf(b.lo, 2)..g.CellOf(b.hi, 2) + 1 {
                    h := g.Hash(ix, iy, iz)
                    g.items[cursor[h]] = Cast[i32](k)
                    cursor[h] += 1
                }
            }
        }
    }
    return g
}

/// The number of (cell, triangle) entries. O(1).
export Grid.Entries(&self): int {
    return self.items.Len()
}

/// The nearest triangle `r` hits in (tmin, tmax). O(cells walked + entries
/// tested); allocates nothing.
export Grid.Hit(&self, tris: []geom.Tri, r: &geom.Ray, tmin: f64, tmax: f64): ?geom.Hit {
    // A direction of no length never leaves its cell.
    if self.items.Len() == 0 || r.dir.Len() == 0.0 {
        return none
    }
    // Clip the ray to the grid's box, one slab per axis.
    var t0 := tmin
    var t1 := tmax
    for axis in 0..3 {
        o := r.origin.Axis(axis)
        d := r.dir.Axis(axis)
        lo := self.lo.Axis(axis)
        hi := lo + self.cell.Axis(axis) * Cast[f64](self.res)
        if d == 0.0 {
            if o < lo || o > hi {
                return none
            }
            continue
        }
        ta := (lo - o) / d
        tb := (hi - o) / d
        t0 = math.Max(t0, math.Min(ta, tb))
        t1 = math.Min(t1, math.Max(ta, tb))
        if t0 > t1 {
            return none
        }
    }
    entry := r.At(t0)
    var cell: [3]int = [0; 3]
    var step: [3]int = [0; 3]
    var next: [3]f64 = [math.Inf; 3]
    var delta: [3]f64 = [math.Inf; 3]
    for axis in 0..3 {
        cell[axis] = self.CellOf(entry, axis)
        d := r.dir.Axis(axis)
        size := self.cell.Axis(axis)
        edge := self.lo.Axis(axis) + Cast[f64](cell[axis]) * size
        if d > 0.0 {
            step[axis] = 1
            next[axis] = (edge + size - r.origin.Axis(axis)) / d
            delta[axis] = size / d
        } else if d < 0.0 {
            step[axis] = -1
            next[axis] = (edge - r.origin.Axis(axis)) / d
            delta[axis] = -size / d
        }
    }
    var best: ?geom.Hit = none
    var best_t := t1
    for {
        h := self.Hash(cell[0], cell[1], cell[2])
        for k in self.starts[h]..self.starts[h + 1] {
            if hit := tris[self.items[k]].Hit(r, tmin, best_t) {
                best_t = hit.t
                best = some(hit)
            }
        }
        axis := if next[0] <= next[1] && next[0] <= next[2] { 0 } else if next[1] <= next[2] { 1 } else { 2 }
        // A hit no farther than this cell's far side cannot be beaten by a
        // later cell; a hit beyond it may be, by a triangle met further on.
        if best.IsSome() && best_t <= next[axis] {
            return best
        }
        cell[axis] += step[axis]
        if cell[axis] < 0 || cell[axis] >= self.res || next[axis] > t1 {
            return best
        }
        next[axis] += delta[axis]
    }
}

impl Free for Grid {
    Free(self, a: &Alloc) {
        Grid(starts, items, ..) := self
        starts.Free(a)
        items.Free(a)
    }
}

test "PowerOfTwoAtLeast rounds up to a power of two" {
    Assert(PowerOfTwoAtLeast(1) == 1)
    Assert(PowerOfTwoAtLeast(5) == 8)
    Assert(PowerOfTwoAtLeast(64) == 64)
}

test "Build refuses a resolution whose cube overflows" (sys: &System) {
    a := sys.heap.Alloc()
    tris := try sys.arena.Slice[geom.Tri](0)
    g := Grid.Build(a, tris[..], 3_000_000) else {
        return
    }
    g.Free(a)
    Assert(false)
}
