// Why: consts may embed a data file or depend on the build profile, but the
// evaluator has no I/O and must keep such reads visible to the cache.
//
// What: std.build's intrinsics (build.File, ProfileOf), the BuildProfile
// extension of Program, readDataFile, and the host helpers SetBytes,
// GenDataFile and Fn.
//
// How: build.File reads through the Program's types.DataFiles extension for
// the module of the request (the module holding the const initializer), and
// records a ReadData in the segment's read set; build.Profile's initializer
// reads the profile through the BuildProfile extension.

package eval

import (
	"mk/internal/mir"
	"mk/internal/types"
)

// BuildProfile is an optional extension of Program: the profile the build
// runs in, which std.build's Profile constant holds. A Program
// without it answers Dev.
type BuildProfile interface {
	Profile() types.Profile
}

// ProfileKind discriminants: the declaration order of std.build's enum
// ProfileKind (Dev, Release, Test, Checked), mapped from types.Profile by
// name, since types orders Dev, TestProfile, Release, Checked.
func profileKind(p types.Profile) uint64 {
	switch p {
	case types.Release:
		return 1
	case types.TestProfile:
		return 2
	case types.Checked:
		return 3
	}
	return 0
}

func init() {
	intrinsics["std.build.File"] = buildFile
	intrinsics["std.build.ProfileOf"] = func(m *Machine, f *frame, t *mir.Terminator, _ types.FnRef) {
		p := types.Dev
		if bp, ok := m.cfg.Program.(BuildProfile); ok {
			p = bp.Profile()
		}
		m.store(m.dest(f, t), profileKind(p))
	}
}

// buildFile is build.File(path: str): []u8. A failed read is EvalDataFile
// (E0910); a Program without DataFiles refuses it as I/O. The bytes are
// charged as a copy of them, ceil(n/16) steps, so a large file
// needs a stated fuel.
func buildFile(m *Machine, f *frame, t *mir.Terminator, _ types.FnRef) {
	d, n := m.sliceWords(m.vargLoc(f, t, 0))
	path := string(m.readBytes(d, n, m.takeVal()))
	df, ok := m.cfg.Program.(types.DataFiles)
	if !ok {
		refuse(types.RefuseIo, "build.File without a data file reader")
	}
	r := df.ReadData(m.req.Module, path)
	m.readDataFile(r, path)
	m.putBytes(m.dest(f, t), r.Bytes)
}

// readDataFile records a data file read's ReadData in the running
// segment's read set and ends the evaluation with EvalDataFile when the
// read failed. Gen's GenDataFile calls it for Gen.Input.
func (m *Machine) readDataFile(r types.DataFile, path string) {
	if r.Read.Kind != 0 {
		m.seg().reads.Add(r.Read)
	}
	if r.Err != types.DataFileOK {
		panic(&stop{err: &types.EvalError{Kind: types.EvalDataFile, File: r.Err, Path: path}})
	}
	m.charge(c(uint64(len(r.Bytes))))
}

// SetBytes writes a []u8 of b into a fresh own allocation (a host's
// Gen.Input result), after ReadDataFile charged it.
func (m *Machine) SetBytes(dst Value, b []byte) { m.putBytes(m.loc(dst), b) }

// GenDataFile is Gen.Input's read: ReadDataFile under the machine's guard,
// so a failed read is the run's EvalDataFile error, then the bytes into dst.
func (m *Machine) GenDataFile(dst Value, r types.DataFile, path string) *types.EvalError {
	st := m.guard(func() {
		m.readDataFile(r, path)
		m.SetBytes(dst, r.Bytes)
	})
	if st != nil {
		return st.err
	}
	return nil
}

func (m *Machine) putBytes(l loc, b []byte) {
	var w uint64
	if len(b) > 0 {
		a := m.mem.newAlloc(allocOwn, uint64(len(b)))
		copy(m.mem.at(a).bytes, b)
		w = ptrWord(a, 0)
	}
	al := m.mem.access(l.a, l.off, 16, true)
	al.storePtr(l.off, w)
	al.storeScalar(l.off+8, 8, uint64(len(b)))
}

// Fn reads a fn value: the function it names, with its generic arguments
// (a splice of a fn value spells its qualified name).
func (m *Machine) Fn(v Value) (fn types.FnRef, err *types.EvalError) {
	st := m.guard(func() { fn = m.fnOf(m.load(m.loc(v))) })
	if st != nil {
		return types.FnRef{}, st.err
	}
	return fn, nil
}
