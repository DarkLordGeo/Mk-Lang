// verdict: reject E0706
// note: `open` before an alias that is not a non-generic error set (reason: open)
error Bad

open type Count = int // <- E0706

open type Errors[T] = Bad | T // <- E0706
