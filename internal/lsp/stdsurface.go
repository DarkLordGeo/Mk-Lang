// Why: the reference's library chapter states rules that no single doc comment
// carries; a std hover shows them below the signature.
//
// What: surfaceDoc, surfaceFor and surfaceMarkdown.
//
// How: looks a query subject up in stdsurface_table*.go, which TestKeywordTable
// generates from the reference's library chapter.

package lsp

import "strings"

// surfaceDoc is one std item of the reference's library chapter: its module (""
// for the prelude), its lines there, and the sentences of the section's
// prose that speak of it.
type surfaceDoc struct {
	Module string
	Lines  []string
	Notes  []string
}

// surfaceFor finds a query subject's library entry: the subject as it
// is (`str.Len`, `fs.ReadText`, `fs.File.Read`), then without a leading
// module binding (`File.Read`).
func surfaceFor(subject string) (surfaceDoc, bool) {
	if d, ok := surfaceDocs[subject]; ok {
		return d, true
	}
	if _, rest, ok := strings.Cut(subject, "."); ok && strings.Contains(rest, ".") {
		if d, ok := surfaceDocs[rest]; ok {
			return d, true
		}
	}
	return surfaceDoc{}, false
}

// surfaceMarkdown renders an entry for a hover, below the signature.
func surfaceMarkdown(d surfaceDoc) string {
	var b strings.Builder
	b.WriteString("**" + surfaceSection)
	if d.Module != "" {
		b.WriteString(", `" + d.Module + "`")
	}
	b.WriteString("**\n```mk\n" + strings.Join(d.Lines, "\n") + "\n```")
	if len(d.Notes) > 0 {
		b.WriteString("\n" + strings.Join(d.Notes, " "))
	}
	return b.String()
}
