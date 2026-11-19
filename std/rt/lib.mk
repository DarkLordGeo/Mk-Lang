// Why: in a library loaded into a host, fault reports must look up Mk
// functions in the library's own image, not the host executable's.
//
// What: SetImage, which replaces the image bounds rt.Init recorded.
//
// How: std.task.StartLibrary calls it once with sys.LibraryImage's bounds,
// before any task or fault, so FaultSite reads the library's image.

/// SetImage replaces the image bounds rt.Init took (the host executable's,
/// in a library) with a library's own (sys.LibraryImage's). Called once, by
/// std.task.StartLibrary, before any task or fault. O(1); allocates nothing.
export SetImage(lo: uint, hi: uint) {
    // Edge: as Init's stores, before any task or fault reads them.
    Rt.image_lo.Store(u64(lo))
    Rt.image_hi.Store(u64(hi))
}
