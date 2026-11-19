// Why: on very large programs the syntax trees of checked modules hold
// memory that nothing reads again.
//
// What: Module.Release, which drops a checked module's syntax side.
//
// How: once every unit of a module is checked and lowered and its
// declarations evaluated, nothing reads its ASTs through check again; a
// released module answers its importers as a module Load made does.

package check

// Release drops m's syntax side: the module input (its ASTs), resolve's
// Info, the node index of every file, the item records, the early bodies,
// the capture records and the finished impl flow jobs. What importers and
// the later stages read stays: the item list (in resolve's order), the
// member sets, the impl table, the assertions and Slice impls, the units,
// the diagnostics and Reads.
//
// The caller calls it once the module's own work is through (its units
// checked, its Early bodies lowered, its EvalConst calls made) and before
// any importer starts, so no goroutine reads the dropped fields while they
// are written. After Release, Check, EvalConst, Early and every other
// entry that reads the module's syntax must not be called for m (an ICE,
// as for a module Load made); a session that answers queries over syntax
// after the check (the driver's KeepSyntax) does not release.
func (m *Module) Release() {
	if m.info != nil {
		m.ids = m.itemIDs()
	}
	m.in, m.info, m.files, m.byItem = nil, nil, nil, nil
	m.early, m.implFlows = nil, nil
	m.capMu.Lock()
	m.caps = nil
	m.capMu.Unlock()
	m.litE0601 = nil
}
