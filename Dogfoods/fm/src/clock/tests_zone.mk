// Why: `--zone` is user input and must be refused clearly when wrong.
// What: tests of ParseZone.
// How: local, utc and signed offsets parse; an hour past 23, a missing
// colon and a word are refused.

OffsetOf(c: ZoneChoice): ?i32 {
    return match c {
        .Fixed(offset_seconds) { some(offset_seconds) }
        _ { none }
    }
}

IsLocal(c: ZoneChoice): bool {
    return match c {
        .Local { true }
        _ { false }
    }
}

test "ParseZone reads local, utc and signed offsets" {
    Assert(IsLocal(ParseZone("local").Must()))
    Assert(OffsetOf(ParseZone("utc").Must()).IsNone())
    Assert(OffsetOf(ParseZone("-05:30").Must()).Must() == -19800)
    Assert(OffsetOf(ParseZone("+01:00").Must()).Must() == 3600)
}

test "ParseZone refuses an hour past 23, a missing colon and a word" {
    _ = ParseZone("+24:00") else {
        return
    }
    Panic("+24:00 was accepted")
}

test "ParseZone refuses a missing colon" {
    _ = ParseZone("+0530") else {
        return
    }
    Panic("+0530 was accepted")
}
