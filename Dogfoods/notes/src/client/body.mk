// Why: The client sends JSON bodies and query strings that must be escaped
// correctly.
//
// What: NoteJson, PatchJson and ListPath.
//
// How: Bodies are built as json.Value trees, so the library escapes the
// strings; query values are percent-encoded byte by byte.

import std.json

/// The JSON body of a new note, built as a `json.Value` so that its strings
/// are escaped by the library. Allocates the text in `a`.
export NoteJson(a: &Arena, title: str into a, body: str into a, tags: []str into a): OutOfMemory!str from a *tags into a {
    var members := List[json.Member].New(a)
    try members.Push(json.Member(key: "title", value: json.Value.Text(s: title)))
    try members.Push(json.Member(key: "body", value: json.Value.Text(s: body)))
    if tags.Len() > 0 {
        try members.Push(json.Member(key: "tags", value: try TagArray(a, tags)))
    }
    v := json.Value.Object(members: members)
    return try Compact(a, &v)
}

/// The JSON body of a PATCH from `field=value` words: `title`, `body`, and
/// `tags` as a comma-separated list, empty for none. A word without `=` is
/// `Usage` with its index.
export PatchJson(a: &Arena into a, words: []str): Usage|OutOfMemory!str from a *words into a, *a into a {
    var members := List[json.Member].New(a)
    for i, word in words {
        eq := word.Find("=") else { fail Usage(word: i + 2) }
        key := word[..eq]
        value := word[eq + 1..]
        if key == "tags" {
            var tags := List[str].New(a)
            for tag in value.Split(",") {
                if tag.Len() > 0 {
                    try tags.Push(tag)
                }
            }
            done := tags.Done()
            try members.Push(json.Member(key: key, value: try TagArray(a, done[..])))
        } else {
            try members.Push(json.Member(key: key, value: json.Value.Text(s: value)))
        }
    }
    v := json.Value.Object(members: members)
    return try Compact(a, &v)
}

/// `/notes`, with `key=value` words as its query, each value percent-encoded.
export ListPath(a: &Arena, words: []str): Usage|OutOfMemory!str from a {
    var t := Text.New(a)
    try t.Push("/notes")
    for i, word in words {
        eq := word.Find("=") else { fail Usage(word: i + 1) }
        try t.Push(if i == 0 { "?" } else { "&" })
        try t.Push(word[..eq])
        try t.Push("=")
        try PushEscaped(mut t, word[eq + 1..])
    }
    return t.Done()
}

// v as compact JSON text in a.
Compact(a: &Arena, v: &json.Value): OutOfMemory!str from a {
    var t := Text.New(a)
    var w := t.Writer()
    // A Value built here holds only strings, so Format fails only when t cannot grow.
    v.Format(mut w) else { fail OutOfMemory }
    return t.Done()
}

// A JSON array of tag strings.
TagArray(a: &Arena, tags: []str): OutOfMemory!json.Value from a, tags *tags into a {
    var items := List[json.Value].New(a)
    for tag in tags {
        try items.Push(json.Value.Text(s: tag))
    }
    return json.Value.Array(items: items)
}

// Appends s percent-encoded: unreserved bytes as they are, the rest as %XX.
PushEscaped(t: mut Text, s: str): OutOfMemory!void {
    hex := "0123456789ABCDEF"
    for i in 0..s.Len() {
        b := s.Byte(i)
        plain := (b >= b'a' && b <= b'z') || (b >= b'A' && b <= b'Z') || (b >= b'0' && b <= b'9') || b == b'-' || b == b'_' || b == b'.' || b == b'~'
        if plain {
            try t.Push(s[i..i + 1])
        } else {
            try t.PushChar('%')
            try t.PushChar(Cast[char](hex.Byte(Cast[int](b >> 4))))
            try t.PushChar(Cast[char](hex.Byte(Cast[int](b & 15))))
        }
    }
}
