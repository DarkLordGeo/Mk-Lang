// Why: a const initializer may read a file at build time (build.File);
// a failed read must be a diagnostic at the const, not an evaluator panic.
//
// What: dataFileDiag, E0910 file-input.
//
// How: renders the evaluation's EvalDataFile with the same facts
// eval.DataFileDiag gives gen, since check does not import eval.

package check

import (
	"mk/internal/diag"
	"mk/internal/source"
	"mk/internal/types"
)

// dataFileDiag is E0910 file-input at site for a failed build.File read:
// facts path and reason, as eval.DataFileDiag gives it for gen (check does
// not import eval).
func dataFileDiag(site source.Span, err *types.EvalError) *diag.Diagnostic {
	why := map[types.DataFileError]string{
		types.DataFilePath:       "is not a relative `/` path with no `..`, `.` or empty element, drive or backslash",
		types.DataFileMissing:    "names no file under the package root",
		types.DataFileNotRegular: "names something that is not a regular file",
		types.DataFileLink:       "is reached through a link",
		types.DataFileTooLarge:   "names a file of more than 64 MiB",
	}[err.File]
	d := diag.New(diag.E0910, site, "the data file path \""+err.Path+"\" "+why).
		Text("path", err.Path).Text("reason", err.File.String())
	switch err.File {
	case types.DataFilePath:
		d.Alt("write the path relative to the package root, with `/` and no `..`")
	case types.DataFileMissing:
		d.Alt("create the file, or correct the path: it is relative to the root of the package holding the reading module")
	case types.DataFileLink:
		d.Alt("read the file where it is, not through a link")
	case types.DataFileTooLarge:
		d.Alt("split the file: a data file is at most 64 MiB")
	}
	return d
}
