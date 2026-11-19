// Why: Each protocol command has its own rules and replies; they are kept apart
// from the connection loop.
//
// What: Handle, which dispatches a parsed line, and one function per command:
// Nick, Join, Part, Say, Who, ListRooms, Topic and History.
//
// How: Each handler checks the room table under the hub's lock, writes its
// reply to the client, and broadcasts an event frame to the room when the
// command changes it.

import std.atomic
import src.hub
import src.proto

/// Runs one command line and writes its reply, which always ends with a line
/// that starts `OK` or `ERR`. Fails only to end the connection.
Handle(c: &Ctx, hb: &hub.Hub, me: mut Me, w: mut Writer, line: str): Ended!void *c into me, *hb into hb {
    cmd := proto.Parse(line) else e {
        match e {
            proto.Unknown {
                try Reply(w, "ERR 421 unknown command")
            }
            proto.Usage(code) {
                Print(w, "ERR {code} bad arguments\n", (code)) else {
                    fail Broken
                }
            }
            proto.BadName {
                try Reply(w, "ERR 432 bad name")
            }
        }
        return
    }
    match cmd {
        .Nick(name) {
            try Nick(c, hb, me, w, name)
        }
        .Join(room) {
            try Join(c, hb, me, w, room)
        }
        .Part(room) {
            try Part(c, hb, me, w, room)
        }
        .Say(room, text) {
            try Say(c, hb, me, w, room, text)
        }
        .Who(room) {
            try Who(c, hb, w, room)
        }
        .Rooms {
            try ListRooms(c, hb, w)
        }
        .Topic(room, text) {
            try Topic(c, hb, me, w, room, text)
        }
        .History(room, count) {
            try History(c, hb, w, room, count)
        }
        .Motd(text) {
            try Motd(c, hb, me, w, text)
        }
        .Stats {
            s := &hb.stats
            Print(w, "STATS online={o} peak={p} messages={m} limited={l} dropped={d}\nOK stats\n", (
                o: s.online.Load(atomic.Acquire),
                p: s.peak.Load(atomic.Relaxed),
                m: s.messages.Load(),
                l: s.limited.Load(),
                d: s.dropped.Load(),
            )) else {
                fail Broken
            }
        }
        .Whoami {
            Print(w, "YOU nick={n} op={op} rooms={r} memory={b} peak={p}\nOK whoami\n", (
                n: me.nick.Str(),
                op: me.op,
                r: me.joined.Len(),
                b: c.use.Live(),
                p: c.use.Peak(),
            )) else {
                fail Broken
            }
        }
        .Ping(tag) {
            Print(w, "OK pong {tag}\n", (tag)) else {
                fail Broken
            }
        }
        .Kick(nick) {
            try Kick(c, hb, me, w, nick)
        }
        .Oper(token) {
            if hb.cfg.token.Str().Len() == 0 || token != hb.cfg.token.Str() {
                try Reply(w, "ERR 464 bad operator token")
                return
            }
            me.op = true
            try Reply(w, "OK oper")
        }
        .Shutdown(token) {
            if hb.cfg.token.Str().Len() == 0 || token != hb.cfg.token.Str() {
                try Reply(w, "ERR 464 bad operator token")
                return
            }
            try Reply(w, "OK shutdown")
            hb.stop.NotifyOne()
        }
        .Quit {
            try Reply(w, "OK bye")
            w.Flush() else {
                fail Broken
            }
            fail Quit
        }
    }
}

/// Builds `* <who> <what> <room>` and the like into a frame.
Event(c: &Ctx, hb: &hub.Hub, f: str, a: str, b: str): Ended!proto.Frame {
    s := c.arena.Fmt("* {a}{f}{b}", (a, f, b)) else {
        fail Broken
    }
    return proto.Frame.Of(proto.Kind.Line, s)
}

Tell(c: &Ctx, hb: &hub.Hub, room: str, frame: proto.Frame): Ended!int *hb into hb {
    return hb.Broadcast(c.io, room, frame, c.id) else e {
        match e {
            Canceled {
                fail Canceled
            }
            OutOfMemory {
                fail Broken
            }
        }
    }
}

Nick(c: &Ctx, hb: &hub.Hub, me: mut Me, w: mut Writer, name: str): Ended!void {
    table := hb.Table(c.io) else {
        fail Canceled
    }
    var g := table.Lock(c.io) else {
        fail Canceled
    }
    g.Mut().SetNick(c.id, name) else {
        g.Unlock()
        try Reply(w, "ERR 433 nick in use")
        return
    }
    g.Unlock()
    me.nick = proto.Name.Of(name)
    Print(w, "OK nick {name}\n", (name)) else {
        fail Broken
    }
}

Join(c: &Ctx, hb: &hub.Hub, me: mut Me, w: mut Writer, room: str): Ended!void *c into me, *hb into hb {
    if me.nick.Str().Len() == 0 {
        try Reply(w, "ERR 451 set a nick first")
        return
    }
    // the client's own record of the room comes first: it is what the
    // memory budget limits, and nothing is shared yet if it fails
    var name := HeapText.New(c.ca)
    name.Push(c.ca, room) else {
        name.Free(c.ca)
        try Reply(w, "ERR 507 client memory budget exhausted")
        return
    }
    me.joined.Push(c.ca, name) else {
        name.Free(c.ca)
        try Reply(w, "ERR 507 client memory budget exhausted")
        return
    }
    table := hb.Table(c.io) else {
        fail Canceled
    }
    var g := table.Lock(c.io) else {
        fail Canceled
    }
    i := g.Mut().Ensure(room, "") else {
        g.Unlock()
        Forget(c, hb, me, room)
        try Reply(w, "ERR 405 too many rooms")
        return
    }
    g.Mut().Join(i, c.id) else {
        g.Unlock()
        Forget(c, hb, me, room)
        try Reply(w, "ERR 443 already in room")
        return
    }
    topic := Copy(c, hb, g.Ref().At(i).Topic()) else {
        g.Unlock()
        fail Broken
    }
    count := g.Ref().At(i).count
    g.Unlock()
    _ = try Tell(c, hb, room, try Event(c, hb, " joined ", me.nick.Str(), room))
    Print(w, "OK join {room} members={count} topic={topic}\n", (room, count, topic)) else {
        fail Broken
    }
}

/// Copies `s` into the connection's arena, so it outlives a lock.
Copy(c: &Ctx, hb: &hub.Hub, s: str): Ended!str from c.arena {
    return c.arena.Dup(s) else {
        fail Broken
    }
}

/// Removes `room` from the client's own list of rooms.
Forget(c: &Ctx, hb: &hub.Hub, me: mut Me, room: str) {
    var at: ?int = none
    for i, t in &me.joined {
        if t.Str() == room {
            at = some(i)
        }
    }
    if i := at {
        me.joined.Remove(i).Free(c.ca)
    }
}

Part(c: &Ctx, hb: &hub.Hub, me: mut Me, w: mut Writer, room: str): Ended!void *hb into hb {
    table := hb.Table(c.io) else {
        fail Canceled
    }
    var g := table.Lock(c.io) else {
        fail Canceled
    }
    i := g.Ref().Find(room) else {
        g.Unlock()
        try Reply(w, "ERR 403 no such room")
        return
    }
    g.Mut().Part(i, c.id) else {
        g.Unlock()
        try Reply(w, "ERR 442 not in room")
        return
    }
    g.Unlock()
    Forget(c, hb, me, room)
    _ = try Tell(c, hb, room, try Event(c, hb, " left ", me.nick.Str(), room))
    Print(w, "OK part {room}\n", (room)) else {
        fail Broken
    }
}

/// Whether this connection is in `room`; `none` when there is no such room.
Member(c: &Ctx, hb: &hub.Hub, room: str): Ended!?bool {
    table := hb.Table(c.io) else {
        fail Canceled
    }
    g := table.LockRead(c.io) else {
        fail Canceled
    }
    defer g.Unlock()
    i := g.Ref().Find(room) else {
        return none
    }
    return some(g.Ref().At(i).Has(c.id))
}

Say(c: &Ctx, hb: &hub.Hub, me: mut Me, w: mut Writer, room: str, text: str): Ended!void *hb into hb {
    member := try Member(c, hb, room) else {
        try Reply(w, "ERR 403 no such room")
        return
    }
    if !member {
        try Reply(w, "ERR 404 not in room")
        return
    }
    slot := hb.Slot(c.id)
    if hb.cfg.burst > 0 {
        if !slot.rate.TryAcquire(1) {
            _ = hb.stats.limited.Add(1)
            try Reply(w, "ERR 429 rate limited")
            return
        }
        _ = slot.used.FetchAdd(1, atomic.Relaxed)
    }
    s := c.arena.Fmt("<{room}> {nick}: {text}", (room, nick: me.nick.Str(), text)) else {
        fail Broken
    }
    _ = hb.stats.messages.Add(1)
    n := try Tell(c, hb, room, proto.Frame.Of(proto.Kind.Line, s))
    hb.history.Record(c.io, room, me.nick.Str(), text) else {
        fail Canceled
    }
    Print(w, "OK msg {n}\n", (n)) else {
        fail Broken
    }
}

Who(c: &Ctx, hb: &hub.Hub, w: mut Writer, room: str): Ended!void {
    table := hb.Table(c.io) else {
        fail Canceled
    }
    g := table.LockRead(c.io) else {
        fail Canceled
    }
    defer g.Unlock()
    rooms := g.Ref()
    i := rooms.Find(room) else {
        try Reply(w, "ERR 403 no such room")
        return
    }
    Print(w, "MEMBERS {room}", (room)) else {
        fail Broken
    }
    var at := -1
    for s := rooms.At(i).NextMember(at) {
        at = s
        Print(w, " {n}", (n: rooms.Nick(s))) else {
            fail Broken
        }
    }
    try Reply(w, "\nOK who")
}

ListRooms(c: &Ctx, hb: &hub.Hub, w: mut Writer): Ended!void {
    table := hb.Table(c.io) else {
        fail Canceled
    }
    g := table.LockRead(c.io) else {
        fail Canceled
    }
    defer g.Unlock()
    rooms := g.Ref()
    for i in 0..rooms.Len() {
        r := rooms.At(i)
        Print(w, "ROOM {name} {count} {topic}\n", (name: r.name.Str(), count: r.count, topic: r.Topic())) else {
            fail Broken
        }
    }
    Print(w, "OK rooms {n}\n", (n: rooms.Len())) else {
        fail Broken
    }
}

Topic(c: &Ctx, hb: &hub.Hub, me: mut Me, w: mut Writer, room: str, text: str): Ended!void *hb into hb {
    table := hb.Table(c.io) else {
        fail Canceled
    }
    var g := table.Lock(c.io) else {
        fail Canceled
    }
    i := g.Ref().Find(room) else {
        g.Unlock()
        try Reply(w, "ERR 403 no such room")
        return
    }
    if !g.Ref().At(i).Has(c.id) {
        g.Unlock()
        try Reply(w, "ERR 404 not in room")
        return
    }
    g.Mut().SetTopic(i, text)
    g.Unlock()
    _ = try Tell(c, hb, room, try Event(c, hb, " set the topic of ", me.nick.Str(), room))
    try Reply(w, "OK topic")
}

History(c: &Ctx, hb: &hub.Hub, w: mut Writer, room: str, count: int): Ended!void {
    g := hb.history.recent.Lock(c.io) else {
        fail Canceled
    }
    defer g.Unlock()
    ring := g.Ref()
    var skip := 0
    for i in 0..ring.Len() {
        if ring.At(i).room.Str() == room {
            skip += 1
        }
    }
    skip = if skip > count { skip - count } else { 0 }
    var shown := 0
    for i in 0..ring.Len() {
        e := ring.At(i)
        if e.room.Str() != room {
            continue
        }
        if skip > 0 {
            skip -= 1
            continue
        }
        Print(w, "HIST {room} <{nick}> {text}\n", (room, nick: e.nick.Str(), text: e.Text())) else {
            fail Broken
        }
        shown += 1
    }
    Print(w, "OK history {shown}\n", (shown)) else {
        fail Broken
    }
}

Motd(c: &Ctx, hb: &hub.Hub, me: mut Me, w: mut Writer, text: str): Ended!void *hb into hb {
    if text.Len() == 0 {
        m := hb.Greeting(c.io) else {
            fail Canceled
        }
        Print(w, "MOTD {t}\nOK motd\n", (t: m.Ref().Text())) else {
            m.Free(hb.heap)
            fail Broken
        }
        m.Free(hb.heap)
        return
    }
    if !me.op {
        try Reply(w, "ERR 481 operator only")
        return
    }
    hb.SetMotd(c.io, text) else e {
        match e {
            Canceled {
                fail Canceled
            }
            OutOfMemory {
                fail Broken
            }
        }
    }
    _ = hb.NoticeAll(c.io, proto.Frame.Of(proto.Kind.Line, "NOTICE the message of the day changed")) else {
        fail Broken
    }
    try Reply(w, "OK motd set")
}

Kick(c: &Ctx, hb: &hub.Hub, me: mut Me, w: mut Writer, nick: str): Ended!void *hb into hb {
    if !me.op {
        try Reply(w, "ERR 481 operator only")
        return
    }
    table := hb.Table(c.io) else {
        fail Canceled
    }
    g := table.LockRead(c.io) else {
        fail Canceled
    }
    s := g.Ref().SlotOf(nick) else {
        g.Unlock()
        try Reply(w, "ERR 401 no such nick")
        return
    }
    g.Unlock()
    hb.Slot(s).ctl.Send(c.io, hub.Ctl.Kick) else {
        fail Canceled
    }
    try Reply(w, "OK kick")
}
