// Why: the server's data: owned text keys and values whose keys are chosen
// by clients.
// What: Store (a linear view) with Get, Set, Swap, Append, Del, Apply, Keys,
// Entries and Digest; Op, the operations the log records.
// How: a map on the heap keyed with SipHash (HashKey), so that keys chosen
// by clients cannot collide on purpose.

import std.hash

/// A change to the store, as the write-ahead log records it.
export enum Op: u8 {
    Set = 1
    Del = 2
    Append = 3
}

/// Parses the byte a log record stores its operation as.
export Op.FromByte(b: u8): ?Op {
    return match b {
        1 { some(Op.Set) }
        2 { some(Op.Del) }
        3 { some(Op.Append) }
        _ { none }
    }
}

/// Keys and values, both heap-owned. Every value is a `HeapText` the map
/// owns, released by `Set`, `Replace` and `Free` (the release protocol).
export linear view struct Store {
    map: TextMap[HeapText]
    used: int
}

/// An empty store whose map is keyed with `key`. Allocates nothing yet.
export Store.New(a: &Alloc, key: HashKey): Store {
    return Store(map: TextMap[HeapText].NewKeyed(a, key), used: 0)
}

/// An empty store with the fixed hash, for tools whose input is trusted
/// and whose output must not depend on the process. Allocates nothing yet.
export Store.NewFixed(a: &Alloc): Store {
    return Store(map: TextMap[HeapText].New(a), used: 0)
}

/// The number of keys.
export Store.Len(&self): int {
    return self.map.Len()
}

/// The bytes held in keys and values, without the map's own overhead.
export Store.Bytes(&self): int {
    return self.used
}

/// The value of `key`, as a view that lives while the store is not changed.
export Store.Get(&self, key: str): ?str from self {
    v := self.map.Get(key) else {
        return none
    }
    return some(v.Str())
}

/// Copies `value` into heap text owned by `a`.
NewText(a: &Alloc, value: str): OutOfMemory!HeapText from a {
    var t := HeapText.New(a)
    onerr t.Free(a)
    try t.Push(a, value)
    return t
}

/// Sets `key` to `value`, freeing the value it replaces. O(len); allocates
/// the key the first time and the value each time.
export Store.Set(mut self, a: &Alloc into self, key: str, value: str): OutOfMemory!void {
    if v := self.map.GetMut(key) {
        before := v.Len()
        try v.Set(a, value)
        self.used += value.Len() - before
        return
    }
    t := try NewText(a, value)
    onerr t.Free(a)
    try self.map.Set(a, key, t)
    self.used += key.Len() + value.Len()
}

/// Sets `key` to `value` and hands back the value it had, for `GETSET`.
/// The caller frees what it gets.
export Store.Swap(mut self, a: &Alloc into self, key: str, value: str): OutOfMemory!?HeapText from self {
    t := try NewText(a, value)
    onerr t.Free(a)
    old := try self.map.Replace(a, key, t)
    match &old {
        some(v) {
            self.used += value.Len() - v.Len()
        }
        none {
            self.used += key.Len() + value.Len()
        }
    }
    return old
}

/// Appends `value` to the value of `key`, creating it empty first, and
/// returns the new length. O(len); allocates when the value grows.
export Store.Append(mut self, a: &Alloc into self, key: str, value: str): OutOfMemory!int {
    fresh := self.map.Get(key).IsNone()
    empty := HeapText.New(a)
    onerr empty.Free(a)
    v := try self.map.Ensure(a, key, empty)
    if fresh {
        self.used += key.Len()
    }
    try v.Push(a, value)
    self.used += value.Len()
    return v.Len()
}

/// Removes `key`; reports whether it was there.
export Store.Del(mut self, a: &Alloc, key: str): bool {
    v := self.map.Remove(a, key) else {
        return false
    }
    self.used -= key.Len() + v.Len()
    v.Free(a)
    return true
}

/// Applies one logged change, as recovery does.
export Store.Apply(mut self, a: &Alloc into self, op: Op, key: str, value: str): OutOfMemory!void {
    match op {
        .Set {
            try self.Set(a, key, value)
        }
        .Del {
            _ = self.Del(a, key)
        }
        .Append {
            _ = try self.Append(a, key, value)
        }
    }
}

/// Every key starting with `prefix`, in byte order, copied into `arena`.
/// O(n log n); allocates the list and each key in `arena`.
export Store.Keys(&self, arena: &Arena, prefix: str): OutOfMemory!List[str] from arena {
    var out := List[str].New(arena)
    for (key, value) in self.map.Iter() {
        _ = value
        if key.StartsWith(prefix) {
            // pushing the view `key` is refused (E0604), so it is copied
            try out.Push(try arena.Dup(key))
        }
    }
    out.Sort(fn(x: &str, y: &str): bool { return *x < *y })
    return out
}

/// The entries in the map's order, which follows its hash key.
export view struct Entries {
    it: TextMapIter[HeapText]
}

/// The next key and value, as views into the store.
export Entries.Next(mut self): ?(key: str, value: str) from self {
    (key, value) := self.it.Next() else {
        return none
    }
    return some((key: key, value: value.Str()))
}

/// Iterates over every entry, in no particular order.
export Store.Entries(&self): Entries from self {
    return Entries(it: self.map.Iter())
}

/// A digest of the contents that does not depend on the order the map
/// iterates in: the wrapping sum of a 64-bit xxHash of each entry (its value
/// under the hash of its key as the seed), so two stores with equal contents
/// have equal digests in any process. The value goes through a streaming
/// state, which equals Xx64 of the whole value, so a value kept in pieces
/// hashes the same.
export Store.Digest(&self): u64 {
    var sum: u64 = 0
    for (key, value) in self.map.Iter() {
        var entry := hash.Xx64State.New(hash.Xx64(key.AsBytes(), 0x6B7664))
        entry.Bytes(value.Str().AsBytes())
        sum = sum +% entry.Sum()
    }
    return sum +% Cast[u64](self.map.Len())
}

impl Free for Store {
    Free(self, a: &Alloc) {
        Store(map, ..) := self
        map.Free(a)
    }
}
