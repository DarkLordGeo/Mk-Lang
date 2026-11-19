// Why: a package's API documentation and its examples must come from the
// code, or they drift; mk doc is the command that writes the one and
// checks the other.
//
// What: runDoc, mk doc [--html | --embed] [-o file] [--std module] [path]:
// the public surface of the package, or of std's modules under a path, as
// Markdown, as HTML, or as the API section a module page embeds
// (driver/doc.go). docCheck, mk doc --check [path]: compiles the fenced mk
// blocks of the package's /// text instead.
//
// How: --check judges each block by its verdict line as the corpus judges
// doc/'s blocks (internal/corpus: Extract, BlockUnit, JudgeUnit), with the
// test prelude std embeds. A block without a verdict line is an error; exit
// 1 when a block fails. Each block is a unit of its own: it sees std and
// the test prelude, not the package it documents, so an example that calls
// the package's own items cannot be checked this way.

package main

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"mk/internal/corpus"
	"mk/internal/driver"
)

func init() {
	register(command{"doc", "doc     [flags] [path]         the package's public surface as Markdown or HTML; --check compiles the doc text's mk blocks", runDoc})
}

func runDoc(args []string, stdout, stderr io.Writer) int {
	var cf compileFlags
	fs := newFlagSet("doc", stderr, &cf)
	asHTML := fs.Bool("html", false, "write HTML instead of Markdown")
	embed := fs.Bool("embed", false, "write the API section a page embeds: no title and no module heading for the first module, items at ###")
	out := fs.String("o", "", "write to this file instead of standard output")
	stdMod := fs.String("std", "", "document std's public modules under this path (std for all) instead of the package; takes no path")
	check := fs.Bool("check", false, "compile the fenced mk blocks of the package's /// text, each by its verdict")
	path, _, err := parseArgs(fs, args, false)
	if err != nil {
		return usageErr(stderr, fs, err)
	}
	if *check {
		return docCheck(path, cf, stdout, stderr)
	}
	if *embed && *asHTML {
		return usageErr(stderr, fs, fmt.Errorf("--embed writes Markdown; it takes no --html"))
	}
	if *stdMod != "" {
		if path != "" {
			return usageErr(stderr, fs, fmt.Errorf("--std takes no path"))
		}
		// std's surface needs no package: check from an empty file.
		dir, err := os.MkdirTemp("", "mk-doc-")
		if err != nil {
			fmt.Fprintf(stderr, "mk doc: %v\n", err)
			return 2
		}
		defer os.RemoveAll(dir)
		path = filepath.Join(dir, "std_doc.mk")
		if err := os.WriteFile(path, []byte("// The root of mk doc --std: std's surface is documented.\n"), 0o644); err != nil {
			fmt.Fprintf(stderr, "mk doc: %v\n", err)
			return 2
		}
	}
	cfg, ok := cf.config(path, stderr)
	if !ok {
		return 2
	}
	// A std-only module such as std.raw is indexed only when std is the
	// code being read.
	cfg.BrowseStd = driver.DocStdBrowse(*stdMod)
	s, err := driver.NewSession(cfg)
	if err != nil {
		fmt.Fprintf(stderr, "mk doc: %v\n", err)
		return 2
	}
	dp, res, err := s.Doc(driver.DocOptions{Std: *stdMod})
	if err != nil {
		fmt.Fprintf(stderr, "mk doc: %v\n", err)
		return 2
	}
	if dp == nil {
		_ = res.WriteReport(stderr, cf.json)
		return max(1, driver.ExitStatus(res.Report))
	}
	w := stdout
	if *out != "" {
		f, err := os.Create(*out)
		if err != nil {
			fmt.Fprintf(stderr, "mk doc: %v\n", err)
			return 2
		}
		defer f.Close()
		w = f
	}
	switch {
	case *asHTML:
		err = dp.WriteHTML(w)
	case *embed:
		err = dp.WriteEmbed(w)
	default:
		err = dp.WriteMarkdown(w)
	}
	if err != nil {
		fmt.Fprintf(stderr, "mk doc: %v\n", err)
		return 2
	}
	return 0
}

// docCompiler is corpus.Compiler over a corpus session, with the test
// prelude std embeds (std.TestPreludePath), so mk doc --check needs no
// repository.
type docCompiler struct {
	sess    *driver.CorpusSession
	prelude string
}

func (d *docCompiler) Start() (corpus.StdInfo, error) {
	res := d.sess.Std()
	info := corpus.StdInfo{State: d.sess.StdState(), Through: d.sess.Through()}
	if len(res.Report.Diags) > 0 {
		info.Text = res.Report.Text(res.Sources)
	}
	return info, nil
}

func (d *docCompiler) Check(u *corpus.Unit, mode driver.UnitMode) driver.BlockResult {
	prelude := d.prelude
	if u.Block != nil {
		prelude = driver.DocPreludeFor(prelude, u.Block.Source) // the block's own names win
	}
	return d.sess.CheckUnit(corpus.ToUnitInput(u, prelude, mode))
}

// blockLineRe is a position in a block's own file, block.mk:N.
var blockLineRe = regexp.MustCompile(regexp.QuoteMeta(corpus.BlockFile) + `:(\d+)`)

// fileLines rewrites block.mk:N in s to file:M, M the file's line (the
// verdict line is b.Line, the block's line 1 the next).
func fileLines(s string, b *corpus.Block) string {
	return blockLineRe.ReplaceAllStringFunc(s, func(m string) string {
		n, _ := strconv.Atoi(blockLineRe.FindStringSubmatch(m)[1])
		return b.Document + ":" + strconv.Itoa(b.Line+n)
	})
}

func (d *docCompiler) Available() driver.StageSet { return driver.Available() }

// docCheck is mk doc --check: every .mk file under the package root (a
// directory, or one file), testdata and dot directories left out.
func docCheck(path string, cf compileFlags, stdout, stderr io.Writer) int {
	root := path
	if root == "" {
		root = "."
	}
	var files []string
	if st, err := os.Stat(root); err == nil && !st.IsDir() {
		files = []string{root}
	} else {
		err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() && p != root && (d.Name() == "testdata" || strings.HasPrefix(d.Name(), ".")) {
				return filepath.SkipDir
			}
			if !d.IsDir() && strings.HasSuffix(p, ".mk") {
				files = append(files, p)
			}
			return nil
		})
		if err != nil {
			fmt.Fprintf(stderr, "mk doc --check: %v\n", err)
			return 2
		}
	}
	slices.Sort(files)
	prelude, err := driver.DocTestPrelude()
	if err != nil {
		fmt.Fprintf(stderr, "mk doc --check: the test prelude: %v\n", err)
		return 2
	}
	cfg := driver.Config{LLVMDir: cf.llvm, Jobs: cf.jobs, CacheDir: userCacheDir()}
	sess, err := driver.NewCorpusSession(cfg)
	if err != nil {
		fmt.Fprintf(stderr, "mk doc --check: %v\n", err)
		return 2
	}
	comp := &docCompiler{sess: sess, prelude: prelude}
	info, _ := comp.Start()
	failed, n := 0, 0
	for _, f := range files {
		text, err := os.ReadFile(f)
		if err != nil {
			fmt.Fprintf(stderr, "mk doc --check: %v\n", err)
			return 2
		}
		// One block at a time: a malformed one (no verdict line, no
		// closing fence) is reported and the others still run.
		for _, doc := range driver.DocBlockSources(string(text)) {
			n++
			blocks, err := corpus.Extract(filepath.ToSlash(f), doc)
			if err != nil || len(blocks) != 1 {
				failed++
				fmt.Fprintf(stdout, "FAIL %v\n", err)
				continue
			}
			b := &blocks[0]
			u := corpus.BlockUnit(b)
			j := corpus.JudgeUnit(u, comp.Check(u, driver.UnitCheck), info.State, comp.Available())
			fmt.Fprintf(stdout, "%s %s", j.Status, b.Key())
			if j.Reason != "" {
				fmt.Fprintf(stdout, ": %s", fileLines(j.Reason, b))
			}
			fmt.Fprintln(stdout)
			if j.Status == corpus.StatusPass {
				continue
			}
			failed++
			for _, g := range j.Emitted {
				at := g.File
				if g.Own {
					at = b.Document + ":" + strconv.Itoa(b.Line+g.Line)
				} else if g.Line > 0 {
					at += ":" + strconv.Itoa(g.Line)
				}
				fmt.Fprintf(stdout, "    %s %s %s\n", at, g.Code, g.Message)
			}
		}
	}
	fmt.Fprintf(stdout, "%d doc blocks, %d failed\n", n, failed)
	if failed > 0 {
		return 1
	}
	return 0
}
