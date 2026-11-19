// Why: the corpus judges every fenced mk block of the documentation, so
// it needs one fixed answer to where the repository is, which documents it
// reads and in what order, and where the test prelude comes from.
//
// What: FindRoot (the repository root), Documents (every doc/**/*.md in
// byte order of path), DesignBlocks (their blocks, in document and line
// order) and Prelude (the test prelude from the reference).
//
// How: the root is the directory holding doc/reference.md, given or found
// by walking up. The prelude is the first mk block under the reference's
// "Test prelude" heading; std/corpus/testprelude.mk must equal it byte for
// byte, which a std test checks.

package corpus

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// rootMarker is the file whose presence marks the repository root.
const rootMarker = "doc/reference.md"

// FindRoot returns the repository root, absolute: --root when given, else
// found by walking up from the working directory. An explicit start is the
// root itself: it must hold doc/reference.md, and its ancestors are never
// tried, so a wrong --root inside a repository is an error (exit 2), not a
// silent run over the enclosing repository. An empty start means the
// working directory and its ancestors, and when none qualifies, the
// directory of the running executable and its ancestors. There is no
// build-time path.
func FindRoot(start string) (string, error) {
	if start != "" {
		abs, err := filepath.Abs(start)
		if err != nil {
			return "", fmt.Errorf("--root %s: %v", start, err)
		}
		if !isRoot(abs) {
			return "", fmt.Errorf("--root %s is no repository root: it holds no %s", start, rootMarker)
		}
		return abs, nil
	}
	var tries []string
	if wd, err := os.Getwd(); err == nil {
		tries = append(tries, wd)
	}
	if exe, err := os.Executable(); err == nil {
		tries = append(tries, filepath.Dir(exe))
	}
	for _, t := range tries {
		abs, err := filepath.Abs(t)
		if err != nil {
			continue
		}
		for dir := abs; ; {
			if isRoot(dir) {
				return dir, nil
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}
	return "", fmt.Errorf("no repository root: no directory holding %s above %s", rootMarker, strings.Join(tries, " or "))
}

// isRoot reports whether dir holds the root marker as a regular file.
func isRoot(dir string) bool {
	fi, err := os.Stat(filepath.Join(dir, filepath.FromSlash(rootMarker)))
	return err == nil && fi.Mode().IsRegular()
}

// Documents lists the corpus documents of the repository at root: every
// regular file doc/**/*.md (doc/reference.md, doc/std/fs.md), as
// repository-relative paths with '/', in byte order of path. It never
// lists a file outside doc/.
func Documents(root string) ([]string, error) {
	dir := filepath.Join(root, "doc")
	if _, err := os.Stat(dir); err != nil {
		return nil, err
	}
	var out []string
	err := filepath.WalkDir(dir, func(p string, e fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !e.Type().IsRegular() || !strings.HasSuffix(e.Name(), ".md") {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return nil, err
	}
	slices.Sort(out)
	if len(out) == 0 {
		return nil, errors.New("doc/ holds no .md file")
	}
	return out, nil
}

// readDoc reads one repository-relative document.
func readDoc(root, doc string) (string, error) {
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(doc)))
	if err != nil {
		return "", fmt.Errorf("%s: %v", doc, err)
	}
	return string(b), nil
}

// DesignBlocks extracts the blocks of every document Documents lists, in
// document order and then line order. Any extraction error stops the run:
// the corpus is never judged in part.
func DesignBlocks(root string) ([]Block, error) {
	docs, err := Documents(root)
	if err != nil {
		return nil, err
	}
	var out []Block
	for _, d := range docs {
		text, err := readDoc(root, d)
		if err != nil {
			return nil, err
		}
		bs, err := Extract(d, text)
		if err != nil {
			return nil, err
		}
		out = append(out, bs...)
	}
	return out, nil
}

// PreludeDocument and PreludeHeading locate the test prelude: the first mk
// block after this heading line of the reference.
const (
	PreludeDocument = "doc/reference.md"
	PreludeHeading  = "## 19. Test prelude"
)

// Prelude returns the test prelude's source text, LF-terminated lines, read
// from the first mk block under PreludeHeading. The verdict line is not
// part of it. std/corpus/testprelude.mk must equal it byte for byte
// (TestTestPreludeMatchesReference, std's test), so the corpus and
// std cannot drift.
func Prelude(root string) (string, error) {
	text, err := readDoc(root, PreludeDocument)
	if err != nil {
		return "", err
	}
	return preludeFrom(text)
}

// preludeFrom finds the prelude block in the reference's text.
func preludeFrom(text string) (string, error) {
	lines := splitLines(text)
	at := -1
	for i, l := range lines {
		if strings.TrimRight(l, " \t") == PreludeHeading {
			at = i
			break
		}
	}
	if at < 0 {
		return "", fmt.Errorf("%s: no line %q", PreludeDocument, PreludeHeading)
	}
	bs, err := Extract(PreludeDocument, text)
	if err != nil {
		return "", err
	}
	for _, b := range bs {
		if b.Line > at+1 { // at is 0-based; Line is the 1-based verdict line
			return b.Source, nil
		}
	}
	return "", fmt.Errorf("%s: no mk block under %q", PreludeDocument, PreludeHeading)
}
