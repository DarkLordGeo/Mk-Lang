// Why: The path tracer itself.
//
// What: Frame, Setup, Job with Trace, Pixel and Render, and the band states
// BandDone and BandCanceled.
//
// How: Camera rays through a thin lens, direct light from spherical area lights
// by one shadow ray each (soft shadows), one diffuse or mirror bounce per
// level, and the image split into bands rendered by tasks. Every random number
// comes from `sampler.Sampler.ForPixel`, so the image is the same on any number
// of carriers.

import std.math
import std.time
import src.geom
import src.grid
import src.sampler
import src.scene

// Offsets a secondary ray's start so it does not hit its own surface.
const Epsilon: f64 = 1e-7

/// The camera's precomputed frame: where pixel (0, 0) is, the steps across a
/// row and down a column on the focus plane, and the lens.
export struct Frame {
    eye: geom.Vec
    corner: geom.Vec
    across: geom.Vec
    down: geom.Vec
    lens_u: geom.Vec
    lens_v: geom.Vec
    lens_radius: f64
}

/// Builds the frame for the scene's camera and image size. O(1).
export Setup(s: &scene.Scene): Frame {
    c := s.camera
    aspect := Cast[f64](s.width) / Cast[f64](s.height)
    half_h := math.Tan(c.fov * math.Pi / 360.0)
    half_w := aspect * half_h
    w := c.eye.Sub(c.target).Unit()
    u := c.up.Cross(w).Unit()
    v := w.Cross(u)
    focus := if c.aperture > 0.0 { c.focus } else { 1.0 }
    across := u.Scale(2.0 * half_w * focus)
    down := v.Scale(-2.0 * half_h * focus)
    corner := c.eye.Sub(w.Scale(focus)).Sub(across.Scale(0.5)).Sub(down.Scale(0.5))
    return Frame(eye: c.eye, corner: corner, across: across.Scale(1.0 / Cast[f64](s.width)), down: down.Scale(1.0 / Cast[f64](s.height)), lens_u: u, lens_v: v, lens_radius: c.aperture / 2.0)
}

/// What the tasks share: the scene, its triangle grid and the frame.
export struct Job {
    export scene: &scene.Scene
    export grid: &grid.Grid
    export frame: Frame
}

Job.Nearest(&self, r: &geom.Ray, tmax: f64): ?geom.Hit {
    var best: ?geom.Hit = none
    var far := tmax
    for sp in &self.scene.spheres {
        if h := sp.Hit(r, Epsilon, far) {
            far = h.t
            best = some(h)
        }
    }
    for pl in &self.scene.planes {
        if h := pl.Hit(r, Epsilon, far) {
            far = h.t
            best = some(h)
        }
    }
    if h := self.grid.Hit(self.scene.tris.Items(), r, Epsilon, far) {
        best = some(h)
    }
    return best
}

// The colour of a diffuse surface at the hit: its own, a checker's, or an
// image's texel.
Job.Albedo(&self, m: scene.Material, h: &geom.Hit): geom.Vec {
    return match m {
        .Diffuse(color) { color }
        .Checker(a, b, scale) {
            // The parity of the cell: Mod keeps the sign of its first
            // argument, so cells left of zero give -1 as well as 1.
            k := math.Floor(h.u * scale) + math.Floor(h.v * scale)
            if math.Abs(math.Mod(k, 2.0)) == 1.0 { b } else { a }
        }
        .Image(texture) { self.scene.Texel(texture, h.u, h.v) }
        .Mirror(color) { color }
    }
}

// Light reaching the hit straight from each light, through one shadow ray to
// a random point of the light's sphere.
Job.Direct(&self, h: &geom.Hit, smp: mut sampler.Sampler): geom.Vec {
    var sum := geom.Splat(0.0)
    for l in &self.scene.lights {
        d := smp.Sphere()
        target := l.center.Add(geom.V(d.x, d.y, d.z).Scale(l.radius))
        to := target.Sub(h.point)
        dist := to.Len()
        if dist == 0.0 {
            continue
        }
        dir := to.Scale(1.0 / dist)
        cos := h.normal.Dot(dir)
        if cos <= 0.0 {
            continue
        }
        shadow := geom.Ray(origin: h.point.Add(h.normal.Scale(Epsilon)), dir: dir)
        if self.Nearest(&shadow, dist - Epsilon).IsSome() {
            continue
        }
        sum = sum.Add(l.color.Scale(cos / (dist * dist)))
    }
    return sum
}

/// The light arriving along `r`, following at most `depth` bounces. O(depth
/// times the cost of a nearest-hit query and one shadow ray per light).
export Job.Trace(&self, r: geom.Ray, depth: int, smp: mut sampler.Sampler): geom.Vec {
    h := self.Nearest(&r, math.Inf) else { return self.scene.background }
    m := self.scene.materials[h.material]
    if depth <= 0 {
        return geom.Splat(0.0)
    }
    match m {
        .Mirror(color, fuzz) {
            d := smp.Sphere()
            dir := r.dir.Reflect(h.normal).Add(geom.V(d.x, d.y, d.z).Scale(fuzz)).Unit()
            if dir.Dot(h.normal) <= 0.0 {
                return geom.Splat(0.0)
            }
            next := geom.Ray(origin: h.point.Add(h.normal.Scale(Epsilon)), dir: dir)
            return color.Mul(self.Trace(next, depth - 1, smp))
        }
        _ {
        }
    }
    albedo := self.Albedo(m, &h)
    direct := self.Direct(&h, smp).Scale(1.0 / math.Pi)
    // One cosine-weighted bounce: a disk sample lifted onto the hemisphere
    // (Malley's method), whose density cancels the cosine and 1/pi.
    p := smp.Disk()
    z := math.Sqrt(math.Max(0.0, 1.0 - p.x * p.x - p.y * p.y))
    dir := geom.Basis.Around(h.normal).World(geom.V(p.x, p.y, z))
    bounce := geom.Ray(origin: h.point.Add(h.normal.Scale(Epsilon)), dir: dir)
    indirect := self.Trace(bounce, depth - 1, smp)
    return albedo.Mul(direct.Add(indirect))
}

/// The averaged colour of pixel (x, y). O(samples times `Trace`).
export Job.Pixel(&self, x: int, y: int): geom.Vec {
    s := self.scene
    f := &self.frame
    var sum := geom.Splat(0.0)
    for k in 0..s.samples {
        var smp := sampler.Sampler.ForPixel(s.seed, y * s.width + x, k)
        px := Cast[f64](x) + smp.Stratum()
        py := Cast[f64](y) + smp.Next()
        target := f.corner.Add(f.across.Scale(px)).Add(f.down.Scale(py))
        var origin := f.eye
        if f.lens_radius > 0.0 {
            d := smp.Disk()
            origin = origin.Add(f.lens_u.Scale(d.x * f.lens_radius)).Add(f.lens_v.Scale(d.y * f.lens_radius))
        }
        r := geom.Ray(origin: origin, dir: target.Sub(origin).Unit())
        sum = sum.Add(self.Trace(r, s.depth, mut smp))
    }
    return sum.Scale(1.0 / Cast[f64](s.samples))
}

/// A band's state after a render when every row was drawn; 0 is a band
/// that never started.
export const BandDone: u8 = 1
/// A band's state after a render when the deadline stopped it.
export const BandCanceled: u8 = 2

// Renders the rows from `y0` into `rows`, three values a pixel. Checks for
// cancellation before each row.
RenderBand(job: &Job, io: Io, y0: int, rows: mut []f64, state: mut []u8) {
    w := job.scene.width
    for i in 0..rows.Len() / (w * 3) {
        io.Yield() else {
            state[0] = BandCanceled
            return
        }
        for x in 0..w {
            c := job.Pixel(x, y0 + i)
            at := (i * w + x) * 3
            rows[at] = c.x
            rows[at + 1] = c.y
            rows[at + 2] = c.z
        }
    }
    state[0] = BandDone
}

/// Renders the whole image into `pixels` (width times height times 3 values)
/// with one task per band of `band` rows, cancelled after `limit` when one is
/// given. Returns false when the deadline stopped a band. O(pixels times
/// `Pixel`); allocates one byte per band in `a`.
export Job.Render(&self, a: &Arena, io: Io, pixels: mut []f64, band: int, limit: ?time.Duration): OutOfMemory!bool {
    w := self.scene.width
    bands := (self.scene.height + band - 1) / band
    var states := try a.Slice[u8](bands)
    var flags := states.ChunksMut(1)
    var y0 := 0
    scope s := io.Scope() {
        if d := limit {
            s.CancelAfter(d)
        }
        for rows in pixels.ChunksMut(w * 3 * band) {
            state := flags.Next() else { break }
            s.go RenderBand(self, io, y0, rows, state)
            y0 += band
        }
    }
    for st in states {
        if st != BandDone {
            return false
        }
    }
    return true
}
