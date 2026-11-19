// Why: the examples in the documentation are short and call helpers they do
// not declare; they still have to compile as tests.
// What: the corpus test prelude: Point, the linear Res with Close and
// Acquire, and the stubs MakePoint, Cond, Use, UseMut and Take.
// How: the corpus runner and mk doc --check give it to each example block;
// the bodiless declarations are signatures only, enough for the checker.

struct Point {
    x: i32
    y: i32
}

linear struct Res {
    id: i32
}

Res.Close(self)
Acquire(name: str): Res
MakePoint(): Point
Cond(): bool
Use[T](x: &T) {}
UseMut[T](x: mut T) {}
Take[T](x: T)
