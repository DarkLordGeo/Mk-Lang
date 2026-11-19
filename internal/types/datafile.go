// Why: build.File and Gen.Input read data files at compile time, but the
// evaluator has no I/O: the driver reads the file. eval and gen both call it
// and neither imports the driver, so the callback's shape lives here.
//
// What: DataFiles, the host callback; DataFile, its answer; DataFileError;
// DataFileLimit and the hash domains DataDomain and DataFailDomain.
//
// How: a read's answer carries the Read its caller adds to its read set,
// hashed over the bytes (or over the failure reason and path), so the cache
// reruns a reader when the file changes or fails differently.

package types

// DataFileLimit is the largest data file build.File and Gen.Input read:
// 64 MiB.
const DataFileLimit = 64 << 20

// DataDomain is the hash domain of a ReadData read's Hash: the SHA-256 of
// the file's bytes under its own domain (iface's domain table).
const DataDomain = "mk.data.v1"

// DataFailDomain is the hash domain of a failed ReadData read's Hash: the
// SHA-256 of the reason (DataFileError.String), a zero byte and the path as
// written, so a read that fails for another reason (no file, then a
// directory at the path) is another answer and reruns its reader. Registered
// with iface's domain table.
const DataFailDomain = "mk.datafail.v1"

// DataFileError is why a data file read failed; DataFileOK when it did not.
// Its String is the fact reason of E0910.
type DataFileError uint8

const (
	DataFileOK DataFileError = iota
	// DataFilePath: the path is empty, absolute, names a drive, uses `\` or
	// holds a `..` element (or a `.` or empty one); the source text alone
	// decides it, so no read is recorded.
	DataFilePath
	// DataFileMissing: no file at the path, or the reading module has no
	// package root on disk (std, a memory tree).
	DataFileMissing
	// DataFileNotRegular: the path names a directory or another non-regular
	// file.
	DataFileNotRegular
	// DataFileLink: an element of the path, from the package root, is a
	// link (each element is Lstat'ed).
	DataFileLink
	// DataFileTooLarge: more than DataFileLimit bytes.
	DataFileTooLarge
)

var dataFileErrorNames = [...]string{"", "path", "missing", "not-regular", "link", "too-large"}

// String is the fact reason of E0910: path, missing, not-regular, link or
// too-large; "" for DataFileOK.
func (e DataFileError) String() string {
	if int(e) < len(dataFileErrorNames) {
		return dataFileErrorNames[e]
	}
	return "data-file?"
}

// DataFile is one read's answer: the bytes, or why there are none, and the
// read the caller adds to its evaluation's read set (Kind 0 for a
// DataFilePath failure, which records no read).
type DataFile struct {
	Bytes []byte
	Err   DataFileError
	Read  Read
}

// DataFiles reads data files for build.File and Gen.Input. from is the
// module holding the const initializer (build.File) or the gen line
// (Gen.Input), and path the string as written; the implementation validates
// both. ReadData is safe for concurrent use and deterministic within a
// build (each package and path is read once and every reader gets the same
// bytes), and never waits on another goroutine's check, lower or borrow
// work or calls back into the evaluator.
type DataFiles interface {
	ReadData(from ModuleID, path string) DataFile
}
