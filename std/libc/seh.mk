// Why: a C source compiled for Windows that uses __try/__except names
// __C_specific_handler in its unwind data, and a C runtime normally supplies
// it; Mk links C sources against no C runtime.
//
// What: __C_specific_handler, the language handler of structured exception
// handling on x86_64-windows.
//
// How: it forwards to the one ntdll.dll exports, which every Windows process
// has loaded. On other targets nothing calls it.

import std.raw

// ExceptionContinueSearch is EXCEPTION_DISPOSITION's "not handled here":
// the dispatcher goes on to the next frame's handler.
const ExceptionContinueSearch: i32 = 1

extern GetModuleHandleW(name: *u16): uint
extern GetProcAddress(module: uint, name: *u8): *u8

/// The Windows C language handler for __try, __except and __finally, which
/// C code built for Windows calls. It runs ntdll's handler, looked up at
/// each call, with the dispatcher's four arguments, and answers
/// continue-search when ntdll has none. O(1) besides ntdll's own scope
/// walk; allocates nothing.
export extern __C_specific_handler(rec: *u8, frame: *u8, ctx: *u8, dc: *u8): i32 {
    if Target.os == Os.Windows {
        // "ntdll.dll" as a NUL-terminated UTF-16 string.
        name: [10]u16 = [110, 116, 100, 108, 108, 46, 100, 108, 108, 0]
        unsafe "kernel32; the module and procedure names are NUL-terminated constants, and ntdll's __C_specific_handler takes the dispatcher's four arguments unchanged" {
            m := GetModuleHandleW(raw.Data(name[..]))
            p := if m == 0 { raw.Null[u8]() } else { GetProcAddress(m, raw.StrData("__C_specific_handler\u{0}")) }
            if !raw.IsNull(p) {
                var w := u64(raw.Addr(p))
                handler := raw.Read(raw.PtrCast[u64, fn(*u8, *u8, *u8, *u8): i32](raw.OfMut(mut w)))
                return handler(rec, frame, ctx, dc)
            }
        }
    }
    return ExceptionContinueSearch
}
