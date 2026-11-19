// Why: Meshes come from other tools as Wavefront OBJ files.
//
// What: The OBJ reader: `v` positions, `vt` texture coordinates and `f` faces,
// the subset a renderer of triangles needs (paulbourke.net/dataformats/obj).
//
// How: Faces with more than three corners are split into a fan; indices may be
// negative, counting back from the last vertex read. Other statements, such as
// `vn`, `g`, `o`, `s`, `usemtl` and `mtllib`, are skipped.

import std.fs
import src.geom

// One corner of a face: a position index and a texture index, or -1.
struct Corner {
    v: int
    t: int
}

// Resolves an OBJ index (1-based, or negative from the end) against `count`
// items, or -1 when it names none.
Resolve(text: str, count: int): int {
    i := text.ParseInt() else { return -1 }
    k := if i < 0 { Cast[i64](count) + i } else { i - 1 }
    return if k >= 0 && k < Cast[i64](count) { Cast[int](k) } else { -1 }
}

ParseCorner(word: str, verts: int, uvs: int): ?Corner {
    var parts := word.Split("/")
    v := Resolve(parts.Next() else { "" }, verts)
    if v < 0 {
        return none
    }
    t := parts.Next() else { "" }
    if t.Len() == 0 {
        return some(Corner(v: v, t: -1))
    }
    k := Resolve(t, uvs)
    if k < 0 {
        return none
    }
    return some(Corner(v: v, t: k))
}

Loader.Mesh(mut self, a: &Alloc into s, arena: &Arena, io: Io, path: str, m: &Placement, s: mut Scene): Error!void {
    text := try fs.ReadText(arena, io, path)
    var verts := List[geom.Vec].New(arena)
    var uvs := List[geom.Vec].New(arena)
    var corners := List[Corner].New(arena)
    var n := 0
    for raw in text.Lines() {
        n += 1
        self.line = n
        f := Split(raw, n) else { fail BadFace(line: n) }
        if f.n == 0 {
            continue
        }
        match f.words[0] {
            "v" {
                if f.n < 4 {
                    fail FieldCount(line: n)
                }
                try verts.Push(m.Apply(try f.Vec(1)))
            }
            "vt" {
                if f.n < 3 {
                    fail FieldCount(line: n)
                }
                try uvs.Push(geom.V(try f.Num(1), try f.Num(2), 0.0))
            }
            "f" {
                if f.n < 4 {
                    fail BadFace(line: n)
                }
                corners.Clear()
                for i in 1..f.n {
                    c := ParseCorner(f.words[i], verts.Len(), uvs.Len()) else { fail BadFace(line: n) }
                    try corners.Push(c)
                }
                for i in 1..corners.Len() - 1 {
                    try s.tris.Push(a, Triangle(&verts, &uvs, corners[0], corners[i], corners[i + 1], m.material))
                }
            }
            _ {
            }
        }
    }
    self.line = 0
}

// A corner's texture coordinate, or a default spread over the triangle's
// corners when the face gives none.
Uv(uvs: &List[geom.Vec], c: Corner, fallback: geom.Vec): geom.Vec {
    return if c.t < 0 { fallback } else { uvs[c.t] }
}

Triangle(verts: &List[geom.Vec], uvs: &List[geom.Vec], p: Corner, q: Corner, r: Corner, material: int): geom.Tri {
    return geom.Tri(a: verts[p.v], b: verts[q.v], c: verts[r.v], ta: Uv(uvs, p, geom.V(0.0, 0.0, 0.0)), tb: Uv(uvs, q, geom.V(1.0, 0.0, 0.0)), tc: Uv(uvs, r, geom.V(0.0, 1.0, 0.0)), material: material)
}

test "Resolve counts negative indices back from the end" {
    Assert(Resolve("1", 3) == 0)
    Assert(Resolve("-1", 3) == 2)
    Assert(Resolve("4", 3) == -1)
    Assert(Resolve("x", 3) == -1)
}

test "ParseCorner reads v, v/t and v//n" {
    Assert(ParseCorner("2", 3, 0).Must().v == 1)
    Assert(ParseCorner("2/1", 3, 1).Must().t == 0)
    Assert(ParseCorner("2//5", 3, 0).Must().t == -1)
    Assert(ParseCorner("2/9", 3, 1).IsNone())
}
