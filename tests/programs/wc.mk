// Word count: the ten most frequent words of the file args[1], ties by word in byte
// order. Input wc.in, expected output wc.out; cases in wc.cases.

import std.fs

struct Entry {
    word: str
    count: int
}

ByCount(a: &Entry, b: &Entry): bool {
    if a.count != b.count {
        return a.count > b.count
    }
    return a.word < b.word
}

Count(a: &Arena, text: str into a): OutOfMemory!List[Entry] from a, text {
    var counts := Map[str, int].New(a)
    for word in text.Words() {
        n := try counts.Ensure(word, 0)
        *n += 1
    }
    var out := List[Entry].New(a)
    for (key, value) in counts.Iter() {
        try out.Push(Entry(word: *key, count: *value))
    }
    out.Sort(ByCount)
    return out
}

export Main(sys: &System): fs.Error|OutOfMemory|WriteError!void {
    text := try fs.ReadText(sys.arena, sys.io, sys.args[1])
    entries := try Count(sys.arena, text)
    var out := sys.Stdout()
    for i, e in entries {
        if i == 10 {
            break
        }
        try Print(mut out, "{word} {count}\n", (word: e.word, count: e.count))
    }
}
