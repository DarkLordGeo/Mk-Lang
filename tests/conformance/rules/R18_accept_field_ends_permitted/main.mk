// verdict: ok
// rule: R18
// rule: S-G135
// note: the same field-only `ends self` as R18_reject_field_ends is a primitive, which a module listed in unsafe_modules may declare; the root module calls it
import arenas

Wipe(o: mut arenas.O) {
    o.Clear()
}
