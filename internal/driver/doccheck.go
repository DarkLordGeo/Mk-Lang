// Why: mk doc --check compiles the examples in a package's /// text; one
// malformed block must not hide the others, and a block that declares a
// name the test prelude also declares must still compile.
//
// What: DocBlockSources (one document per fenced mk block of a file's ///
// text) and DocPreludeFor (the test prelude for one block).
//
// How: each block's document keeps the file's length with every other line
// blank, so the corpus extractor reads one block at the file's line
// numbers. DocPreludeFor drops each column-0 prelude declaration whose name
// the block declares itself; a method head (P.Sum) declares no P.

package driver

import (
	"regexp"
	"strings"
)

// DocBlockSources is, for each fenced mk block of the /// text of a
// file (DocBlocksSource), a document of the file's length holding only
// that block, from its opening fence to its closing one (or the end), each
// other line blank: corpus.Extract reads one block from each, at the
// file's line numbers, and an error in one stops none of the others.
func DocBlockSources(text string) []string {
	lines := strings.Split(DocBlocksSource(text), "\n")
	var out []string
	for i := 0; i < len(lines); i++ {
		if strings.TrimRight(lines[i], " \t") != "```mk" {
			continue
		}
		end := len(lines) - 1
		for j := i + 1; j < len(lines); j++ {
			if strings.TrimRight(lines[j], " \t") == "```" {
				end = j
				break
			}
		}
		doc := make([]string, len(lines))
		copy(doc[i:end+1], lines[i:end+1])
		out = append(out, strings.Join(doc, "\n"))
		i = end
	}
	return out
}

// preludeDeclName is the name a column-0 declaration line declares:
// `struct P`, `linear struct R`, `enum E`, `F(`, `F[`, and for a method
// `R.Close(` its type R.
var preludeDeclName = regexp.MustCompile(`^(?:export\s+)?(?:(?:linear\s+|view\s+|open\s+)*(?:struct|enum|interface|error|type|const|var)\s+)?([A-Za-z_][A-Za-z0-9_]*)`)

// docMethodHead is a method's head, Type.Name( or Type.Name[.
var docMethodHead = regexp.MustCompile(`^(?:export\s+)?[A-Za-z_][A-Za-z0-9_]*\.[A-Za-z_][A-Za-z0-9_]*\s*[(\[]`)

// DocPreludeFor is the test prelude for one doc block: prelude without
// each top-level declaration whose name the block declares at column 0
// itself (a doc example that declares Use, Point or Take keeps its own),
// so the block never collides with the prelude. A declaration runs from
// its column-0 line to the next one; a method R.M goes with R.
func DocPreludeFor(prelude, block string) string {
	own := map[string]bool{}
	for _, l := range strings.Split(block, "\n") {
		if l == "" || l[0] == ' ' || l[0] == '\t' || l[0] == '}' || strings.HasPrefix(l, "//") || strings.HasPrefix(l, "import ") {
			continue
		}
		// A method head, P.Sum(, adds to P and declares no P of its own:
		// the prelude's Point stays.
		if docMethodHead.MatchString(l) {
			continue
		}
		if m := preludeDeclName.FindStringSubmatch(l); m != nil {
			own[m[1]] = true
		}
	}
	if len(own) == 0 {
		return prelude
	}
	var out []string
	drop := false
	for _, l := range strings.Split(prelude, "\n") {
		if l != "" && l[0] != ' ' && l[0] != '\t' && l[0] != '}' {
			drop = false
			if m := preludeDeclName.FindStringSubmatch(l); m != nil && own[m[1]] {
				drop = true
			}
		}
		if !drop {
			out = append(out, l)
		} else if strings.HasPrefix(l, "}") {
			drop = false // the dropped declaration's closing brace
		}
	}
	return strings.Join(out, "\n")
}
