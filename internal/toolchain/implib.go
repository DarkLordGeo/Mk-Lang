// Why: a Windows executable links against import libraries, and their
// export lists come from std's and packages' .def files; a stray character
// in a name could silently change what the library imports.
//
// What: ParseDefList, DefText, ImportLib and the name checks validExport
// and validDLL.
//
// How: only plain names pass (no ';', '=', '@' or module-definition
// keyword); the text is "LIBRARY <dll>", "EXPORTS" and one name per line,
// run through llvm-lib, or the COFF linker's /lib mode when there is no
// llvm-lib.

package toolchain

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// defKeywords are the words llvm-lib's module-definition parser reads as
// keywords (LLVM COFFModuleDefinition.cpp). A line holding one is no
// export; "DATA" after EXPORTS is swallowed without an error
// (measured with llvm-lib 21.1.8 on 2026-11-03). Compared without case.
var defKeywords = []string{"BASE", "CONSTANT", "DATA", "EXPORTAS", "EXPORTS", "HEAPSIZE",
	"LIBRARY", "NAME", "NONAME", "PRIVATE", "STACKSIZE", "VERSION"}

// validExport reports whether n is one plain exported name: an ASCII letter
// or '_', then letters, digits and '_', and not a module-definition
// keyword. Anything else could change the import library's meaning
// without llvm-lib noticing: ';' starts a comment, '=' makes an alias,
// '@' an ordinal, and a keyword is read as one.
func validExport(n string) bool {
	if n == "" || (n[0] >= '0' && n[0] <= '9') {
		return false
	}
	for i := 0; i < len(n); i++ {
		c := n[i]
		if !(c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9') {
			return false
		}
	}
	for _, k := range defKeywords {
		if strings.EqualFold(n, k) {
			return false
		}
	}
	return true
}

// validDLL reports whether d is a DLL file name the LIBRARY line carries
// as one token: letters, digits, '_', '-' and '.', not starting with '.'.
func validDLL(d string) bool {
	if d == "" || d[0] == '.' {
		return false
	}
	for i := 0; i < len(d); i++ {
		c := d[i]
		if !(c == '_' || c == '-' || c == '.' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}

// ParseDefList reads the runtime's `.def` list format
// (std/sys/windows/<dll>.def): one exported name per line. Blank lines and
// surrounding spaces are ignored, CRLF is accepted, and the order is kept. A
// line that is not one plain name (validExport: inner whitespace, ';', '=',
// '@', a keyword) is an error naming its line number.
func ParseDefList(text string) ([]string, error) {
	var names []string
	for i, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if !validExport(line) {
			return nil, fmt.Errorf("line %d: %q is not one exported name", i+1, line)
		}
		names = append(names, line)
	}
	return names, nil
}

// DefText is the module-definition text llvm-lib reads: "LIBRARY <dll>",
// "EXPORTS", then one name per line, each line ending in "\n". llvm-lib
// 21.1.8 refuses a bare name list, so the header is never optional. A bad
// dll (validDLL), an empty list, or a name that is not one plain name
// (validExport) is an error.
func DefText(dll string, names []string) (string, error) {
	if !validDLL(dll) {
		return "", fmt.Errorf("toolchain: bad DLL name %q", dll)
	}
	if len(names) == 0 {
		return "", errors.New("toolchain: import library for " + dll + " with no names")
	}
	var b strings.Builder
	b.WriteString("LIBRARY " + dll + "\nEXPORTS\n")
	for _, n := range names {
		if !validExport(n) {
			return "", fmt.Errorf("toolchain: bad export name %q for %s", n, dll)
		}
		b.WriteString(n + "\n")
	}
	return b.String(), nil
}

// importLibArgs is llvm-lib's command line after the executable.
func importLibArgs(def, machine, out string) []string {
	return []string{"/def:" + def, "/machine:" + machine, "/out:" + out}
}

// ImportLib writes a COFF import library for one DLL. names is the
// runtime's list (ParseDefList), dll the DLL file name ("kernel32.dll"),
// machine llvm-lib's machine ("x64"), out an absolute path. It writes
// DefText to a temporary `.def` next to out, runs
// `llvm-lib /def:<tmp>.def /machine:<machine> /out:<out>`, and removes the
// temporary file. The error is *ToolError, or a bad input before any
// process starts.
func (t *Tools) ImportLib(ctx context.Context, dll string, names []string, machine string, out string) error {
	text, err := DefText(dll, names)
	if err != nil {
		return err
	}
	if !filepath.IsAbs(out) {
		return fmt.Errorf("toolchain: import library path must be absolute: %q", out)
	}
	if machine == "" || strings.ContainsAny(machine, " \t") {
		return fmt.Errorf("toolchain: bad machine %q", machine)
	}
	tmp, err := os.CreateTemp(filepath.Dir(out), "implib-*.def")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(text); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	tool, path, args := t.importLibTool(importLibArgs(tmp.Name(), machine, out))
	_, err = run(ctx, tool, path, args, filepath.Dir(out))
	return err
}

// importLibTool is the program ImportLib runs and its whole argument list:
// llvm-lib when Dir has it; else the COFF linker in its /lib mode
// (`lld-link /lib ...` or `lld -flavor link /lib ...`), which runs the same
// library driver and wrote byte-identical import libraries for std's
// kernel32.def and a package's list (TestImportLibByLinker checks it).
func (t *Tools) importLibTool(args []string) (tool, path string, all []string) {
	if t.LLVMLib != "" {
		return NameLLVMLib, t.LLVMLib, args
	}
	tool, path, pre := t.linker(COFF)
	return tool, path, append(append(pre, "/lib"), args...)
}
