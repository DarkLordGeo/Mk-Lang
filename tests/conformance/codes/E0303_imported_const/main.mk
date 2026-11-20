// verdict: reject E0303
// note: only literal operands are judged at check time; a const operand, here one imported from another module, leaves the operator to run time, so a guarded division by a zero const compiles
import lib

Over(): u8 {
    return 200 + 100 // <- E0303
}

Guarded(): int {
    if lib.Zero != 0 {
        return 8 / lib.Zero
    }
    return 0
}

Shortcut(): bool {
    return lib.Zero == 0 || 8 / lib.Zero > 1
}
