// Why: The server, the client and the store share one definition of a note.
//
// What: NoteIn, Note, NoteList, Stats and Problem, their JSON codecs, the
// limits MaxTitle, MaxBody, MaxTags and MaxTag, and Validate.
//
// How: Each type gets its codec from `gen json.Codec`, so encoding and decoding
// are generated, not written by hand.

import std.json

/// The longest title a note may have, in bytes.
export const MaxTitle: int = 200

/// The longest body a note may have, in bytes.
export const MaxBody: int = 65536

/// The most tags a note may carry.
export const MaxTags: int = 16

/// The longest tag, in bytes.
export const MaxTag: int = 32

/// A note as a client sends it in a POST or PUT body. `tags` may be left out.
export struct NoteIn {
    export title: str
    export body: str
    export tags: ?[]str
}

gen json.Codec(NoteIn)

/// A stored note. `rev` starts at 1 and grows by one with every change.
export struct Note {
    export id: i64
    export title: str
    export body: str
    export tags: []str
    export rev: i64
}

gen json.Codec(Note)

/// One page of notes and the number of notes the query matched in all.
export struct NoteList {
    export notes: []Note
    export total: int
}

gen json.Codec(NoteList)

/// Counts over the whole store.
export struct Stats {
    export notes: int
    export tags: int
    export words: int
    export avg_words: f64
}

gen json.Codec(Stats)

/// The body of every error response: a message, and where it applies a byte
/// offset into the request body or the index of the offending field.
export struct Problem {
    export message: str
    export offset: ?int
}

gen json.Codec(Problem)

export error EmptyTitle
export error TitleTooLong(len: int)
export error BodyTooLong(len: int)
export error TooManyTags(count: int)
export error BadTag(index: int)

/// Why a note was refused before it reached the store.
export type Error = EmptyTitle | TitleTooLong | BodyTooLong | TooManyTags | BadTag

/// Checks a note against the limits above. A tag is 1 to 32 bytes of `a` to
/// `z`, `0` to `9` and `-`. O(n) in the note's size; allocates nothing.
export Validate(n: &NoteIn): Error!void {
    if n.title.Trim().Len() == 0 {
        fail EmptyTitle
    }
    if n.title.Len() > MaxTitle {
        fail TitleTooLong(len: n.title.Len())
    }
    if n.body.Len() > MaxBody {
        fail BodyTooLong(len: n.body.Len())
    }
    tags := n.tags else { return }
    if tags.Len() > MaxTags {
        fail TooManyTags(count: tags.Len())
    }
    for i, tag in tags {
        if !IsTag(tag) {
            fail BadTag(index: i)
        }
    }
}

/// Reports whether `s` is a well-formed tag. O(n); allocates nothing.
export IsTag(s: str): bool {
    if s.Len() == 0 || s.Len() > MaxTag {
        return false
    }
    for i in 0..s.Len() {
        b := s.Byte(i)
        ok := (b >= b'a' && b <= b'z') || (b >= b'0' && b <= b'9') || b == b'-'
        if !ok {
            return false
        }
    }
    return true
}

/// A sentence that explains `e` to the client. Allocates nothing.
export Explain(e: Error): str {
    return match e {
        EmptyTitle { "title is empty" }
        TitleTooLong { "title is longer than 200 bytes" }
        BodyTooLong { "body is longer than 65536 bytes" }
        TooManyTags { "more than 16 tags" }
        BadTag { "a tag is not 1 to 32 bytes of a-z, 0-9 and -" }
    }
}

/// The index `e` points at, for the problem body: the tag's index, else none.
export Where(e: Error): ?int {
    return match e {
        BadTag(index) { some(index) }
        EmptyTitle | TitleTooLong | BodyTooLong | TooManyTags { none }
    }
}
