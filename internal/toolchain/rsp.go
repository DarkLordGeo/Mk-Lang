// Why: a build of thousands of packs can exceed Windows' 32,767-character
// command line, so a long link must read its arguments from a file.
//
// What: runLinkerIn, the response file text (rspText) and the quoting of
// each linker flavour (quoteWindows, quotePOSIX, rspQuoting).
//
// How: from responseFileAt characters on, the arguments go into a response
// file in the link's directory, written with an explicit quoting option and
// removed afterwards.

package toolchain

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// responseFileAt is the length of a link's rendered command line
// (CommandLine) from which the linker reads its arguments from a response
// file. Windows' CreateProcess limit is 32,767 characters, and a build of
// about 200,000 lines passed 3,455 objects in 294 KB (tools/synth, the
// synth writer, 2026-11-18). Tests set it to 0 to force the file.
var responseFileAt = 16 << 10

// rspQuoting is the linker option that names the response file's quoting,
// given before the @file: Windows rules for lld-link (its default on every
// host), POSIX rules for ld.lld, whose default otherwise follows the host.
// ld64.lld has no such option and always reads POSIX (GNU) quoting, so a
// Mach-O link gets none ("").
func rspQuoting(f Format) string {
	switch f {
	case COFF:
		return "--rsp-quoting=windows"
	case MachO:
		return ""
	}
	return "--rsp-quoting=posix"
}

// rspText is args as a response file under f's quoting, one argument per
// line.
func rspText(f Format, args []string) string {
	var b strings.Builder
	for _, a := range args {
		if f == COFF {
			b.WriteString(quoteWindows(a))
		} else {
			b.WriteString(quotePOSIX(a))
		}
		b.WriteByte('\n')
	}
	return b.String()
}

// quoteWindows quotes a for LLVM's Windows tokenizer (the rules of
// CommandLineToArgvW): an argument with no space, tab, newline or double
// quote, and not empty, is as it is; otherwise it is wrapped in double
// quotes, a double quote inside is \", and the backslashes before a double
// quote (or before the closing one) are doubled.
func quoteWindows(a string) string {
	if a != "" && !strings.ContainsAny(a, " \t\n\r\"") {
		return a
	}
	var b strings.Builder
	b.WriteByte('"')
	slashes := 0
	for i := 0; i < len(a); i++ {
		c := a[i]
		switch c {
		case '\\':
			slashes++
			continue
		case '"':
			b.WriteString(strings.Repeat(`\`, 2*slashes+1))
		default:
			b.WriteString(strings.Repeat(`\`, slashes))
		}
		slashes = 0
		b.WriteByte(c)
	}
	b.WriteString(strings.Repeat(`\`, 2*slashes))
	b.WriteByte('"')
	return b.String()
}

// quotePOSIX quotes a for LLVM's GNU tokenizer: a backslash before every
// backslash, quote and white-space character; an empty argument is "".
func quotePOSIX(a string) string {
	if a == "" {
		return `""`
	}
	var b strings.Builder
	for i := 0; i < len(a); i++ {
		switch a[i] {
		case '\\', '"', '\'', ' ', '\t', '\n', '\r':
			b.WriteByte('\\')
		}
		b.WriteByte(a[i])
	}
	return b.String()
}

// runLinkerIn runs the format's linker with args in dir. A command line of
// responseFileAt characters or more passes args through a response file
// written in dir and removed afterwards. With private (dir is a directory
// of this link alone: thinLink's and pdbLink's work directories) the file
// is link.rsp, named relatively, so a PDB, which records the command line,
// does not depend on a random name; otherwise a new temporary file, by
// its absolute path. A ToolError names the whole command, not the file.
func (t *Tools) runLinkerIn(ctx context.Context, f Format, args []string, dir string, private bool) ([]byte, error) {
	tool, path, pre := t.linker(f)
	full := append(append([]string(nil), pre...), args...)
	if path == "" || len(CommandLine(path, full)) < responseFileAt {
		return run(ctx, tool, path, full, dir)
	}
	text := []byte(rspText(f, args))
	var at, file string
	if private {
		file, at = filepath.Join(dir, "link.rsp"), "link.rsp"
		if err := os.WriteFile(file, text, 0o644); err != nil {
			return nil, err
		}
	} else {
		tmp, err := os.CreateTemp(dir, "mkrsp-*.rsp")
		if err != nil {
			return nil, err
		}
		file, at = tmp.Name(), tmp.Name()
		_, werr := tmp.Write(text)
		if cerr := tmp.Close(); werr == nil {
			werr = cerr
		}
		if werr != nil {
			os.Remove(file)
			return nil, werr
		}
	}
	defer os.Remove(file)
	rargs := append([]string(nil), pre...)
	if q := rspQuoting(f); q != "" {
		rargs = append(rargs, q)
	}
	out, err := run(ctx, tool, path, append(rargs, "@"+at), dir)
	var te *ToolError
	if errors.As(err, &te) {
		te.Command = CommandLine(path, full)
	}
	return out, err
}
