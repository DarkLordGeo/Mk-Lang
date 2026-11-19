// Why: the stack and variables outlive one line, but std.big's values live
// only in an arena that is reset after every line.
//
// What: Machine with New, Free, Load, Keep, Store, Recall and Names, and the
// run's Settings.
//
// How: values are kept as 64-bit words in heap lists and rebuilt with
// big.FromWords when a line loads them.

import std.big

/// Settings are the limits and the output base a run starts with.
export struct Settings {
    /// Output base, 2 to 36
    export base: int
    /// Longest number literal, in digits, that a line may hold
    export max_digits: int
    /// Largest result, in bits, that an operation may produce
    export max_bits: int
}

/// DefaultSettings prints in base 10 and allows 100,000 digits and 4,000,000
/// bits.
export DefaultSettings(): Settings {
    return Settings(base: 10, max_digits: 100_000, max_bits: 4_000_000)
}

// One kept stack entry: its words are words[at..at + len] of Machine.words.
struct Item {
    len: int
    neg: bool
}

// A kept variable: its own words, so one can be replaced without moving the
// others.
struct Kept {
    neg: bool
    words: HeapList[u64]
}

impl Free for Kept {
    Free(self, a: &Alloc) {
        Kept(words, ..) := self
        words.Free(a)
    }
}

/// Machine is the calculator between lines: the stack and the variables in
/// heap lists from one allocator, and the settings. Release it with Free.
export linear view struct Machine {
    words: HeapList[u64]
    items: HeapList[Item]
    vars: TextMap[Kept]
    settings: Settings
}

/// New is an empty machine whose lists will allocate from a.
export Machine.New(a: &Alloc, settings: Settings): Machine {
    return Machine(
        words: HeapList[u64].New(a),
        items: HeapList[Item].New(a),
        vars: TextMap[Kept].New(a),
        settings: settings,
    )
}

/// Free releases the lists, with the allocator they came from.
export Machine.Free(self, a: &Alloc) {
    Machine(words, items, vars, ..) := self
    words.Free(a)
    items.Free(a)
    vars.Free(a)
}

/// Depth is the number of values on the kept stack.
export Machine.Depth(&self): int {
    return self.items.Len()
}

/// Load copies the kept stack into a, bottom first, so the copies outlive
/// any later change to the machine. O(total words); allocates them in a.
export Machine.Load(&self, a: &Arena): OutOfMemory!List[big.Int] from a {
    var out := List[big.Int].New(a)
    var at := 0
    for item in self.items {
        kept := big.FromWords(self.words[at..at + item.len], item.neg)
        try out.Push(try kept.Clone(a))
        at += item.len
    }
    return out
}

/// Keep replaces the kept stack with stack. Fails with OutOfMemory when the
/// machine's allocator cannot hold it, leaving the kept stack empty.
/// O(total words); allocates them from a.
export Machine.Keep(mut self, a: &Alloc, stack: &List[big.Int]): OutOfMemory!void {
    self.words.Clear()
    self.items.Clear()
    onerr self.words.Clear()
    onerr self.items.Clear()
    for i in 0..stack.Len() {
        v := &stack[i]
        words := v.Words()
        try self.words.Extend(a, words)
        try self.items.Push(a, Item(len: words.Len(), neg: v.Sign() < 0))
    }
}

/// Store sets the variable name to v, replacing any value it had. Fails with
/// OutOfMemory when the allocator cannot hold the words. O(words).
export Machine.Store(mut self, a: &Alloc into self, name: str, v: &big.Int): OutOfMemory!void {
    var words := HeapList[u64].New(a)
    onerr words.Free(a)
    try words.Extend(a, v.Words())
    kept := Kept(neg: v.Sign() < 0, words: words)
    onerr kept.Free(a)
    try self.vars.Set(a, name, kept)
}

/// Recall is a copy in a of the variable name, or none when it is not set.
/// O(words); allocates them in a.
export Machine.Recall(&self, a: &Arena, name: str): OutOfMemory!?big.Int from a {
    entry := self.vars.Get(name) else {
        return none
    }
    kept := big.FromWords(entry.words[..], entry.neg)
    return some(try kept.Clone(a))
}

/// Names lists the variables in a, sorted by name.
export Machine.Names(&self, a: &Arena): OutOfMemory!List[str] from a {
    var out := List[str].New(a)
    for (key, value) in self.vars.Iter() {
        _ = value
        try out.Push(try a.Dup(key))
    }
    out.Sort(fn(x: &str, y: &str): bool { return *x < *y })
    return out
}
