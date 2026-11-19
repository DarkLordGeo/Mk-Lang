// Why: The renderer asks one question of every shape: where does this ray first
// hit it.
//
// What: Ray intersection with the three primitive shapes: spheres, planes and
// triangles.
//
// How: Each test returns the nearest hit inside (tmin, tmax), or none.

import std.math

/// Where a ray met a surface: the distance, the unit normal facing the ray,
/// texture coordinates in [0, 1] and the surface's material.
export struct Hit {
    export t: f64
    export point: Vec
    export normal: Vec
    export u: f64
    export v: f64
    export material: int
}

/// A sphere, textured by longitude and latitude.
export struct Sphere {
    export center: Vec
    export radius: f64
    export material: int
}

/// An infinite plane through `point`, textured by its two in-plane axes.
export struct Plane {
    export point: Vec
    export normal: Vec
    export material: int
}

/// A triangle with a texture coordinate per corner.
export struct Tri {
    export a: Vec
    export b: Vec
    export c: Vec
    export ta: Vec
    export tb: Vec
    export tc: Vec
    export material: int
}

FaceForward(n: Vec, dir: Vec): Vec {
    return if n.Dot(dir) > 0.0 { n.Scale(-1.0) } else { n }
}

/// The nearest intersection of `r` with the sphere in (tmin, tmax). O(1).
export Sphere.Hit(&self, r: &Ray, tmin: f64, tmax: f64): ?Hit {
    oc := r.origin.Sub(self.center)
    b := oc.Dot(r.dir)
    c := oc.Dot(oc) - self.radius * self.radius
    disc := b * b - c
    if disc < 0.0 {
        return none
    }
    root := math.Sqrt(disc)
    var t := -b - root
    if t <= tmin || t >= tmax {
        t = -b + root
        if t <= tmin || t >= tmax {
            return none
        }
    }
    p := r.At(t)
    out := p.Sub(self.center).Scale(1.0 / self.radius)
    // Longitude from Atan2 and latitude from Acos, each mapped onto [0, 1].
    u := 0.5 + math.Atan2(out.z, out.x) / (2.0 * math.Pi)
    v := math.Acos(math.Min(1.0, math.Max(-1.0, out.y))) / math.Pi
    return some(Hit(t: t, point: p, normal: FaceForward(out, r.dir), u: u, v: v, material: self.material))
}

/// The intersection of `r` with the plane in (tmin, tmax). O(1).
export Plane.Hit(&self, r: &Ray, tmin: f64, tmax: f64): ?Hit {
    denom := self.normal.Dot(r.dir)
    if math.Abs(denom) < 1e-12 {
        return none
    }
    t := self.point.Sub(r.origin).Dot(self.normal) / denom
    if t <= tmin || t >= tmax {
        return none
    }
    p := r.At(t)
    basis := Basis.Around(self.normal)
    d := p.Sub(self.point)
    return some(Hit(t: t, point: p, normal: FaceForward(self.normal, r.dir), u: d.Dot(basis.u), v: d.Dot(basis.v), material: self.material))
}

/// The intersection of `r` with the triangle in (tmin, tmax), by the
/// Moller-Trumbore test (JGT 1997). O(1).
export Tri.Hit(&self, r: &Ray, tmin: f64, tmax: f64): ?Hit {
    e1 := self.b.Sub(self.a)
    e2 := self.c.Sub(self.a)
    p := r.dir.Cross(e2)
    det := e1.Dot(p)
    if math.Abs(det) < 1e-14 {
        return none
    }
    inv := 1.0 / det
    s := r.origin.Sub(self.a)
    bu := s.Dot(p) * inv
    if bu < 0.0 || bu > 1.0 {
        return none
    }
    q := s.Cross(e1)
    bv := r.dir.Dot(q) * inv
    if bv < 0.0 || bu + bv > 1.0 {
        return none
    }
    t := e2.Dot(q) * inv
    if t <= tmin || t >= tmax {
        return none
    }
    bw := 1.0 - bu - bv
    uv := self.ta.Scale(bw).Add(self.tb.Scale(bu)).Add(self.tc.Scale(bv))
    n := e1.Cross(e2).Unit()
    return some(Hit(t: t, point: r.At(t), normal: FaceForward(n, r.dir), u: uv.x, v: uv.y, material: self.material))
}

/// The triangle's axis-aligned bounding box. O(1).
export Tri.Bounds(&self): (lo: Vec, hi: Vec) {
    return (lo: self.a.Min(self.b).Min(self.c), hi: self.a.Max(self.b).Max(self.c))
}

test "a ray through the middle of a sphere hits its near side" {
    s := Sphere(center: V(0.0, 0.0, 5.0), radius: 1.0, material: 0)
    r := Ray(origin: V(0.0, 0.0, 0.0), dir: V(0.0, 0.0, 1.0))
    h := s.Hit(&r, 1e-9, 100.0).Must()
    Assert(h.t == 4.0)
    Assert(h.normal.z == -1.0)
}

test "a ray parallel to a plane misses it" {
    p := Plane(point: V(0.0, 0.0, 0.0), normal: V(0.0, 1.0, 0.0), material: 0)
    r := Ray(origin: V(0.0, 1.0, 0.0), dir: V(1.0, 0.0, 0.0))
    Assert(p.Hit(&r, 1e-9, 100.0).IsNone())
}

test "a triangle hit interpolates the texture coordinates" {
    tri := Tri(a: V(-1.0, -1.0, 2.0), b: V(1.0, -1.0, 2.0), c: V(-1.0, 1.0, 2.0), ta: V(0.0, 0.0, 0.0), tb: V(1.0, 0.0, 0.0), tc: V(0.0, 1.0, 0.0), material: 3)
    r := Ray(origin: V(0.0, 0.0, 0.0), dir: V(0.0, 0.0, 1.0))
    h := tri.Hit(&r, 1e-9, 100.0).Must()
    Assert(h.t == 2.0 && h.u == 0.5 && h.v == 0.5 && h.material == 3)
}
