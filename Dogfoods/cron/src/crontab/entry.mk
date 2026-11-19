// Why: an entry line packs a schedule, a name, options, `--` and a command
// into one line, and each part has its own errors.
//
// What: ReadEntry, the word splitter Words2, GoodName, Special (the `@`
// schedules), FiveFields, Command and Stages for pipelines.
//
// How: the line is split into words; the schedule is read first, options up
// to `--`, and the rest is the command, split at one `|` into two stages.

import std.time

/// A cursor over the words of a line, separated by spaces and tabs.
struct Words2 {
    text: str
    pos: int
}

/// The next word and the offset just past it; none at the end.
Words2.Next(mut self): ?str from self.text {
    for self.pos < self.text.Len() && IsSpace(self.text.Byte(self.pos)) {
        self.pos += 1
    }
    if self.pos >= self.text.Len() {
        return none
    }
    start := self.pos
    for self.pos < self.text.Len() && !IsSpace(self.text.Byte(self.pos)) {
        self.pos += 1
    }
    return some(self.text[start..self.pos])
}

IsSpace(c: u8): bool {
    return c == b' ' || c == b'\t'
}

/// Reads one entry line into `tab.entries`, or reports why it cannot.
ReadEntry(a: &Arena into tab, tab: mut Crontab, line: int, text: str, zone: int): OutOfMemory!void {
    var words := Words2(text: text, pos: 0)
    first := words.Next().Must() // the caller skipped blank lines
    schedule := if first.StartsWith("@") {
        try Special(a, tab, line, first, mut words) else { return }
    } else {
        try FiveFields(a, tab, line, first, mut words) else { return }
    }
    when := text[..words.pos]
    name := words.Next() else {
        try Report(tab, line, "the entry has no name")
        return
    }
    if !GoodName(name) {
        try Report(tab, line, try a.Fmt("{name}: a job name is letters, digits, '-', '_' and '.'", (name)))
        return
    }
    if !Find(&*tab, name).IsNone() {
        try Report(tab, line, try a.Fmt("{name}: a job of this name is already defined", (name)))
        return
    }
    var timeout: ?time.Duration = none
    var detach := false
    var input: ?str = none
    var dashes := false
    for word := words.Next() {
        if word == "--" {
            dashes = true
            break
        }
        if word == "detach" {
            detach = true
        } else if word.StartsWith("timeout=") {
            d := time.ParseDuration(word[8..]) else {
                try Report(tab, line, try a.Fmt("{word}: not a duration such as 30s or 5m", (word)))
                return
            }
            if d.Nanos() <= 0 {
                try Report(tab, line, try a.Fmt("{word}: a timeout is above zero", (word)))
                return
            }
            timeout = some(d)
        } else if word.StartsWith("input=") {
            input = some(try a.Dup(word[6..]))
        } else {
            try Report(tab, line, try a.Fmt("{word}: unknown option (timeout=, detach, input=)", (word)))
            return
        }
    }
    if !dashes {
        try Report(tab, line, "no '--' before the command")
        return
    }
    (argv, then) := try Command(a, tab, line, text[words.pos..]) else { return }
    try tab.entries.Push(Entry(name: try a.Dup(name), line: line, schedule: schedule, when: try a.Dup(when), zone: zone, timeout: timeout, detach: detach, input: input, argv: argv, then: then))
}

/// A job name: letters, digits, `-`, `_` and `.`.
GoodName(name: str): bool {
    for i in 0..name.Len() {
        c := name.Byte(i)
        ok := (c >= b'a' && c <= b'z') || (c >= b'A' && c <= b'Z') || (c >= b'0' && c <= b'9')
        if !ok && c != b'-' && c != b'_' && c != b'.' {
            return false
        }
    }
    return name.Len() > 0
}

/// `@yearly`, `@annually`, `@monthly`, `@weekly`, `@daily`, `@midnight`,
/// `@hourly`, `@reboot`, or `@every D`; none, reported, otherwise.
Special(a: &Arena into tab, tab: mut Crontab, line: int, word: str, words: mut Words2): OutOfMemory!?Schedule {
    text := match word {
        "@yearly" | "@annually" { "0 0 1 1 *" }
        "@monthly" { "0 0 1 * *" }
        "@weekly" { "0 0 * * 0" }
        "@daily" | "@midnight" { "0 0 * * *" }
        "@hourly" { "0 * * * *" }
        "@reboot" { return some(Schedule.Reboot) }
        "@every" {
            arg := words.Next() else {
                try Report(tab, line, "@every needs a duration, such as @every 5m")
                return none
            }
            d := time.ParseDuration(arg) else {
                try Report(tab, line, try a.Fmt("@every {arg}: not a duration such as 30s or 5m", (arg)))
                return none
            }
            if d.Nanos() < time.Seconds(1).Nanos() {
                try Report(tab, line, try a.Fmt("@every {arg}: the shortest period is 1s", (arg)))
                return none
            }
            return some(Schedule.Every(nanos: d.Nanos()))
        }
        _ {
            try Report(tab, line, try a.Fmt("{word}: unknown schedule", (word)))
            return none
        }
    }
    var expanded := Words2(text: text, pos: 0)
    head := expanded.Next().Must() // the table above holds five fields
    return try FiveFields(a, tab, line, head, mut expanded)
}

const FieldNames: []str = ["minute", "hour", "day-of-month", "month", "day-of-week"]

/// The five fields, the first already read as `first`.
FiveFields(a: &Arena into tab, tab: mut Crontab, line: int, first: str, words: mut Words2): OutOfMemory!?Schedule {
    var bits: [5]u64 = [0; 5]
    var stars: [5]bool = [false; 5]
    var text := first
    for i in 0..5 {
        if i > 0 {
            text = words.Next() else {
                try Report(tab, line, try a.Fmt("the schedule has {i} fields, not 5", (i)))
                return none
            }
        }
        b := match i {
            0 { &MinuteBounds }
            1 { &HourBounds }
            2 { &DayBounds }
            3 { &MonthBounds }
            _ { &WeekdayBounds }
        }
        f := ParseField(b, text) else e {
            try Report(tab, line, try FieldMessage(a, FieldNames[i], e))
            return none
        }
        bits[i] = f.bits
        stars[i] = f.star
    }
    // crontab(5): 7 is Sunday as well as 0
    var weekdays := bits[4]
    if weekdays & (u64(1) << 7) != 0 {
        weekdays = (weekdays | 1) & 0x7f
    }
    spec := Spec(
        minutes: bits[0],
        hours: bits[1],
        days: bits[2],
        months: bits[3],
        weekdays: weekdays,
        any_day: stars[2],
        any_weekday: stars[4],
    )
    return some(Schedule.Cron(spec: spec))
}

/// The command after `--`: words, double quotes keeping spaces, `$NAME`
/// and `${NAME}` taken from the variables, and one `|` making a pipeline
/// of two stages.
Command(a: &Arena into tab, tab: mut Crontab, line: int, text: str): OutOfMemory!?(argv: []str, then: []str) from a {
    var stages := Stages(first: List[str].New(a), second: List[str].New(a), stage: 0)
    var word := Text.New(a)
    var in_word := false
    var quoted := false
    var i := 0
    for i < text.Len() {
        c := text.Byte(i)
        if c == b'"' {
            quoted = !quoted
            in_word = true
            i += 1
            continue
        }
        if !quoted && (IsSpace(c) || c == b'|') {
            if in_word {
                try stages.Add(try a.Dup(word.Str()))
                word.Clear()
                in_word = false
            }
            if c == b'|' {
                if stages.stage == 1 {
                    try Report(tab, line, "a command is at most two stages joined by one '|'")
                    return none
                }
                stages.stage = 1
            }
            i += 1
            continue
        }
        in_word = true
        if c == b'$' {
            (name, end) := VarName(text, i + 1)
            value := Lookup(&*tab, name) else {
                try Report(tab, line, try a.Fmt("${name} is not set", (name)))
                return none
            }
            try word.Push(value)
            i = end
            continue
        }
        // a byte at a time keeps UTF-8 whole: the bytes are pushed in order
        start := i
        i += 1
        for i < text.Len() && text.Byte(i) >= 0x80 && text.Byte(i) < 0xc0 {
            i += 1
        }
        try word.Push(text[start..i])
    }
    if quoted {
        try Report(tab, line, "a '\"' is not closed")
        return none
    }
    if in_word {
        try stages.Add(try a.Dup(word.Str()))
    }
    if stages.first.Len() == 0 || (stages.stage == 1 && stages.second.Len() == 0) {
        try Report(tab, line, "a command stage is empty")
        return none
    }
    Stages(first, second, ..) := stages
    argv := first.Done()
    then := second.Done()
    return some((argv: argv[..], then: then[..]))
}

/// The name after a `$` at `at`, bare or in braces, and where it ends.
VarName(text: str, at: int): (name: str, end: int) from text {
    if at < text.Len() && text.Byte(at) == b'{' {
        close := text[at..].Find("}") else {
            return (name: text[at + 1..], end: text.Len())
        }
        return (name: text[at + 1..at + close], end: at + close + 1)
    }
    var end := at
    for end < text.Len() {
        c := text.Byte(end)
        ok := (c >= b'A' && c <= b'Z') || (c >= b'a' && c <= b'z') || (c >= b'0' && c <= b'9') || c == b'_'
        if !ok {
            break
        }
        end += 1
    }
    return (name: text[at..end], end)
}

/// The words of a command's one or two stages, as they are read.
struct Stages {
    first: List[str]
    second: List[str]
    stage: int
}

/// Appends a finished word to the stage being read.
Stages.Add(mut self, word: str into self): OutOfMemory!void {
    if self.stage == 0 {
        try self.first.Push(word)
    } else {
        try self.second.Push(word)
    }
}
