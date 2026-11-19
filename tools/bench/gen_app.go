// Why: the app table's rows (tests/bench/README.md "Applications") need
// larger, more varied inputs than the kernel rows, still the same for every
// language.
//
// What: the gen kinds "tree" (a directory of source-like files for a
// recursive search or an archive), "jsonl" (an NDJSON event log), "mixed"
// (text, structured binary, random bytes and runs, for a compressor) and
// "zlib" (another kind compressed by Go's compress/zlib): genTree, genJSONL,
// genMixed and genZlib.
//
// How: every byte comes from splitmix(seed), so each kind's output is a
// function of its spec.

package main

import (
	"bufio"
	"compress/zlib"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
)

// treeKeywords are the words a tree's text lines carry now and then, so
// that a search for an alternation such as `n[aeiou]+dle|omega|s?igma`,
// case-folded or not, finds lines in many files.
var treeKeywords = []string{"needle", "noodle", "naadle", "omega", "OMEGA", "sigma", "Sigma", "igma", "Needle"}

// treeUnicode are the non-ASCII lines a tree's text holds now and then
// (UTF-8), for a case-insensitive search over more than ASCII.
var treeUnicode = []string{"café Ωmega σίγμα ΣΊΓΜΑ", "naïve façade Ärger ß", "日本語のテキスト needle"}

// genTree writes the "tree" kind into the directory root: files of about
// spec.Bytes in total, in nested directories (at most 6 deep), drawn from
// splitmix(seed) in this order for each file: its directory (an existing
// one, or with one chance in four a new child of one), whether the new
// directory is hidden (a name starting with ".", one in 32), the file's
// name (a word, its index and one of eight extensions that no tool of the
// repository reads as source; one in 32 hidden, one in 8 starting with a
// capital letter, so that bytewise order differs from a case-folded one),
// and its kind: one in 64 binary (random bytes
// with NULs), one in 128 large text (64 KiB to 256 KiB), one in 64 empty,
// and otherwise text of 128 bytes to 8 KiB. Text is lines of words, log
// lines and code-like lines, a keyword in one line of 16 and a UTF-8 line
// in one of 64; one file in 16 ends its lines with CR LF and one in 8 has
// no final newline.
func genTree(root string, spec dataSpec) error {
	g := splitmix{spec.Seed}
	vocab := make([]string, 4096)
	for i := range vocab {
		l := int(g.next()%10) + 1
		b := make([]byte, l)
		for j := range b {
			b[j] = 'a' + byte(g.next()%26)
		}
		vocab[i] = string(b)
	}
	// No extension any tool of the repository reads as source (a tree under
	// tests/bench/data would otherwise reach go build ./... and gofmt).
	exts := []string{".txt", ".log", ".json", ".cfg", ".src", ".dat", ".csv", ".ini"}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}
	dirs := []string{""}
	seen := map[string]bool{"": true}
	var total int64
	for i := 0; total < spec.Bytes; i++ {
		dir := dirs[g.next()%uint64(len(dirs))]
		if g.next()%4 == 0 && strings.Count(dir, "/") < 5 {
			name := vocab[g.next()%uint64(len(vocab))] + strconv.Itoa(i)
			if g.next()%32 == 0 {
				name = "." + name
			}
			child := path.Join(dir, name)
			if !seen[child] {
				if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(child)), 0o755); err != nil {
					return err
				}
				seen[child] = true
				dirs = append(dirs, child)
			}
			dir = child
		}
		name := vocab[g.next()%uint64(len(vocab))] + "_" + strconv.Itoa(i) + exts[g.next()%uint64(len(exts))]
		switch {
		case g.next()%32 == 0:
			name = "." + name
		case g.next()%8 == 0:
			name = strings.ToUpper(name[:1]) + name[1:]
		}
		var body []byte
		binary := false
		switch k := g.next() % 128; {
		case k < 2:
			binary = true
			body = make([]byte, 1024+g.next()%7168)
			for j := range body {
				body[j] = byte(g.next())
			}
			body[g.next()%uint64(min(len(body), 1024))] = 0
		case k < 3:
			body = treeText(&g, vocab, 64<<10+int(g.next()%(192<<10)))
		case k < 5:
		default:
			body = treeText(&g, vocab, 128+int(g.next()%(8<<10)))
		}
		if len(body) > 0 && !binary && g.next()%16 == 0 {
			body = []byte(strings.ReplaceAll(string(body), "\n", "\r\n"))
		}
		if len(body) > 0 && !binary && g.next()%8 == 0 {
			body = []byte(strings.TrimRight(string(body), "\r\n"))
		}
		if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(path.Join(dir, name))), body, 0o644); err != nil {
			return err
		}
		total += int64(len(body))
	}
	return nil
}

// treeText is about size bytes of a tree file's text (genTree).
func treeText(g *splitmix, vocab []string, size int) []byte {
	var b strings.Builder
	levels := []string{"INFO", "WARN", "ERROR", "DEBUG"}
	for b.Len() < size {
		switch g.next() % 4 {
		case 0:
			ms := g.next() % 86400000
			fmt.Fprintf(&b, "2026-11-07T%02d:%02d:%02d.%03dZ %s req=%d %s", ms/3600000, ms/60000%60, ms/1000%60, ms%1000, levels[g.next()%4], g.next()%1000000, vocab[g.next()%uint64(len(vocab))])
		case 1:
			fmt.Fprintf(&b, "func %s(%s, %s int) int { return %s + %d }", vocab[g.next()%uint64(len(vocab))], vocab[g.next()%uint64(len(vocab))], vocab[g.next()%uint64(len(vocab))], vocab[g.next()%uint64(len(vocab))], g.next()%1000)
		default:
			for j := range int(g.next()%12) + 3 {
				if j > 0 {
					b.WriteByte(' ')
				}
				b.WriteString(vocab[g.next()%uint64(len(vocab))])
			}
		}
		if g.next()%16 == 0 {
			b.WriteString(" " + treeKeywords[g.next()%uint64(len(treeKeywords))])
		}
		if g.next()%64 == 0 {
			b.WriteString(" " + treeUnicode[g.next()%uint64(len(treeUnicode))])
		}
		b.WriteByte('\n')
	}
	return []byte(b.String())
}

// jsonlEvents and jsonlEscapes are the "jsonl" kind's event names and the
// escapes its msg strings carry.
var (
	jsonlEvents  = []string{"login", "logout", "view", "click", "search", "buy", "refund", "error"}
	jsonlEscapes = []string{`\"`, `\\`, `\n`, `\t`, `\/`, `\u00e9`, `\u03a9`, `\ud83d\ude00`}
)

// genJSONL writes the "jsonl" kind: one JSON object a line until about
// spec.Bytes bytes, every line
//
//	{"id":<n>,"user":"u<k>","event":"<name>","status":<code>,"bytes":<n>,"ms":<n>,"delta":<signed n>,"ok":true|false,"tags":["t<k>",...],"msg":"<text>"}
//
// with no white space, the members in this order, and every field drawn
// from splitmix(seed) in this order: id (a running count from 1), user
// (u0 to u4095, skewed toward the low ones as genWords' ranks are), event
// (one of eight), status (one of seven codes), bytes (below 10^9), ms
// (below 60,000), delta (-10^6 to 10^6), ok, 0 to 4 tags (t0 to t63), and
// msg, 1 to 12 words of lowercase letters, one in four with one of eight
// escapes (\", \\, \n, \t, \/, \u00e9, \u03a9 and a surrogate pair). There
// are integers only, no floats, the file is ASCII, and no string holds <, >
// or &, so no two libraries need to write a value differently.
func genJSONL(w *bufio.Writer, spec dataSpec) error {
	g := splitmix{spec.Seed}
	codes := []int{200, 201, 204, 400, 404, 500, 503}
	var n int64
	for id := 1; n < spec.Bytes; id++ {
		var b strings.Builder
		a, c := g.next()%4096, g.next()%4096
		fmt.Fprintf(&b, `{"id":%d,"user":"u%d","event":"%s","status":%d,"bytes":%d,"ms":%d,"delta":%d,"ok":%t,"tags":[`,
			id, a*c/4096, jsonlEvents[g.next()%8], codes[g.next()%7], g.next()%1000000000, g.next()%60000,
			int64(g.next()%2000001)-1000000, g.next()&1 == 1)
		for t := range int(g.next() % 5) {
			if t > 0 {
				b.WriteByte(',')
			}
			fmt.Fprintf(&b, `"t%d"`, g.next()%64)
		}
		b.WriteString(`],"msg":"`)
		for j := range int(g.next()%12) + 1 {
			if j > 0 {
				b.WriteByte(' ')
			}
			l := int(g.next()%8) + 1
			for range l {
				b.WriteByte('a' + byte(g.next()%26))
			}
		}
		if g.next()%4 == 0 {
			b.WriteString(jsonlEscapes[g.next()%uint64(len(jsonlEscapes))])
		}
		b.WriteString("\"}\n")
		w.WriteString(b.String())
		n += int64(b.Len())
	}
	return nil
}

// genMixed writes the "mixed" kind: spec.Bytes bytes exactly, in blocks of
// 64 KiB (the last one shorter), each of a kind drawn from splitmix(seed):
// text (lowercase words, spaces and newlines), structured binary (little-
// endian 32-bit fields of a slowly growing counter with small noise, and
// runs of zeros, as in an executable's sections), random bytes, or runs of
// one byte value (1 to 4,096 long). A compressor meets stored, Huffman-only
// and match-heavy blocks in one file.
func genMixed(w *bufio.Writer, spec dataSpec) error {
	g := splitmix{spec.Seed}
	const block = 64 << 10
	buf := make([]byte, 0, block)
	var counter uint32
	for left := spec.Bytes; left > 0; {
		size := min(left, block)
		buf = buf[:0]
		switch g.next() % 4 {
		case 0:
			for int64(len(buf)) < size {
				for range int(g.next()%10) + 1 {
					buf = append(buf, 'a'+byte(g.next()%26))
				}
				if g.next()%12 == 0 {
					buf = append(buf, '\n')
				} else {
					buf = append(buf, ' ')
				}
			}
		case 1:
			for int64(len(buf)) < size {
				if g.next()%16 == 0 {
					buf = append(buf, make([]byte, int(g.next()%512))...)
					continue
				}
				counter += uint32(g.next() % 64)
				v := counter ^ uint32(g.next()%4)
				buf = append(buf, byte(v), byte(v>>8), byte(v>>16), byte(v>>24))
			}
		case 2:
			for int64(len(buf)) < size {
				v := g.next()
				for k := range 8 {
					buf = append(buf, byte(v>>(8*k)))
				}
			}
		default:
			for int64(len(buf)) < size {
				c, l := byte(g.next()), int(g.next()%4096)+1
				for range l {
					buf = append(buf, c)
				}
			}
		}
		if _, err := w.Write(buf[:size]); err != nil {
			return err
		}
		left -= size
	}
	return nil
}

// genZlib writes the "zlib" kind: the kind spec.Of names ("words" when
// absent; also "mixed", "log", "json" or "jsonl"), with spec's bytes and
// seed, compressed by Go's compress/zlib at level 6 (zlib's default). A
// row's check is over the decompressed bytes, so another Go release's
// compressor, which may emit other bytes, changes nothing.
func genZlib(w *bufio.Writer, spec dataSpec) error {
	zw, err := zlib.NewWriterLevel(w, 6)
	if err != nil {
		return err
	}
	bw := bufio.NewWriterSize(zw, 1<<20)
	inner := spec
	inner.Kind, inner.Of = spec.Of, ""
	switch spec.Of {
	case "", "words":
		err = genWords(bw, inner)
	case "mixed":
		err = genMixed(bw, inner)
	case "log":
		err = genLog(bw, inner)
	case "json":
		err = genJSON(bw, inner)
	case "jsonl":
		err = genJSONL(bw, inner)
	default:
		err = fmt.Errorf("zlib of %q: not a file kind", spec.Of)
	}
	if err == nil {
		err = bw.Flush()
	}
	if cerr := zw.Close(); err == nil {
		err = cerr
	}
	return err
}
