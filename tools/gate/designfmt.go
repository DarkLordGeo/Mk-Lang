// Why: the examples in doc/ are what readers copy, so every one of them
// must already be formatted as mk fmt writes it (doc/style.md).
//
// What: the gate's design fmt step: designBlocks (every fenced mk block of
// doc/**/*.md), judgeDesignFmt (mk fmt -l's output read back into block
// names) and designFmtStep.
//
// How: each block is written to its own file and the folder goes through
// one mk fmt -l. A block mk fmt would change fails the step, named as the
// corpus names it (the document under doc/ and the line of the block's
// first line). A block that does not parse is listed by mk fmt as a syntax
// error and is not judged here: its verdict is the corpus step's.

package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// designBlock is one fenced mk block of a document under doc/.
type designBlock struct {
	name string // "reference.md:1359" or "std/fs.md:12", the corpus's name for the block
	file string // the file the step writes it to, "reference_1359.mk" or "std_fs_12.mk"
	text string
}

// designBlocks reads every fenced mk block of root/doc/**/*.md, in byte
// order of the document's path and then line order.
func designBlocks(root string) ([]designBlock, error) {
	dir := filepath.Join(root, "doc")
	var docs []string
	err := filepath.WalkDir(dir, func(p string, e fs.DirEntry, err error) error {
		if err != nil || !e.Type().IsRegular() || !strings.HasSuffix(p, ".md") {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		docs = append(docs, filepath.ToSlash(rel))
		return err
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(docs)
	var out []designBlock
	for _, doc := range docs {
		raw, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(doc)))
		if err != nil {
			return nil, err
		}
		base := doc
		lines := strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n")
		for i := 0; i < len(lines); i++ {
			if strings.TrimSpace(lines[i]) != "```mk" {
				continue
			}
			first := i + 2 // 1-based line of the block's first line
			var b strings.Builder
			for i++; i < len(lines) && strings.TrimSpace(lines[i]) != "```"; i++ {
				b.WriteString(lines[i] + "\n")
			}
			out = append(out, designBlock{
				name: base + ":" + strconv.Itoa(first),
				file: strings.ReplaceAll(strings.TrimSuffix(base, ".md"), "/", "_") + "_" + strconv.Itoa(first) + ".mk",
				text: b.String(),
			})
		}
	}
	return out, nil
}

// judgeDesignFmt reads mk fmt -l's output over the written blocks: a line
// that is a block's path is a block mk fmt would change; a line that starts
// with a block's path and a colon is that block's syntax error.
func judgeDesignFmt(blocks []designBlock, out []string) (changed []string, syntax int) {
	byFile := make(map[string]string, len(blocks))
	for _, b := range blocks {
		byFile[b.file] = b.name
	}
	for _, l := range out {
		l = strings.TrimSpace(l)
		if name, ok := byFile[fileBase(l)]; ok && strings.HasSuffix(l, ".mk") {
			changed = append(changed, name)
			continue
		}
		if p, _, ok := strings.Cut(l, ".mk: "); ok {
			if _, ok := byFile[fileBase(p+".mk")]; ok {
				syntax++
			}
		}
	}
	sort.Strings(changed)
	return changed, syntax
}

// fileBase is the last element of a path split at / and at \, whichever
// host wrote it: a block's file name holds neither.
func fileBase(p string) string {
	return p[strings.LastIndexAny(p, `/\`)+1:]
}

// designFmtStep runs mk fmt -l over every block of doc/ (skipped with the
// corpus steps).
func (g *gate) designFmtStep() {
	const name = "design fmt"
	if g.skip["corpus"] {
		g.skipStep(name, "-skip corpus")
		return
	}
	s := g.newStep(name)
	blocks, err := designBlocks(g.root)
	if err == nil && len(blocks) < 100 {
		err = fmt.Errorf("%d mk blocks under doc/, want at least 100", len(blocks))
	}
	dir := filepath.Join(g.work, "designfmt")
	if err == nil {
		os.RemoveAll(dir)
		err = os.MkdirAll(dir, 0o755)
	}
	for i := 0; err == nil && i < len(blocks); i++ {
		err = os.WriteFile(filepath.Join(dir, blocks[i].file), []byte(blocks[i].text), 0o644)
	}
	if err != nil {
		fail(s, err.Error())
		return
	}
	var out []string
	code := g.exec(s, spec{argv: []string{g.goExe, "run", "./cmd/mk", "fmt", "-l", dir}, tripwire: true}, func(l string) { out = append(out, l) })
	s.Output = tail(out, 60)
	changed, syntax := judgeDesignFmt(blocks, out)
	s.Notes = append(s.Notes, fmt.Sprintf("%d blocks, %d not fixed points of mk fmt, %d do not parse (judged by the corpus step)", len(blocks), len(changed), syntax))
	fmt.Printf("   %s\n", s.Notes[0])
	for _, c := range changed {
		s.Failed = append(s.Failed, "doc/"+c)
		fmt.Printf("   not a fixed point of mk fmt: doc/%s\n", c)
	}
	switch {
	case len(changed) > 0:
		fail(s, fmt.Sprintf("%d design blocks are not fixed points of mk fmt", len(changed)))
	case code != 0 && syntax == 0:
		fail(s, fmt.Sprintf("mk fmt -l exit status %d with no block named", code))
	case code != 0 && code != 1:
		fail(s, fmt.Sprintf("mk fmt -l exit status %d", code))
	}
}
