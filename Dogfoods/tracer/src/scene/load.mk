// Why: Scenes are written by hand in a small text format.
//
// What: The scene file reader: one directive a line, fields separated by
// spaces, `#` to the end of a line a comment.
//
// How: Each directive is parsed into the Scene's lists; an error names the
// line. README.md documents every directive.

import std.fs
import std.math
import src.geom
import src.ppm

/// The directive at this line is not one the tracer knows.
export error UnknownDirective(line: int)
/// Field `field` (1 for the directive) of the line is not a valid value.
export error BadField(line: int, field: int)
/// The line has too few or too many fields for its directive.
export error FieldCount(line: int)
/// The line names a material no earlier line defined, or defines one twice.
export error BadMaterial(line: int)
/// An OBJ face at this line has fewer than three corners or a bad index.
export error BadFace(line: int)

/// What loading a scene can fail with. `Loader.file` names the file it was
/// reading.
export type Error = UnknownDirective | BadField | FieldCount | BadMaterial | BadFace | ppm.Error | fs.Error | OutOfMemory

// The most fields a line may have.
const MaxFields: int = 16

/// Reads a scene file and the meshes and textures it names.
export struct Loader {
    /// The file being read when loading stopped.
    export file: str
    /// Its line, 0 when the error is not about a line.
    export line: int
}

struct Line {
    words: [16]str
    n: int
    line: int
}

Split(line: str, n: int): ?Line {
    var f := Line(words: [""; 16], n: 0, line: n)
    for w in line.Words() {
        if w.StartsWith("#") {
            break
        }
        if f.n == MaxFields {
            return none
        }
        f.words[f.n] = w
        f.n += 1
    }
    return some(f)
}

Line.Num(&self, i: int): BadField!f64 {
    x := self.words[i].ParseFloat() else { fail BadField(line: self.line, field: i + 1) }
    if !math.IsFinite(x) {
        fail BadField(line: self.line, field: i + 1)
    }
    return x
}

Line.Int(&self, i: int, lo: i64, hi: i64): BadField!int {
    x := self.words[i].ParseInt() else { fail BadField(line: self.line, field: i + 1) }
    if x < lo || x > hi {
        fail BadField(line: self.line, field: i + 1)
    }
    return Cast[int](x)
}

Line.Vec(&self, i: int): BadField!geom.Vec {
    return geom.V(try self.Num(i), try self.Num(i + 1), try self.Num(i + 2))
}

Line.Color(&self, i: int): BadField!geom.Vec {
    c := try self.Vec(i)
    if c.x < 0.0 || c.y < 0.0 || c.z < 0.0 {
        fail BadField(line: self.line, field: i + 1)
    }
    return c
}

// Checks that the line has exactly `lo` or exactly `hi` fields.
Line.Want(&self, lo: int, hi: int): FieldCount!void {
    if self.n < lo || self.n > hi || (self.n > lo && self.n < hi) {
        fail FieldCount(line: self.line)
    }
}

// The directory part of `path`, with its separator, or "" for none.
Dir(path: str): str {
    var end := 0
    for i, b in path.AsBytes() {
        if b == b'/' || b == b'\\' {
            end = i + 1
        }
    }
    return path[..end]
}

/// Loads the scene at `path` into `s`, reading named files relative to its
/// directory. Fails with the error of the first bad line, file or allocation,
/// leaving `self.file` and `self.line` at it. O(n) in the files read; file
/// contents live in `arena`, the scene's data in `a`.
export Loader.Load(mut self, a: &Alloc into s, arena: &Arena into self, io: Io, path: str, s: mut Scene): Error!void {
    // A copy, so the caller's path need not flow into the loader: a loader
    // that holds a loan of the arena would carry it into the caller's owner.
    scene_file := try arena.Dup(path)
    self.file = scene_file
    self.line = 0
    dir := Dir(scene_file)
    text := try fs.ReadText(arena, io, path)
    var names := Map[str, int].New(arena)
    var n := 0
    for raw in text.Lines() {
        n += 1
        self.line = n
        f := Split(raw, n) else { fail FieldCount(line: n) }
        if f.n == 0 {
            continue
        }
        match f.words[0] {
            "size" {
                try f.Want(3, 3)
                s.width = try f.Int(1, 1, 4096)
                s.height = try f.Int(2, 1, 4096)
            }
            "samples" {
                try f.Want(2, 2)
                s.samples = try f.Int(1, 1, 4096)
            }
            "depth" {
                try f.Want(2, 2)
                s.depth = try f.Int(1, 1, 64)
            }
            "seed" {
                try f.Want(2, 2)
                s.seed = Cast[u64](try f.Int(1, 0, 1 << 62))
            }
            "grid" {
                try f.Want(2, 2)
                s.grid_res = try f.Int(1, 0, 1 << 40)
            }
            "background" {
                try f.Want(4, 4)
                s.background = try f.Color(1)
            }
            "camera" {
                try f.Want(8, 10)
                s.camera.eye = try f.Vec(1)
                s.camera.target = try f.Vec(4)
                // The view direction must have a length and must not be
                // parallel to +y, the camera's up.
                look := s.camera.target.Sub(s.camera.eye)
                if look.Len() == 0.0 || look.Cross(s.camera.up).Len() == 0.0 {
                    fail BadField(line: n, field: 5)
                }
                s.camera.fov = try f.Num(7)
                if !(s.camera.fov > 0.0 && s.camera.fov < 180.0) {
                    fail BadField(line: n, field: 8)
                }
                if f.n == 10 {
                    s.camera.aperture = math.Abs(try f.Num(8))
                    s.camera.focus = try f.Num(9)
                    if !(s.camera.focus > 0.0) {
                        fail BadField(line: n, field: 10)
                    }
                }
            }
            "light" {
                try f.Want(8, 8)
                try s.lights.Push(a, Light(center: try f.Vec(1), radius: math.Abs(try f.Num(4)), color: try f.Color(5)))
            }
            "material" {
                try self.Material(a, arena, io, dir, &f, mut names, s)
                self.file = scene_file
            }
            "sphere" {
                try f.Want(6, 6)
                radius := try f.Num(4)
                if !(radius > 0.0) {
                    fail BadField(line: n, field: 5)
                }
                try s.spheres.Push(a, geom.Sphere(center: try f.Vec(1), radius: radius, material: try Lookup(&names, &f, 5)))
            }
            "plane" {
                try f.Want(8, 8)
                normal := (try f.Vec(4)).Unit()
                if normal.Len() == 0.0 {
                    fail BadField(line: n, field: 5)
                }
                try s.planes.Push(a, geom.Plane(point: try f.Vec(1), normal: normal, material: try Lookup(&names, &f, 7)))
            }
            "mesh" {
                try f.Want(8, 8)
                m := Placement(scale: try f.Num(3), turn: try f.Num(4), offset: try f.Vec(5), material: try Lookup(&names, &f, 2))
                mesh := try arena.Fmt("{dir}{name}", (dir, name: f.words[1]))
                self.file = mesh
                self.line = 0
                try self.Mesh(a, arena, io, mesh, &m, s)
                self.file = scene_file
            }
            _ {
                fail UnknownDirective(line: n)
            }
        }
    }
    self.line = 0
}

Lookup(names: &Map[str, int], f: &Line, i: int): BadMaterial!int {
    k := names.Get(&f.words[i]) else { fail BadMaterial(line: f.line) }
    return *k
}

Loader.Material(mut self, a: &Alloc into s, arena: &Arena into self, io: Io, dir: str, f: &Line, names: mut Map[str, int], s: mut Scene): Error!void *f into names {
    if f.n < 3 {
        fail FieldCount(line: f.line)
    }
    if names.Has(&f.words[1]) {
        fail BadMaterial(line: f.line)
    }
    m := match f.words[2] {
        "diffuse" {
            try f.Want(6, 6)
            Material.Diffuse(color: try f.Color(3))
        }
        "checker" {
            try f.Want(10, 10)
            Material.Checker(a: try f.Color(3), b: try f.Color(6), scale: try f.Num(9))
        }
        "mirror" {
            try f.Want(7, 7)
            Material.Mirror(color: try f.Color(3), fuzz: math.Min(1.0, math.Abs(try f.Num(6))))
        }
        "texture" {
            try f.Want(4, 4)
            file := try arena.Fmt("{dir}{name}", (dir, name: f.words[3]))
            // A texture's errors are about the texture file, not this line.
            self.file = file
            self.line = 0
            data := try fs.ReadBytes(arena, io, file)
            offset := s.texels.Len()
            size := try ppm.Decode(a, data, mut s.texels)
            try s.textures.Push(a, Texture(width: size.width, height: size.height, offset: offset))
            Material.Image(texture: s.textures.Len() - 1)
        }
        _ {
            fail BadField(line: f.line, field: 3)
        }
    }
    try s.materials.Push(a, m)
    try names.Set(f.words[1], s.materials.Len() - 1)
}

/// How a mesh is placed: scaled, turned about +y by `turn` degrees, then
/// moved by `offset`.
struct Placement {
    scale: f64
    turn: f64
    offset: geom.Vec
    material: int
}

Placement.Apply(&self, p: geom.Vec): geom.Vec {
    rad := self.turn * math.Pi / 180.0
    c := math.Cos(rad)
    s := math.Sin(rad)
    q := p.Scale(self.scale)
    return geom.V(c * q.x + s * q.z, q.y, c * q.z - s * q.x).Add(self.offset)
}
