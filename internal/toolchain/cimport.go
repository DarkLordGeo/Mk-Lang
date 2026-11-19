// Why: the C import runs clang as its parser (preprocessor, AST and probe
// runs), and every process mk starts must go through one runner with the
// scrubbed environment of run.go.
//
// What: RunClang, StreamClang and ResourceDir.
//
// How: internal/cimport builds the command lines (-nostdinc, explicit
// -isystem and --sysroot); this file only runs them, streaming large output
// such as a JSON AST to its reader.

package toolchain

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// RunClang runs clang with args in dir and returns its stdout and stderr.
// A non-zero exit is *ToolError with both still returned: the C import
// reads stderr's errors to drop failed macro probes.
func (t *Tools) RunClang(ctx context.Context, args []string, dir string) ([]byte, []byte, error) {
	return runBoth(ctx, NameClang, t.Clang, args, dir)
}

// StreamClang starts clang with args in dir and hands its stdout to read
// while it runs, so a large output (the JSON AST of windows.h is about
// 300 MB) is never held whole. read's error wins over clang's, so a
// *ToolError means read finished and clang still exited non-zero (the
// caller may accept that: a failed macro probe is an error line); stderr is
// returned either way. If read returns before the end, the rest of stdout
// is discarded so clang can finish.
func (t *Tools) StreamClang(ctx context.Context, args []string, dir string, read func(io.Reader) error) ([]byte, error) {
	cmdline := CommandLine(t.Clang, args)
	if t.Clang == "" {
		return nil, &ToolError{Tool: NameClang, Command: cmdline, ExitCode: ExitNotStarted, Err: errNoTool}
	}
	cmd := exec.CommandContext(ctx, t.Clang, args...)
	cmd.Env = scrubbedEnv(runtime.GOOS, os.Getenv)
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, &ToolError{Tool: NameClang, Command: cmdline, ExitCode: ExitNotStarted, Err: err}
	}
	if err := cmd.Start(); err != nil {
		return nil, toolError(ctx, NameClang, cmdline, "", nil, err)
	}
	rerr := read(out)
	_, _ = io.Copy(io.Discard, out)
	werr := cmd.Wait()
	if rerr != nil {
		return stderr.Bytes(), rerr
	}
	if werr != nil {
		return stderr.Bytes(), toolError(ctx, NameClang, cmdline, stderr.String(), cmd.ProcessState, werr)
	}
	return stderr.Bytes(), nil
}

// ResourceDir is clang's resource directory (`clang -print-resource-dir`):
// its include directory holds stddef.h, stdarg.h, stdint.h and the other
// freestanding headers, the only system headers a C import or a C source
// sees without a sysroot.
func (t *Tools) ResourceDir(ctx context.Context) (string, error) {
	out, err := run(ctx, NameClang, t.Clang, []string{"-print-resource-dir"}, "")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
