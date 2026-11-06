package discovery

import (
	"reflect"
	"testing"
)

func TestDiffTargets(t *testing.T) {
	a := Target{Address: "10.0.0.1:9100", Path: "/metrics", Exporter: "node", Version: "1.7.0", Source: sourceBuildInfo}
	b := Target{Address: "10.0.0.2:9115", Path: "/metrics", Exporter: "blackbox", Source: sourceBuildInfo}
	aUpgraded := a
	aUpgraded.Version = "1.8.0"

	cases := []struct {
		name       string
		prev, next []Target
		want       []targetChange
	}{
		{name: "both empty", want: nil},
		{name: "unchanged", prev: []Target{a, b}, next: []Target{a, b}, want: nil},
		{name: "order does not matter", prev: []Target{a, b}, next: []Target{b, a}, want: nil},
		{name: "added", prev: []Target{a}, next: []Target{a, b},
			want: []targetChange{{kind: ChangeAdded, new: b}}},
		{name: "removed", prev: []Target{a, b}, next: []Target{a},
			want: []targetChange{{kind: ChangeRemoved, old: b}}},
		// An upgrade is one change, not a removal plus an addition: otherwise
		// every exporter rollout would look like services vanishing.
		{name: "version upgrade is a change", prev: []Target{a}, next: []Target{aUpgraded},
			want: []targetChange{{kind: ChangeChanged, old: a, new: aUpgraded}}},
		{name: "everything at once, ordered by address", prev: []Target{b, a}, next: []Target{aUpgraded},
			want: []targetChange{
				{kind: ChangeChanged, old: a, new: aUpgraded},
				{kind: ChangeRemoved, old: b},
			}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := diffTargets(tc.prev, tc.next); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("diffTargets() =\n  %+v\nwant\n  %+v", got, tc.want)
			}
		})
	}
}

// TestChangedTargetLogsOnlyTheDifference: a "changed" line must say what
// changed. Logging both full records would leave the reader to diff them.
func TestChangedTargetLogsOnlyTheDifference(t *testing.T) {
	old := Target{Address: "10.0.0.1:9100", Path: "/metrics", Exporter: "node", Version: "1.7.0", Source: sourceBuildInfo}
	upgraded := old
	upgraded.Version = "1.8.0"

	got := targetChange{kind: ChangeChanged, old: old, new: upgraded}.logAttrs()
	want := []any{"change", "changed", "target", "10.0.0.1:9100", "old_version", "1.7.0", "new_version", "1.8.0"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("logAttrs() = %v, want %v", got, want)
	}
}
