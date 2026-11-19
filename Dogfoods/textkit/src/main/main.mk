// Why: textkit is one program with many commands.
// What: Main, Overview and Version.
// How: the first argument names the command; the rest goes to it.

import src.convert
import src.count
import src.grep
import src.input
import src.layout
import src.normal
import src.order
import src.segment
import src.utf16

/// The release this program reports with --version.
const Version: str = "textkit 0.2.0"

const Overview: str = """
    usage: textkit <command> [options] [file...]

    Unicode text tools. Each command reads the files named, or standard input
    when none is or a file is -, and writes standard output.

    commands:
      norm       rewrite lines in a normalization form (NFC, NFD, NFKC, NFKD)
      fold       fold case for caseless comparison, or map to lower or upper case
      width      measure lines in terminal columns, or show their grapheme clusters
      columns    line up separated fields into columns by display width
      sort       sort lines by a plain, folded or numeric key
      uniq       drop repeated lines, adjacent or all
      wc         count lines, words, characters, bytes and grapheme clusters
      grep       print lines that match a regular expression
      utf16      convert between UTF-8 and UTF-16
      words      split lines into words at Unicode word boundaries
      sentences  split paragraphs into sentences at Unicode sentence boundaries
      convert    convert text between the encodings of the Encoding Standard

    textkit <command> --help describes a command's options.
    exit status: 0 done, 1 nothing matched or not in form, 2 bad command line,
    3 an input failed, 4 out of memory.
    """

/// Runs the command `sys.args[1]` names over the rest of the arguments and
/// returns its exit status.
export Main(sys: &System): int {
    var s := input.Streams(out: sys.Stdout(), err: sys.Stderr())
    if sys.args.Len() < 2 {
        s.err.Str(Overview) else {}
        return input.Usage
    }
    command := sys.args[1]
    args := sys.args[2..]
    if command == "--help" || command == "-h" || command == "help" {
        s.out.Str(Overview) else {
            return input.Failed
        }
        return input.Ok
    }
    if command == "--version" {
        Print(mut s.out, "{v}\n", (v: Version)) else {
            return input.Failed
        }
        return input.Ok
    }
    var stdin := sys.Stdin()
    c := input.Ctx(arena: sys.arena, heap: sys.heap, io: sys.io)
    return match command {
        "norm" { normal.Norm(&c, mut s, mut stdin, args) }
        "fold" { normal.Fold(&c, mut s, mut stdin, args) }
        "width" { layout.Width(&c, mut s, mut stdin, args) }
        "columns" { layout.Columns(&c, mut s, mut stdin, args) }
        "sort" { order.Sort(&c, mut s, mut stdin, args) }
        "uniq" { order.Uniq(&c, mut s, mut stdin, args) }
        "wc" { count.Wc(&c, mut s, mut stdin, args) }
        "grep" { grep.Grep(&c, mut s, mut stdin, args) }
        "utf16" { utf16.Utf16(&c, mut s, mut stdin, args) }
        "words" { segment.Words(&c, mut s, mut stdin, args) }
        "sentences" { segment.Sentences(&c, mut s, mut stdin, args) }
        "convert" { convert.Convert(&c, mut s, mut stdin, args) }
        _ {
            Print(mut s.err, "textkit: unknown command {command}; textkit --help lists them\n", (command)) else {}
            input.Usage
        }
    }
}
