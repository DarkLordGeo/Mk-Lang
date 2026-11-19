// Why: Room membership and nicks change on every command and are read on every
// broadcast.
//
// What: Room, Rooms and their operations (Find, Ensure, Join, Part, SetNick,
// SlotOf, Leave), the errors NickInUse, NotMember, AlreadyMember, NoSuchNick
// and TooManyRooms, and the limits MaxRooms and MaxClients.
//
// How: Plain fixed-size structs: each room holds a bit per member slot, so the
// whole table lives inline behind one lock and allocates nothing.

import src.proto

/// Why a room or nick change was refused.
export error NickInUse
export error NotMember
export error AlreadyMember
export error NoSuchNick
export error TooManyRooms

/// The most rooms a server holds, and the most clients it serves.
export const MaxRooms: int = 32
export const MaxClients: int = 1024

/// A room: its name, its topic and a bit per member slot. Plain and of fixed
/// size, so the whole table lives inline behind one lock and allocates
/// nothing; members are listed in slot order.
export struct Room {
    export name: proto.Name
    topic_n: int
    topic_b: [160]u8
    members: [16]u64
    export count: int
}

/// The room's topic.
export Room.Topic(&self): str from self {
    // set only from `str` values cut on a scalar boundary
    return str.From(self.topic_b[..self.topic_n]).Must()
}

/// Whether `slot` is a member.
export Room.Has(&self, slot: int): bool {
    return self.members[slot / 64] & (Cast[u64](1) << Cast[u64](slot % 64)) != 0
}

/// The first member slot after `after`, or `none`. O(clients / 64).
export Room.NextMember(&self, after: int): ?int {
    for s in after + 1..MaxClients {
        if self.members[s / 64] == 0 {
            continue
        }
        if self.Has(s) {
            return some(s)
        }
    }
    return none
}

Room.SetTopic(mut self, text: str) {
    var n := text.Len()
    if n > 160 {
        n = 160
        for n > 0 && text.Byte(n) & 0xC0 == 0x80 {
            n -= 1
        }
    }
    for i in 0..n {
        self.topic_b[i] = text.Byte(i)
    }
    self.topic_n = n
}

/// Every room and every nick.
export struct Rooms {
    rooms: [32]Room
    used: int
    nicks: [1024]proto.Name
}

/// An empty table. O(clients); allocates nothing.
export Rooms.New(): Rooms {
    r := Room(name: proto.Name.Of(""), topic_n: 0, topic_b: [0; 160], members: [0; 16], count: 0)
    return Rooms(rooms: [r; 32], used: 0, nicks: [proto.Name.Of(""); 1024])
}

/// The index of the room called `name`. O(rooms).
export Rooms.Find(&self, name: str): ?int {
    for i in 0..self.used {
        if self.rooms[i].name.Str() == name {
            return some(i)
        }
    }
    return none
}

/// The room at `i`, which `Find` or `Ensure` returned.
export Rooms.At(&self, i: int): &Room from self {
    return &self.rooms[i]
}

/// How many rooms there are.
export Rooms.Len(&self): int {
    return self.used
}

/// Makes the room `name` with `topic` if it does not exist, and returns its
/// index. Fails with `TooManyRooms` past `MaxRooms`.
export Rooms.Ensure(mut self, name: str, topic: str): TooManyRooms!int {
    if i := self.Find(name) {
        return i
    }
    if self.used == MaxRooms {
        fail TooManyRooms
    }
    i := self.used
    self.rooms[i].name = proto.Name.Of(name)
    self.rooms[i].SetTopic(topic)
    self.used += 1
    return i
}

/// The nick of `slot`, empty when it has none.
export Rooms.Nick(&self, slot: int): str from self {
    return self.nicks[slot].Str()
}

/// Gives `slot` the nick `nick`. Fails with `NickInUse` when another slot
/// holds it. O(clients).
export Rooms.SetNick(mut self, slot: int, nick: str): NickInUse!void {
    for i, n in &self.nicks {
        if i != slot && n.Str() == nick {
            fail NickInUse
        }
    }
    self.nicks[slot] = proto.Name.Of(nick)
}

/// The slot that holds `nick`. O(clients).
export Rooms.SlotOf(&self, nick: str): NoSuchNick!int {
    for i, n in &self.nicks {
        if n.Str() == nick {
            return i
        }
    }
    fail NoSuchNick
}

/// Adds `slot` to room `i`. Fails with `AlreadyMember`.
export Rooms.Join(mut self, i: int, slot: int): AlreadyMember!void {
    if self.rooms[i].Has(slot) {
        fail AlreadyMember
    }
    self.rooms[i].members[slot / 64] |= Cast[u64](1) << Cast[u64](slot % 64)
    self.rooms[i].count += 1
}

/// Removes `slot` from room `i`. Fails with `NotMember`.
export Rooms.Part(mut self, i: int, slot: int): NotMember!void {
    if !self.rooms[i].Has(slot) {
        fail NotMember
    }
    self.rooms[i].members[slot / 64] &= ~(Cast[u64](1) << Cast[u64](slot % 64))
    self.rooms[i].count -= 1
}

/// Sets room `i`'s topic, cut to 160 bytes.
export Rooms.SetTopic(mut self, i: int, topic: str) {
    self.rooms[i].SetTopic(topic)
}

/// Removes `slot` from every room and frees its nick, when its connection
/// ends. Returns how many rooms it left. O(rooms).
export Rooms.Leave(mut self, slot: int): int {
    var left := 0
    for i in 0..self.used {
        self.Part(i, slot) else {
            continue
        }
        left += 1
    }
    self.nicks[slot] = proto.Name.Of("")
    return left
}
