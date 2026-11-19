// Why: queues name orders by handle, so an order's value must outlive its
// removal from the book until no queue can reach it.
// What: Orders, the store of every order the engine knows.
// How: values in a generational pool, a live flag per arrival number, an
// index from client ids, and the set of ids that are done; Sweep frees the
// dead values.

import std.coll
import std.mem

/// The order store. An order is live while its flag is set; a dead order's
/// value stays in the pool, where queues may still name it, until Sweep.
export view struct Orders {
    pool: mem.Pool[Order]
    flags: coll.Bitset
    index: Map[ClientId, mem.Handle]
    done: Set[ClientId]
    seq: int
}

/// An empty store for at most `capacity` arrivals, all in `a`. Fails with
/// OutOfMemory when `a` cannot hold the flags. O(capacity / 64).
export Orders.New(a: &Arena, capacity: int): OutOfMemory!Orders from a {
    // A fixed hash key keeps the index's order, and so nothing printed,
    // independent of the process.
    key := HashKey.Of(0x6f72646572626f6f, 0x6b2d696e64657800)
    return Orders(
        pool: mem.Pool[Order].New(a),
        flags: try coll.Bitset.New(a, capacity),
        index: Map[ClientId, mem.Handle].NewKeyed(a, key),
        done: Set[ClientId].New(a),
        seq: 0,
    )
}

/// The capacity in arrivals.
export Orders.Capacity(&self): int {
    return self.flags.Len()
}

/// Whether `id` names a live order.
export Orders.Has(&self, id: &ClientId): bool {
    return self.index.Has(id)
}

/// Whether `id` named an order that has since filled or been cancelled.
export Orders.WasDone(&self, id: &ClientId): bool {
    return self.done.Has(id)
}

/// The handle of the live order `id`.
export Orders.Find(&self, id: &ClientId): ?mem.Handle {
    h := self.index.Get(id) else {
        return none
    }
    return some(*h)
}

/// The order `h` names if it is still live.
export Orders.Live(&self, h: mem.Handle): ?&Order {
    o := self.pool.Get(h) else {
        return none
    }
    if !self.flags.Has(o.seq) {
        return none
    }
    return some(o)
}

/// The order `h` names, live or dead, mut.
export Orders.Mut(mut self, h: mem.Handle): ?mut Order {
    return self.pool.GetMut(h)
}

/// The taker and the maker, both mut, for one execution.
export Orders.Pair(mut self, taker: mem.Handle, maker: mem.Handle): ?(a: mut Order, b: mut Order) {
    return self.pool.GetPair(taker, maker)
}

/// Stores a new live order under `id`, numbering it with the next arrival
/// number; a reused id leaves the done set. Fails with OutOfMemory when the
/// arena cannot grow, and panics past the capacity.
export Orders.Add(mut self, o: Order): OutOfMemory!mem.Handle {
    var order := o
    order.seq = self.seq
    h := try self.pool.Add(order)
    try self.index.Set(order.id, h)
    self.flags.Set(order.seq)
    self.seq += 1
    _ = self.done.Remove(&order.id)
    return h
}

/// Stores the new order `o` in place of the live order with the same id,
/// which dies; returns the new order's handle. Fails with OutOfMemory when
/// the arena cannot grow.
export Orders.Reissue(mut self, o: Order): OutOfMemory!mem.Handle {
    var order := o
    order.seq = self.seq
    h := try self.pool.Add(order)
    if old := try self.index.Replace(order.id, h) {
        if prev := self.pool.Get(old) {
            self.flags.Remove(prev.seq)
        }
    }
    self.flags.Set(order.seq)
    self.seq += 1
    return h
}

/// Marks the order `h` done: its flag clears, its id leaves the index and
/// joins the done set. Fails with OutOfMemory when the done set cannot grow.
export Orders.Retire(mut self, h: mem.Handle): OutOfMemory!void {
    o := self.pool.Get(h) else {
        return
    }
    id := o.id
    self.flags.Remove(o.seq)
    _ = self.index.Remove(&id)
    _ = try self.done.Add(id)
}

/// Drops an order that never entered the book, such as a market order with
/// nothing to trade against.
export Orders.Discard(mut self, h: mem.Handle) {
    _ = self.pool.Free(h)
}

/// Drops the values of dead orders; queues that still name them see stale
/// handles. Returns how many went.
export Orders.Sweep(mut self): int {
    flags := &self.flags
    return self.pool.Sweep(fn(h: mem.Handle, o: &Order): bool {
        _ = h
        return flags.Has(o.seq)
    })
}

/// How many orders are live.
export Orders.LiveCount(&self): int {
    // Every live order has one flag and one index entry.
    Assert(self.index.Len() == self.flags.Count())
    return self.flags.Count()
}

/// How many order values the pool holds, dead ones not yet swept included.
export Orders.Held(&self): int {
    return self.pool.Len()
}

/// The live orders in `a`, in arrival order. O(n log n).
export Orders.Resting(&self, a: &Arena): OutOfMemory!List[Order] from a {
    var out := List[Order].New(a)
    for (handle, value) in self.pool.Iter() {
        _ = handle
        if self.flags.Has(value.seq) {
            try out.Push(*value)
        }
    }
    out.Sort(fn(x: &Order, y: &Order): bool { return x.seq < y.seq })
    return out
}
