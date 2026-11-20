// Why: a package's API documentation and its examples must come from the
// code, or they drift; mk doc is the command that writes the one and
// checks the other.
//
// What: runDoc, mk doc [--html | --embed] [-o file] [--std module] [path]:
// the public surface of the package, or of std's modules under a path, as
// Markdown, as HTML, or as the API section a module page embeds
// (driver/doc.go). A path std or std.<module> that names nothing on disk
// is --std's module (stdModuleArg), and an unknown module is refused.
// docCheck, mk doc --check [path]: compiles the fenced mk blocks of the
// package's /// text instead, or of std's modules under --std.
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
	if *stdMod == "" && stdModuleArg(path) {
		*stdMod, path = path, ""
	}
	var stdFiles []driver.DocFile
	if *stdMod != "" {
		if path != "" {
			return usageErr(stderr, fs, fmt.Errorf("--std takes no path"))
		}
		if stdFiles, err = driver.DocStdFiles(*stdMod); err != nil {
			fmt.Fprintf(stderr, "mk doc: %v\n", err)
			return 2
		}
	}
	if *check {
		return docCheck(path, stdFiles, cf, stdout, stderr)
	}
	if *embed && *asHTML {
		return usageErr(stderr, fs, fmt.Errorf("--embed writes Markdown; it takes no --html"))
	}
	if *stdMod != "" {
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

// stdModuleArg reports whether mk doc's path argument names std's modules
// rather than a package: std or std.<module>, with no path separator, no
// .mk suffix (a mistyped file name stays a path) and nothing of that name
// on disk. mk doc std.fs is then mk doc --std std.fs; runDoc refuses a name
// that is no std module (driver.DocStdFiles).
func stdModuleArg(path string) bool {
	if path != "std" && !strings.HasPrefix(path, "std.") || strings.HasSuffix(path, ".mk") || strings.ContainsAny(path, `/\`) {
		return false
	}
	_, err := os.Stat(path)
	return err != nil
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
// directory, or one file), testdata and dot directories left out, or the
// files of std's modules when stdFiles is set (--std, or a std.<module>
// path).
func docCheck(path string, stdFiles []driver.DocFile, cf compileFlags, stdout, stderr io.Writer) int {
	files := stdFiles
	if files == nil {
		var err error
		if files, err = packageDocFiles(path); err != nil {
			fmt.Fprintf(stderr, "mk doc --check: %v\n", err)
			return 2
		}
	}
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
		// One block at a time: a malformed one (no verdict line, no
		// closing fence) is reported and the others still run.
		for _, doc := range driver.DocBlockSources(f.Text) {
			n++
			blocks, err := corpus.Extract(filepath.ToSlash(f.Path), doc)
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

// packageDocFiles is every .mk file under the package root path (a
// directory, or one file; "" is the working directory), testdata and dot
// directories left out, in path order.
func packageDocFiles(path string) ([]driver.DocFile, error) {
	root := path
	if root == "" {
		root = "."
	}
	var names []string
	if st, err := os.Stat(root); err == nil && !st.IsDir() {
		names = []string{root}
	} else {
		err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() && p != root && (d.Name() == "testdata" || strings.HasPrefix(d.Name(), ".")) {
				return filepath.SkipDir
			}
			if !d.IsDir() && strings.HasSuffix(p, ".mk") {
				names = append(names, p)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	slices.Sort(names)
	files := make([]driver.DocFile, 0, len(names))
	for _, n := range names {
		b, err := os.ReadFile(n)
		if err != nil {
			return nil, err
		}
		files = append(files, driver.DocFile{Path: n, Text: string(b)})
	}
	return files, nil
}
