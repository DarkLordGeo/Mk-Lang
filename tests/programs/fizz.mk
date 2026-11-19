// FizzBuzz from 1 to 15. Expected output: fizz.out (58 bytes, LF only); cases: fizz.cases.

export Main(sys: &System): WriteError!void {
    var out := sys.Stdout()
    for i in 1..16 {
        if i % 15 == 0 {
            try Print(mut out, "fizzbuzz\n")
        } else if i % 3 == 0 {
            try Print(mut out, "fizz\n")
        } else if i % 5 == 0 {
            try Print(mut out, "buzz\n")
        } else {
            try Print(mut out, "{i}\n", (i))
        }
    }
}
