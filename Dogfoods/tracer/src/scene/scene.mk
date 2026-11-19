// Why: Render tasks all read one scene.
//
// What: The scene: camera, materials, textures, lights and shapes, as the scene
// file describes them.
//
// How: Held in heap lists, so render tasks share it by `&`.

import std.math
import src.geom

/// How a surface scatters light.
export enum Material {
    /// Lambertian reflection of the colour
    Diffuse(color: geom.Vec)
    /// Two colours alternating in cubes of the given size in texture space
    Checker(a: geom.Vec, b: geom.Vec, scale: f64)
    /// Mirror reflection tinted by the colour, blurred by `fuzz` from 0 to 1
    Mirror(color: geom.Vec, fuzz: f64)
    /// Lambertian reflection of an image texture's texel
    Image(texture: int)
}

/// Where one image's texels sit in `Scene.texels`.
export struct Texture {
    export width: int
    export height: int
    export offset: int
}

/// A spherical area light: shadows soften with its radius.
export struct Light {
    export center: geom.Vec
    export radius: f64
    export color: geom.Vec
}

/// A thin-lens camera. `aperture` is the lens diameter (0 for a pinhole) and
/// `focus` the distance of the plane in focus.
export struct Camera {
    export eye: geom.Vec
    export target: geom.Vec
    export up: geom.Vec
    export fov: f64
    export aperture: f64
    export focus: f64
}

/// Everything a render reads. Owns its heap lists; release with `Free`.
export linear view struct Scene {
    export width: int
    export height: int
    export samples: int
    export depth: int
    export seed: u64
    export grid_res: int
    export background: geom.Vec
    export camera: Camera
    export materials: HeapList[Material]
    export textures: HeapList[Texture]
    export texels: HeapList[f32]
    export lights: HeapList[Light]
    export spheres: HeapList[geom.Sphere]
    export planes: HeapList[geom.Plane]
    export tris: HeapList[geom.Tri]
}

/// An empty scene with the defaults a scene file overrides: 64 by 48 pixels,
/// 4 samples, 4 bounces, a pinhole camera at the origin looking down +z.
/// O(1); allocates nothing until something is added.
export Scene.New(a: &Alloc): Scene from a {
    camera := Camera(eye: geom.V(0.0, 0.0, 0.0), target: geom.V(0.0, 0.0, 1.0), up: geom.V(0.0, 1.0, 0.0), fov: 40.0, aperture: 0.0, focus: 1.0)
    return Scene(width: 64, height: 48, samples: 4, depth: 4, seed: 1, grid_res: 0, background: geom.V(0.0, 0.0, 0.0), camera: camera, materials: HeapList[Material].New(a), textures: HeapList[Texture].New(a), texels: HeapList[f32].New(a), lights: HeapList[Light].New(a), spheres: HeapList[geom.Sphere].New(a), planes: HeapList[geom.Plane].New(a), tris: HeapList[geom.Tri].New(a))
}

/// The texel of texture `t` nearest to (u, v), with u and v wrapped into
/// [0, 1). O(1).
export Scene.Texel(&self, t: int, u: f64, v: f64): geom.Vec {
    tex := self.textures[t]
    x := Wrap(u, tex.width)
    // Row 0 is the top of the image, and v grows upward.
    y := tex.height - 1 - Wrap(v, tex.height)
    i := tex.offset + (y * tex.width + x) * 3
    return geom.V(Cast[f64](self.texels[i]), Cast[f64](self.texels[i + 1]), Cast[f64](self.texels[i + 2]))
}

// The index in [0, n) of the cell holding Fract(u) * n. A coordinate that
// is not finite maps to 0.
Wrap(u: f64, n: int): int {
    f := math.Fract(u)
    if !math.IsFinite(f) {
        return 0
    }
    // Fract keeps the sign of a negative u, so fold it into [0, 1).
    w := if f < 0.0 { f + 1.0 } else { f }
    k := Cast[int](math.Floor(w * Cast[f64](n)))
    return if k >= n { n - 1 } else { k }
}

impl Free for Scene {
    Free(self, a: &Alloc) {
        Scene(materials, textures, texels, lights, spheres, planes, tris, ..) := self
        materials.Free(a)
        textures.Free(a)
        texels.Free(a)
        lights.Free(a)
        spheres.Free(a)
        planes.Free(a)
        tris.Free(a)
    }
}

test "Wrap folds negative and large coordinates into the texture" {
    Assert(Wrap(0.25, 4) == 1)
    Assert(Wrap(-0.25, 4) == 3)
    Assert(Wrap(7.5, 4) == 2)
}
