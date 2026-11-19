// verdict: reject E0706
// note: inside a generic, an imported clause that reads an enum value no clause can spell, and a const generic the caller's clauses do not bound to values where it holds, cannot be judged here: no restatement is asked, since the restated text would keep the callee's names for the value, which the caller's own names could match by text
import lib

enum Mode {
    A
    B
}

Honest[const N: int](t: &[4]int): int
    requires N >= 0 && N < 4
{
    return lib.At[N, lib.MB](t) // <- E0706
}

Spoof[const N: int, const D: Mode](t: &[4]int): int
    requires N >= 0 && N < 4 && D == Mode.A
{
    return lib.At[N, lib.MB](t) // <- E0706
}

Listed[const N: int](t: &[4]int): int
    requires N == 0 || N == 1
{
    return lib.At[N, lib.MA](t)
}

Calls(t: &[4]int): int {
    return Honest[0](t) + Spoof[0, Mode.A](t) + Listed[1](t)
}
