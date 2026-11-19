// Why: the protocol parser decides what reaches the store.
// What: tests of Parse and ParseReply.
// How: values with spaces, key limits at and over the bound, array headers.

/// Whether parsing `line` fails with `KeyTooLong`.
TooLong(line: str, b: &Bounds): bool {
    _ = Parse(line, b) else e {
        return match e {
            KeyTooLong { true }
            _ { false }
        }
    }
    return false
}

test "Parse takes a value with spaces as the rest of the line" {
    b := Bounds(max_key: 8, max_value: 16)
    match Parse("SET k hello world", &b).Must() {
        .Set(key, value) {
            Assert(key == "k" && value == "hello world")
        }
        _ {
            Assert(false)
        }
    }
}

test "Parse refuses a key over the limit and accepts one at it" {
    b := Bounds(max_key: 2, max_value: 16)
    Assert(TooLong("GET abc", &b))
    Assert(!TooLong("GET ab", &b))
}

test "ParseReply reads an array header and refuses a bad integer" {
    match ParseReply("*3").Must() {
        .Array(n) {
            Assert(n == 3)
        }
        _ {
            Assert(false)
        }
    }
    bad := ParseReply(":x") else {
        return
    }
    _ = bad
    Assert(false)
}
