// A package with a path dependency builds and runs:
// app imports units, a package in ../units, and its module
// units.conv. Prints three lengths and their sum.
import units
import units.conv

export Main(sys: &System): WriteError!void {
    xs := [units.Length(mm: 1500), units.Length(mm: conv.InchesToMm(12)), units.Length(mm: 25)]
    var out := sys.Stdout()
    for x in xs[..] {
        try Print(mut out, "{x}\n", (x))
    }
    total := units.Sum(xs[..])
    try Print(mut out, "total {total}\n", (total))
}
