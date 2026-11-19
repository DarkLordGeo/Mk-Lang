// Why: kernels compiled for AVX2 or AVX-512 may run only on a CPU that has
// those features.
// What: Auto, V256 and V512, and the feature sets V256Needs and V512Needs.
// How: the one module that names the cpu-clause kernels as values, which
// needs unsafe: each table is built only where the running CPU has the
// features of every function in it.

import src.kernel
import std.simd

/// The features the v256 kernels are compiled for.
export V256Needs(): simd.CpuFeatures {
    return simd.CpuFeatures.Of("x86-64-v3")
}

/// The features the v512 kernels are compiled for.
export V512Needs(): simd.CpuFeatures {
    return simd.CpuFeatures.Of("x86-64-v4")
}

/// The v256 kernels: AVX2 widths, adding by FMA. `none` unless the running
/// CPU has x86-64-v3. O(1).
export V256(): ?kernel.Kernels {
    needs := V256Needs()
    if !simd.Cpu().HasAll(&needs) {
        return none
    }
    unsafe "Cpu() has every feature of x86-64-v3, the clause of each function named here" {
        return some(V256Table())
    }
}

/// The v512 kernels: AVX-512 widths, adding by FMA. `none` unless the
/// running CPU has x86-64-v4. O(1).
export V512(): ?kernel.Kernels {
    if !simd.Cpu().Has("x86-64-v4") {
        return none
    }
    unsafe "Cpu() has x86-64-v4, the clause of each function named here" {
        return some(V512Table())
    }
}

/// The widest table the running CPU can run, by `simd.Dispatch`: v512,
/// then v256, then the baseline v128. O(1).
export Auto(): kernel.Kernels {
    unsafe "Dispatch returns a table only when Cpu() has its needs, the clause of each function in it" {
        choices := [
            simd.Choice[kernel.Kernels](needs: V512Needs(), f: V512Table()),
            simd.Choice[kernel.Kernels](needs: V256Needs(), f: V256Table()),
        ]
        return simd.Dispatch(choices[..], kernel.V128())
    }
}

/// Names the v256 functions; called only inside unsafe blocks whose reason
/// is that the CPU has x86-64-v3.
V256Table(): kernel.Kernels {
    unsafe "every caller has checked Cpu() for x86-64-v3 first" {
        return kernel.Kernels(
            name: "v256",
            threshold: kernel.ThresholdV256,
            invert: kernel.InvertV256,
            brighten: kernel.BrightenV256,
            lut: kernel.LutV256,
            convolve: kernel.ConvolveV256,
            mirror: kernel.MirrorV256,
            gray: kernel.GrayV256,
            half: kernel.HalfV256,
            double: kernel.DoubleV256,
            gain: kernel.GainV256,
            sobel: kernel.SobelV256,
            resize: kernel.ResizeV256,
            morph: kernel.MorphV256,
            swap: kernel.SwapV256,
            posterize: kernel.PosterizeV256,
            gamma: kernel.GammaV256,
        )
    }
}

/// Names the v512 functions; called only inside unsafe blocks whose reason
/// is that the CPU has x86-64-v4.
V512Table(): kernel.Kernels {
    unsafe "every caller has checked Cpu() for x86-64-v4 first" {
        return kernel.Kernels(
            name: "v512",
            threshold: kernel.ThresholdV512,
            invert: kernel.InvertV512,
            brighten: kernel.BrightenV512,
            lut: kernel.LutV512,
            convolve: kernel.ConvolveV512,
            mirror: kernel.MirrorV512,
            gray: kernel.GrayV512,
            half: kernel.HalfV512,
            double: kernel.DoubleV512,
            gain: kernel.GainV512,
            sobel: kernel.SobelV512,
            resize: kernel.ResizeV512,
            morph: kernel.MorphV512,
            swap: kernel.SwapV512,
            posterize: kernel.PosterizeV512,
            gamma: kernel.GammaV512,
        )
    }
}
