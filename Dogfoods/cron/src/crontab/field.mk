// Why: the five time fields must accept what crontab(5) accepts, and nothing
// else.
//
// What: ParseField, Bounds for each field, the month and day names, and the
// field errors.
//
// How: a field is a list of items separated by commas, each `*`, a value, a
// range `a-b`, and either of the last two (or `*`) with a step `/n`. Months
// and weekdays take their three-letter English names, and weekday 7 is
// Sunday as 0 is. The result is a bit set per field.

/// Item `item` (from 1) of a field is neither a number nor a name.
export error BadValue(item: int)
/// Item `item` names `value`, outside the field's range.
export error OutOfRange(item: int, value: i64)
/// Item `item` has a step that is not a number from 1 to the range's top.
export error BadStep(item: int)
/// Item `item` is a range whose end is below its start.
export error Backwards(item: int)

/// Why one field of a schedule could not be read.
export type FieldError = BadValue | OutOfRange | BadStep | Backwards

const MonthNames: []str = ["jan", "feb", "mar", "apr", "may", "jun", "jul", "aug", "sep", "oct", "nov", "dec"]
const DayNames: []str = ["sun", "mon", "tue", "wed", "thu", "fri", "sat"]

/// The range a field accepts and the names it knows: `names[i]` is the
/// value `first_name + i`.
struct Bounds {
    lo: i64
    hi: i64
    names: []str
    first_name: i64
}

const MinuteBounds = Bounds(lo: 0, hi: 59, names: [], first_name: 0)
const HourBounds = Bounds(lo: 0, hi: 23, names: [], first_name: 0)
const DayBounds = Bounds(lo: 1, hi: 31, names: [], first_name: 0)
const MonthBounds = Bounds(lo: 1, hi: 12, names: MonthNames, first_name: 1)
const WeekdayBounds = Bounds(lo: 0, hi: 7, names: DayNames, first_name: 0)

/// A value by number or by name, lower case or not.
Value(b: &Bounds, item: int, text: str): FieldError!i64 {
    for i, name in b.names {
        if SameLetters(text, name) {
            return b.first_name + i64(i)
        }
    }
    n := text.ParseInt() else {
        fail BadValue(item: item)
    }
    if n < b.lo || n > b.hi {
        fail OutOfRange(item: item, value: n)
    }
    return n
}

/// ASCII comparison that ignores case; `want` is lower case.
SameLetters(text: str, want: str): bool {
    if text.Len() != want.Len() {
        return false
    }
    for i in 0..text.Len() {
        var c := text.Byte(i)
        if c >= b'A' && c <= b'Z' {
            c += 32
        }
        if c != want.Byte(i) {
            return false
        }
    }
    return true
}

/// Reads one field into a bit set: bit v is set when value v matches.
/// `star` is true when the field is a bare `*`, which crontab(5) treats
/// apart for the day fields. O(n + hi) for a field of n bytes.
ParseField(b: &Bounds, text: str): FieldError!(bits: u64, star: bool) {
    var bits: u64 = 0
    var item := 0
    for part in text.Split(",") {
        item += 1
        bits = bits | try Item(b, item, part)
    }
    return (bits, star: text == "*")
}

/// One item of a list: `*`, `v`, `a-b`, each optionally `/step`.
Item(b: &Bounds, item: int, part: str): FieldError!u64 {
    var range := part
    var step: i64 = 1
    if slash := part.Find("/") {
        range = part[..slash]
        step = part[slash + 1..].ParseInt() else {
            fail BadStep(item: item)
        }
        if step < 1 || step > b.hi {
            fail BadStep(item: item)
        }
    }
    var lo := b.lo
    var hi := b.hi
    if range != "*" {
        if dash := range.Find("-") {
            lo = try Value(b, item, range[..dash])
            hi = try Value(b, item, range[dash + 1..])
            if hi < lo {
                fail Backwards(item: item)
            }
        } else {
            lo = try Value(b, item, range)
            // `v/step` runs from v to the end of the range, as in cronie
            hi = if step > 1 { b.hi } else { lo }
        }
    }
    var bits: u64 = 0
    var v := lo
    for v <= hi {
        bits = bits | (u64(1) << Cast[u64](v))
        v += step
    }
    return bits
}

/// A message for a field error, naming the field and the item.
FieldMessage(a: &Arena, field: str, e: FieldError): OutOfMemory!str from a {
    return match e {
        BadValue(item) { try a.Fmt("{field} field, item {item}: not a number or a name", (field, item)) }
        OutOfRange(item, value) {
            try a.Fmt("{field} field, item {item}: {value} is out of range", (field, item, value))
        }
        BadStep(item) { try a.Fmt("{field} field, item {item}: bad step", (field, item)) }
        Backwards(item) { try a.Fmt("{field} field, item {item}: range runs backwards", (field, item)) }
    }
}
