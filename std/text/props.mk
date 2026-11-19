// Why: parsers and regular expressions (a JavaScript engine's identifiers,
// \p{..} classes) ask a scalar's Unicode properties one at a time, and
// each answer must cost a few loads.
//
// What: CategoryOf, ScriptOf, ScriptExtensions, Has and UnicodeVersion: a
// scalar's General_Category, Script, Script_Extensions and ECMA-262's
// binary properties, from the UCD snapshot every table of std reads
// (props_tables.mk names it).
//
// How: one three-stage trie maps a scalar to a record of all four (67,331
// bytes of tables, TestStd_PropsTableBytes), so each function is three
// table loads and a field load; nothing allocates, and ScriptExtensions
// returns a slice of static data. std.regex keeps its own name-to-ranges
// tables (tables.mk), from the same files at the same version
// (TestStd_PropsShareRegexSnapshot), since its compiler needs ranges.
// The functions are CategoryOf and ScriptOf, not Category and Script: a
// function and an enum of one name in one module are E0206, and the enums
// keep the type names.

// UcpRec is c's record number.
UcpRec(c: char): int {
    x := u32(c)
    i1 := &UcpIndex1
    i2 := &UcpIndex2
    data := &UcpData
    b2 := u32(i1[x >> (UcpShift2 + UcpShift3)])
    b3 := u32(i2[(b2 << UcpShift2) | ((x >> UcpShift3) & ((u32(1) << UcpShift2) - 1))])
    return Cast[int](data[(b3 << UcpShift3) | (x & ((u32(1) << UcpShift3) - 1))])
}

/// The General_Category of c, such as UppercaseLetter or DecimalNumber;
/// Unassigned for a scalar Unicode does not assign. O(1); allocates
/// nothing.
export CategoryOf(c: char): Category {
    cats := &UcpRecCat
    return UcpCats[Cast[int](cats[UcpRec(c)])]
}

/// The Script of c, such as Latin or Greek; Common for punctuation and
/// symbols that many scripts share, and Unknown for a scalar no script
/// lists. O(1); allocates nothing.
export ScriptOf(c: char): Script {
    scs := &UcpRecScript
    return UcpScripts[Cast[int](scs[UcpRec(c)])]
}

/// The scripts c is used with (its Script_Extensions, UAX 24), ordered by
/// the scripts' codes, or ScriptOf(c) alone when Unicode lists none for c.
/// Never empty. The slice is static data. O(1); allocates nothing.
export ScriptExtensions(c: char): []Script {
    scx := &UcpRecScx
    starts := &UcpScxStart
    lens := &UcpScxLen
    i := Cast[int](scx[UcpRec(c)])
    s := Cast[int](starts[i])
    return UcpScxPool[s..s + Cast[int](lens[i])]
}

/// Whether c has the binary property p, as the Unicode Character Database
/// states it. Any, ASCII and Assigned follow Unicode regular expressions
/// (UTS 18). O(1); allocates nothing.
export Has(c: char, p: Property): bool {
    props := &UcpRecProps
    return (props[UcpRec(c)] >> UcpBit(p)) & 1 != 0
}

/// The version of Unicode that std.text's data comes from, such as
/// "18.0.0". Every Unicode table of std, str's case mapping and regex's
/// classes included, comes from this one version. O(1); allocates
/// nothing.
export UnicodeVersion(): str {
    return UcpVersion
}
