// Why: Every other module of the tracer shares the same small geometry types.
//
// What: Vectors, rays and orthonormal bases. Colours are vectors too, one
// channel per axis.
//
// How: Plain structs (Vec, Ray) passed by value, with the operations written
// out per component.

import std.math

/// A point, a direction or a linear RGB colour.
export struct Vec {
    export x: f64
    export y: f64
    export z: f64
}

/// Makes a vector from its three components. O(1).
export V(x: f64, y: f64, z: f64): Vec {
    return Vec(x: x, y: y, z: z)
}

/// The vector with every component `k`. O(1).
export Splat(k: f64): Vec {
    return Vec(x: k, y: k, z: k)
}

/// Componentwise sum. O(1).
export Vec.Add(self, o: Vec): Vec {
    return Vec(x: self.x + o.x, y: self.y + o.y, z: self.z + o.z)
}

/// Componentwise difference. O(1).
export Vec.Sub(self, o: Vec): Vec {
    return Vec(x: self.x - o.x, y: self.y - o.y, z: self.z - o.z)
}

/// Scales by `k`. O(1).
export Vec.Scale(self, k: f64): Vec {
    return Vec(x: self.x * k, y: self.y * k, z: self.z * k)
}

/// Componentwise product, as a filter applies to a colour. O(1).
export Vec.Mul(self, o: Vec): Vec {
    return Vec(x: self.x * o.x, y: self.y * o.y, z: self.z * o.z)
}

/// The dot product. O(1).
export Vec.Dot(self, o: Vec): f64 {
    return self.x * o.x + self.y * o.y + self.z * o.z
}

/// The cross product. O(1).
export Vec.Cross(self, o: Vec): Vec {
    return Vec(x: self.y * o.z - self.z * o.y, y: self.z * o.x - self.x * o.z, z: self.x * o.y - self.y * o.x)
}

/// The Euclidean length, without overflow for large components. O(1).
export Vec.Len(self): f64 {
    return math.Hypot(math.Hypot(self.x, self.y), self.z)
}

/// The vector scaled to length 1; the zero vector stays zero. O(1).
export Vec.Unit(self): Vec {
    n := self.Len()
    if n == 0.0 {
        return self
    }
    return self.Scale(1.0 / n)
}

/// The component of the largest magnitude. O(1).
export Vec.MaxPart(self): f64 {
    return math.Max(math.Max(self.x, self.y), self.z)
}

/// The mirror direction of `self` about the unit normal `n`. O(1).
export Vec.Reflect(self, n: Vec): Vec {
    return self.Sub(n.Scale(2.0 * self.Dot(n)))
}

/// Component `axis` (0, 1 or 2). O(1).
export Vec.Axis(self, axis: int): f64 {
    return if axis == 0 { self.x } else if axis == 1 { self.y } else { self.z }
}

/// The componentwise minimum. O(1).
export Vec.Min(self, o: Vec): Vec {
    return Vec(x: math.Min(self.x, o.x), y: math.Min(self.y, o.y), z: math.Min(self.z, o.z))
}

/// The componentwise maximum. O(1).
export Vec.Max(self, o: Vec): Vec {
    return Vec(x: math.Max(self.x, o.x), y: math.Max(self.y, o.y), z: math.Max(self.z, o.z))
}

/// A half-line from `origin` along the unit vector `dir`.
export struct Ray {
    export origin: Vec
    export dir: Vec
}

/// The point at distance `t` along the ray. O(1).
export Ray.At(&self, t: f64): Vec {
    return self.origin.Add(self.dir.Scale(t))
}

/// Two unit vectors that with `n` form a right-handed orthonormal basis.
export struct Basis {
    export u: Vec
    export v: Vec
    export n: Vec
}

/// Builds the basis around the unit normal `n` without a branch on its
/// direction (Duff et al., "Building an Orthonormal Basis, Revisited", JCGT
/// 2017). O(1).
export Basis.Around(n: Vec): Basis {
    sign := math.CopySign(1.0, n.z)
    a := -1.0 / (sign + n.z)
    b := n.x * n.y * a
    u := Vec(x: 1.0 + sign * n.x * n.x * a, y: sign * b, z: -sign * n.x)
    v := Vec(x: b, y: sign + n.y * n.y * a, z: -n.y)
    return Basis(u: u, v: v, n: n)
}

/// Turns local coordinates into world coordinates. O(1).
export Basis.World(&self, l: Vec): Vec {
    return self.u.Scale(l.x).Add(self.v.Scale(l.y)).Add(self.n.Scale(l.z))
}

test "Basis.Around gives unit vectors at right angles" {
    for n in [V(0.0, 0.0, 1.0), V(0.0, 0.0, -1.0), V(0.6, 0.0, 0.8), V(0.0, 1.0, 0.0)] {
        b := Basis.Around(n)
        Assert(math.Abs(b.u.Dot(b.v)) < 1e-12)
        Assert(math.Abs(b.u.Dot(n)) < 1e-12)
        Assert(math.Abs(b.u.Len() - 1.0) < 1e-12)
    }
}

test "Reflect mirrors a direction about a normal" {
    r := V(1.0, -1.0, 0.0).Reflect(V(0.0, 1.0, 0.0))
    Assert(r.x == 1.0 && r.y == 1.0 && r.z == 0.0)
}
