package discovery

import "sort"

// Change kinds, used as the "change" log attribute and as the label value of
// promscout_target_changes_total. The set is closed: AllChangeKinds lists it.
const (
	ChangeAdded   = "added"
	ChangeRemoved = "removed"
	ChangeChanged = "changed"
)

// AllChangeKinds is the complete, closed set of change kinds.
var AllChangeKinds = []string{ChangeAdded, ChangeRemoved, ChangeChanged}

// targetChange describes how one target differs between two published sets.
type targetChange struct {
	// kind is one of the Change* constants.
	kind string
	// old is the previous state; zero for ChangeAdded.
	old Target
	// new is the current state; zero for ChangeRemoved.
	new Target
}

// diffTargets compares two published target sets.
//
// Targets are identified by Address: the validator publishes at most one
// target per host:port (the first path that validates). A target present in
// both sets whose Path, Exporter, Version or Source differs is reported as
// ChangeChanged — an exporter upgrade shows up as a version change, not as a
// removal plus an addition.
//
// Parameters:
//   - prev: the previously published set.
//   - next: the newly validated set.
//
// Returns the changes ordered by address, so log output is deterministic.
// The result is empty if both sets are equal, regardless of their order.
func diffTargets(prev, next []Target) []targetChange {
	before := make(map[string]Target, len(prev))
	for _, t := range prev {
		before[t.Address] = t
	}

	var changes []targetChange
	seen := make(map[string]bool, len(next))

	for _, n := range next {
		seen[n.Address] = true
		o, existed := before[n.Address]
		switch {
		case !existed:
			changes = append(changes, targetChange{kind: ChangeAdded, new: n})
		case o != n:
			changes = append(changes, targetChange{kind: ChangeChanged, old: o, new: n})
		}
	}
	for _, o := range prev {
		if !seen[o.Address] {
			changes = append(changes, targetChange{kind: ChangeRemoved, old: o})
		}
	}

	sort.Slice(changes, func(i, j int) bool {
		return changes[i].address() < changes[j].address()
	})
	return changes
}

// address returns the address the change refers to.
func (c targetChange) address() string {
	if c.kind == ChangeRemoved {
		return c.old.Address
	}
	return c.new.Address
}

// logAttrs returns the structured log attributes describing the change.
//
// Added and removed targets carry their full state. A changed target carries
// its address plus old_*/new_* pairs for exactly the fields that differ, so
// the log line says what changed without having to compare two records.
func (c targetChange) logAttrs() []any {
	switch c.kind {
	case ChangeAdded:
		return targetAttrs(c.kind, c.new)
	case ChangeRemoved:
		return targetAttrs(c.kind, c.old)
	}

	attrs := []any{"change", c.kind, "target", c.new.Address}
	for _, f := range []struct{ name, old, new string }{
		{"path", c.old.Path, c.new.Path},
		{"exporter", c.old.Exporter, c.new.Exporter},
		{"version", c.old.Version, c.new.Version},
		{"source", c.old.Source, c.new.Source},
	} {
		if f.old != f.new {
			attrs = append(attrs, "old_"+f.name, f.old, "new_"+f.name, f.new)
		}
	}
	return attrs
}

// targetAttrs returns the log attributes of a complete target.
func targetAttrs(kind string, t Target) []any {
	return []any{
		"change", kind,
		"target", t.Address,
		"path", t.Path,
		"exporter", t.Exporter,
		"version", t.Version,
		"source", t.Source,
	}
}
