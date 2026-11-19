// Why: A PATCH body and an import entry are loose JSON objects that must be
// checked field by field.
//
// What: ApplyPatch, FromEntry, and the errors NotObject, UnknownField and
// WrongType.
//
// How: Reads the json.Value directly: each known member present replaces that
// field, `"tags": null` clears the tags, and any other member is an error
// naming its position.

import std.json

export error NotObject
export error UnknownField(index: int)
export error WrongType(index: int)

/// Why a PATCH body or an import entry could not be read. The index is the
/// member's position in its object.
export type ShapeError = NotObject | UnknownField | WrongType | OutOfMemory

/// Merges a PATCH body into `cur`: each of `title`, `body` and `tags` present
/// in the object replaces that field, and `"tags": null` clears the tags.
/// Fails with `NotObject` unless `v` is an object, `UnknownField` on any other
/// member, and `WrongType` on a member of the wrong JSON type. O(n) in the
/// body; allocates the new tag list in `a`.
export ApplyPatch(a: &Arena, v: &json.Value into v, cur: &Note): ShapeError!NoteIn from a, v, cur {
    try CheckMembers(v)
    var title := cur.title
    var body := cur.body
    var tags: ?[]str = some(cur.tags)
    if t := v.Get("title") {
        title = t.Str() else { fail WrongType(index: try IndexOf(v, "title")) }
    }
    if b := v.Get("body") {
        body = b.Str() else { fail WrongType(index: try IndexOf(v, "body")) }
    }
    if t := v.Get("tags") {
        tags = try TagsOf(a, t, try IndexOf(v, "tags"))
    }
    return NoteIn(title: title, body: body, tags: tags)
}

/// Reads one import entry: an object with `title`, `body` and optional
/// `tags`, as a PATCH body is read, and an optional numeric `id` that is
/// ignored, since the store numbers notes itself. Allocates the tag list in `a`.
export FromEntry(a: &Arena, v: &json.Value into v): ShapeError!NoteIn from a, v {
    try CheckMembers(v)
    t := v.Get("title") else { fail WrongType(index: -1) }
    title := t.Str() else { fail WrongType(index: try IndexOf(v, "title")) }
    var body := ""
    if b := v.Get("body") {
        body = b.Str() else { fail WrongType(index: try IndexOf(v, "body")) }
    }
    if id := v.Get("id") {
        _ = id.Int() else { fail WrongType(index: try IndexOf(v, "id")) }
    }
    var tags: ?[]str = none
    if g := v.Get("tags") {
        tags = try TagsOf(a, g, try IndexOf(v, "tags"))
    }
    return NoteIn(title: title, body: body, tags: tags)
}

// Fails unless v is an object whose members are all known fields.
CheckMembers(v: &json.Value): ShapeError!void {
    match v {
        .Object(members) {
            for i, m in &members {
                known := m.key == "title" || m.key == "body" || m.key == "tags" || m.key == "id"
                if !known {
                    fail UnknownField(index: i)
                }
            }
        }
        .Null | .Bool | .Number | .Text | .Array {
            fail NotObject
        }
    }
}

// The position of the last member named key: Get takes the last of duplicates.
IndexOf(v: &json.Value, key: str): ShapeError!int {
    var found := -1
    match v {
        .Object(members) {
            for i, m in &members {
                if m.key == key {
                    found = i
                }
            }
        }
        .Null | .Bool | .Number | .Text | .Array {
            fail NotObject
        }
    }
    return found
}

// Reads a tags member: an array of strings, or null for no tags.
TagsOf(a: &Arena, v: &json.Value into v, index: int): ShapeError!?[]str from a, v {
    match v {
        .Null {
            return none
        }
        .Array(items) {
            var out := List[str].New(a)
            for item in &items {
                s := item.Str() else { fail WrongType(index: index) }
                // a copy: the value lives in the handler's scratch arena
                try out.Push(try a.Dup(s))
            }
            done := out.Done()
            return some(done[..])
        }
        .Bool | .Number | .Text | .Object {
            fail WrongType(index: index)
        }
    }
}
