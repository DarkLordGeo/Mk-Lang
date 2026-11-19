// Why: every language of a row must see the same input, so all inputs come
// from one generator spec that each baseline can reproduce.
//
// What: splitmix, the generator of every row, and the data files `tools/bench
// gen` writes under tests/bench/data for rows that read a file: genData with
// the json, words, log and gzip kinds, plus plainName and removeDataDir.
//
// How: splitmix64 (Steele, Lea and Flood, "Fast splittable pseudorandom
// number generators", OOPSLA 2014; Vigna's splitmix64.c), which every
// baseline also implements in its own language. A <file>.spec stamp beside
// each file holds its spec, so a changed spec regenerates the file.

package main

import (
	"bufio"
	"compress/gzip"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// splitmix is the generator of every row: state x, and each call adds the
// golden-ratio increment and mixes. The output for seed 0 starts
// 0xe220a8397b1dcdaf (TestSplitmixReference).
type splitmix struct{ x uint64 }

func (s *splitmix) next() uint64 {
	s.x += 0x9e3779b97f4a7c15
	z := s.x
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
	z = (z ^ (z >> 27)) * 0x94d049bb133111eb
	return z ^ (z >> 31)
}

// dataSpec names a generated input file of a row (bench.json "data").
type dataSpec struct {
	Kind  string `json:"kind"`  // the generator, e.g. "json"
	File  string `json:"file"`  // the file name under tests/bench/data (a directory for "tree")
	Bytes int64  `json:"bytes"` // the approximate size
	Seed  uint64 `json:"seed"`
	// Of is the kind the "zlib" kind compresses ("words" when absent).
	Of string `json:"of,omitempty"`
}

// genData writes spec's file into dir unless a file of that name already
// exists with a matching stamp, and returns its path. The stamp file
// <file>.spec holds the spec, so a changed spec regenerates; it is written
// last, so a generator killed midway leaves no stamp and runs again.
func genData(dir string, spec dataSpec) (string, error) {
	if !plainName(spec.File) {
		return "", fmt.Errorf("data file %q must be a plain name", spec.File)
	}
	path := filepath.Join(dir, spec.File)
	stamp := fmt.Sprintf("%s %d %d\n", spec.Kind, spec.Bytes, spec.Seed)
	if spec.Of != "" {
		stamp = fmt.Sprintf("%s %d %d of=%s\n", spec.Kind, spec.Bytes, spec.Seed, spec.Of)
	}
	if b, err := os.ReadFile(path + ".spec"); err == nil && string(b) == stamp {
		if _, err := os.Stat(path); err == nil {
			return path, nil
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	os.Remove(path + ".spec")
	if spec.Kind == "tree" {
		// A directory: the old one (generated data only) goes first.
		if err := removeDataDir(dir, path); err != nil {
			return "", err
		}
		if err := genTree(path, spec); err != nil {
			return "", err
		}
		return path, os.WriteFile(path+".spec", []byte(stamp), 0o644)
	}
	if fi, err := os.Stat(path); err == nil && fi.IsDir() {
		if err := removeDataDir(dir, path); err != nil {
			return "", err
		}
	}
	f, err := os.Create(path)
	if err != nil {
		return "", err
	}
	w := bufio.NewWriterSize(f, 1<<20)
	switch spec.Kind {
	case "json":
		err = genJSON(w, spec)
	case "words":
		err = genWords(w, spec)
	case "log":
		err = genLog(w, spec)
	case "gzip":
		err = genGzip(w, spec)
	case "jsonl":
		err = genJSONL(w, spec)
	case "mixed":
		err = genMixed(w, spec)
	case "zlib":
		err = genZlib(w, spec)
	case "orderfeed":
		err = genOrderfeed(w, spec)
	default:
		err = fmt.Errorf("unknown data kind %q", spec.Kind)
	}
	if err == nil {
		err = w.Flush()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(path)
		return "", err
	}
	return path, os.WriteFile(path+".spec", []byte(stamp), 0o644)
}

// plainName reports whether name is one path element that stays inside its
// directory: not empty, not "." or "..", no separator of either form, and
// local by filepath.IsLocal (so no volume and no reserved device name).
func plainName(name string) bool {
	return name != "" && name != "." && name != ".." &&
		!strings.ContainsAny(name, `/\`) && filepath.IsLocal(name) &&
		filepath.Base(name) == name
}

// removeDataDir removes the generated directory path, which must be exactly
// one element below dir (a name of ".." or "."
// would otherwise remove tests/bench or every row's data).
func removeDataDir(dir, path string) error {
	rel, err := filepath.Rel(dir, path)
	if err != nil || !plainName(rel) {
		return fmt.Errorf("refusing to remove %s: not one element below %s", path, dir)
	}
	return os.RemoveAll(path)
}

// genJSON writes one JSON array of records until about spec.Bytes bytes. Each
// record is {"id":<int>,"name":"<ascii>","score":<int>.<3 digits>,"tags":[...],"ok":true|false,"next":null|{...}}
// drawn from splitmix(seed); strings use only printable ASCII with \" and
// \\ escapes, so every tokenizer reads the same tokens.
func genJSON(w *bufio.Writer, spec dataSpec) error {
	g := splitmix{spec.Seed}
	var n int64
	put := func(s string) {
		w.WriteString(s)
		n += int64(len(s))
	}
	str := func() {
		l := int(g.next()%24) + 1
		b := make([]byte, 0, l+4)
		b = append(b, '"')
		for range l {
			c := byte(g.next()%95) + 32
			if c == '"' || c == '\\' {
				b = append(b, '\\')
			}
			b = append(b, c)
		}
		b = append(b, '"')
		put(string(b))
	}
	var record func(depth int)
	record = func(depth int) {
		put(`{"id":`)
		put(strconv.FormatUint(g.next()%1000000007, 10))
		put(`,"name":`)
		str()
		put(`,"score":`)
		v := g.next()
		if v&1 == 1 {
			put("-")
		}
		put(strconv.FormatUint((v>>1)%100000, 10) + "." + fmt.Sprintf("%03d", (v>>20)%1000))
		put(`,"tags":[`)
		for i := range int(g.next() % 5) {
			if i > 0 {
				put(",")
			}
			str()
		}
		put(`],"ok":`)
		if g.next()&1 == 1 {
			put("true")
		} else {
			put("false")
		}
		put(`,"next":`)
		if depth < 3 && g.next()%4 == 0 {
			record(depth + 1)
		} else {
			put("null")
		}
		put("}")
	}
	put("[")
	for first := true; n < spec.Bytes; first = false {
		if !first {
			put(",\n")
		}
		record(0)
	}
	put("]\n")
	return nil
}

// wordsVocab is the vocabulary size of the "words" kind.
const wordsVocab = 1 << 16

// genWords writes the text of the idiomatic wordfreq row: a vocabulary of
// 65,536 words, each 1 to 12 lowercase ASCII letters (length 1 + next()%12,
// letter 'a' + next()%26), drawn first from splitmix(seed); then words until
// about spec.Bytes bytes, each word vocab[(a%V)*(b%V)/V] for two fresh draws
// a and b (a skew toward the low ranks, density about ln(V/r)/V), followed
// by '\n' when next()%12 == 0 and ' ' otherwise. The file ends in '\n'.
// Words may repeat in the vocabulary; every language counts the text, not
// the vocabulary, so that changes nothing.
func genWords(w *bufio.Writer, spec dataSpec) error {
	g := splitmix{spec.Seed}
	vocab := make([][]byte, wordsVocab)
	for i := range vocab {
		l := int(g.next()%12) + 1
		b := make([]byte, l)
		for j := range b {
			b[j] = 'a' + byte(g.next()%26)
		}
		vocab[i] = b
	}
	var n int64
	for n < spec.Bytes {
		a, b := g.next()%wordsVocab, g.next()%wordsVocab
		word := vocab[a*b/wordsVocab]
		w.Write(word)
		sep := byte(' ')
		if g.next()%12 == 0 {
			sep = '\n'
		}
		w.WriteByte(sep)
		n += int64(len(word)) + 1
	}
	return w.WriteByte('\n')
}

// genLog writes the log of the idiomatic regex row: lines of the form
//
//	2026-11-07T03:14:15.926Z INFO svc=api req=123456 path=/items/abc/42 status=200 dur=17ms
//
// until about spec.Bytes bytes, every field drawn from splitmix(seed) in
// this order: the time of day (next() % 86400000 milliseconds), the level
// (INFO, WARN, ERROR or DEBUG), the service (one of five), the request id
// (next() % 1000000), two path words (one of eight each) and a number
// (next() % 1000), the status (one of twelve codes, three of them 5xx) and
// the duration (next() % 2000 milliseconds, so about half of the lines
// have four digits). ASCII only.
func genLog(w *bufio.Writer, spec dataSpec) error {
	g := splitmix{spec.Seed}
	levels := []string{"INFO", "WARN", "ERROR", "DEBUG"}
	svcs := []string{"api", "auth", "db", "cache", "web"}
	words := []string{"items", "users", "orders", "cart", "search", "login", "static", "health"}
	codes := []int{200, 201, 204, 301, 304, 400, 401, 403, 404, 500, 502, 503}
	var n int64
	for n < spec.Bytes {
		ms := g.next() % 86400000
		line := fmt.Sprintf("2026-11-07T%02d:%02d:%02d.%03dZ %s svc=%s req=%d path=/%s/%s/%d status=%d dur=%dms\n",
			ms/3600000, ms/60000%60, ms/1000%60, ms%1000,
			levels[g.next()%4], svcs[g.next()%5], g.next()%1000000,
			words[g.next()%8], words[g.next()%8], g.next()%1000,
			codes[g.next()%12], g.next()%2000)
		w.WriteString(line)
		n += int64(len(line))
	}
	return nil
}

// genGzip writes the input of the idiomatic gzip row: the "words" text of
// spec (genWords, about spec.Bytes bytes before compression) compressed by
// Go's compress/gzip at gzip.DefaultCompression, with an empty header. The
// row's checksum is over the decompressed bytes, so another Go release's
// compressor, which may emit other bytes, gives the same checksum.
func genGzip(w *bufio.Writer, spec dataSpec) error {
	zw, err := gzip.NewWriterLevel(w, gzip.DefaultCompression)
	if err != nil {
		return err
	}
	bw := bufio.NewWriterSize(zw, 1<<20)
	if err := genWords(bw, spec); err != nil {
		return err
	}
	if err := bw.Flush(); err != nil {
		return err
	}
	return zw.Close()
}
