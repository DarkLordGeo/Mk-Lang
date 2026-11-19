// Why: The server and its tests share one parser for client commands.
//
// What: Command, Parse, and the errors Unknown, Usage and BadName.
//
// How: Parse splits the verb from its arguments; every text in a Command is a
// view of the line, so parsing allocates nothing.

/// A client command, parsed from one line. Texts are views of that line.
export enum Command {
    Nick(name: str)
    Join(room: str)
    Part(room: str)
    Say(room: str, text: str)
    Who(room: str)
    Rooms
    Topic(room: str, text: str)
    History(room: str, count: int)
    Motd(text: str)
    Stats
    Whoami
    Ping(tag: str)
    Kick(nick: str)
    Oper(token: str)
    Shutdown(token: str)
    Quit
}

/// Why a line is not a command; each carries the reply code it is sent with.
export error Unknown
export error Usage(code: int)
export error BadName

/// Every way `Parse` refuses a line.
export type ParseError = Unknown | Usage | BadName

/// The first word of `s` and the rest with its leading spaces removed.
Cut(s: str): (head: str, rest: str) from s {
    i := s.Find(" ") else {
        return (head: s, rest: "")
    }
    var j := i
    for j < s.Len() && s.Byte(j) == b' ' {
        j += 1
    }
    return (head: s[..i], rest: s[j..])
}

RoomArg(s: str): BadName!str from s {
    if !ValidRoom(s) {
        fail BadName
    }
    return s
}

/// Parses one line, its newline already removed. O(n); allocates nothing.
export Parse(line: str): ParseError!Command from line {
    (head: verb, rest) := Cut(line.Trim())
    match verb {
        "NICK" {
            if !ValidNick(rest) {
                fail BadName
            }
            return Command.Nick(name: rest)
        }
        "JOIN" {
            return Command.Join(room: try RoomArg(rest))
        }
        "PART" {
            return Command.Part(room: try RoomArg(rest))
        }
        "MSG" {
            (head: room, rest: text) := Cut(rest)
            if text.Len() == 0 {
                fail Usage(code: 412)
            }
            return Command.Say(room: try RoomArg(room), text: text)
        }
        "WHO" {
            return Command.Who(room: try RoomArg(rest))
        }
        "ROOMS" {
            return Command.Rooms
        }
        "TOPIC" {
            (head: room, rest: text) := Cut(rest)
            return Command.Topic(room: try RoomArg(room), text: text)
        }
        "HISTORY" {
            (head: room, rest: count) := Cut(rest)
            n := count.ParseInt() else {
                fail Usage(code: 461)
            }
            if n < 1 || n > 100 {
                fail Usage(code: 461)
            }
            return Command.History(room: try RoomArg(room), count: Cast[int](n))
        }
        "MOTD" {
            return Command.Motd(text: rest)
        }
        "STATS" {
            return Command.Stats
        }
        "WHOAMI" {
            return Command.Whoami
        }
        "PING" {
            return Command.Ping(tag: rest)
        }
        "KICK" {
            if !ValidNick(rest) {
                fail BadName
            }
            return Command.Kick(nick: rest)
        }
        "OPER" {
            return Command.Oper(token: rest)
        }
        "SHUTDOWN" {
            return Command.Shutdown(token: rest)
        }
        "QUIT" {
            return Command.Quit
        }
        _ {
            fail Unknown
        }
    }
}

test "Parse splits a message into its room and text" {
    c := Parse("MSG #general hello there").Must()
    match c {
        .Say(room, text) {
            Assert(room == "#general")
            Assert(text == "hello there")
        }
        _ {
            Assert(false)
        }
    }
}

test "Parse refuses a room without a hash and an unknown verb" {
    match Parse("JOIN general") {
        ok(_) {
            Assert(false)
        }
        err(e) {
            match e {
                BadName {}
                _ {
                    Assert(false)
                }
            }
        }
    }
    match Parse("FROB") {
        ok(_) {
            Assert(false)
        }
        err(e) {
            match e {
                Unknown {}
                _ {
                    Assert(false)
                }
            }
        }
    }
}
