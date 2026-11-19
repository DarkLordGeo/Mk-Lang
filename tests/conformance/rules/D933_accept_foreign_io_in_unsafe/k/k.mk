import std.raw
import std.runtime

export RoundTrip(): bool {
    var word: u64 = 0
    unsafe "ctx is the address of a local of this frame; the Foreign is released before the frame returns" {
        ctx := raw.PtrCast[u64, u8](raw.OfMut(mut word))
        f := runtime.ForeignIo(ctx, &runtime.InertTable)
        same := raw.Addr(runtime.ForeignContext(f.Io())) == raw.Addr(ctx)
        f.Release()
        return same
    }
}
