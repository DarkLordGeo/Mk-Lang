// Why: every task switch saves one register context and resumes another;
// the layout must match what the assembly support pack's mk_switch and
// mk_task_start read and write.
//
// What: Context, the saved context of a task or a carrier, and the two
// intrinsics Switch and InitContext. Private to std.task.
//
// How: the compiler lowers Switch to a call to mk_switch and InitContext to
// a write of the support pack's initial frame; this file only declares them.

/// Context is [8]u64 with the support pack's word layout: [0] sp, on
/// Windows [1..4] the NT_TIB words.
export type Context = [8]u64

// Switch calls mk_switch: save this context, resume the other.
Switch(from: *Context, to: *Context)

// InitContext writes a fresh frame below top that enters entry(arg) through
// mk_task_start, and sets ctx[0].
InitContext(ctx: *Context, top: *u8, entry: fn(*u8), arg: *u8)
