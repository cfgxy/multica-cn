//go:build linux

package execenv

import "testing"

func TestMountOrderShallowBeforeDeep(t *testing.T) {
	t.Parallel()
	// Declaration order ["deep/child", root, "deep", sibling] — the nested
	// declaration shape a caller can produce (the save side rejects duplicate
	// subdirectories but not nesting). Mount order must be by target depth:
	// a parent overlay mounted after its child would shadow the child mount,
	// and the child unit's writes would land in the parent's upperdir.
	units := []SnapshotUnitSpec{
		{Target: "/data/repo/deep/nested/child"},
		{Target: "/data/repo"},
		{Target: "/data/repo/deep"},
		{Target: "/sibling"},
	}
	got := make([]string, 0, len(units))
	for _, i := range mountOrder(units) {
		got = append(got, units[i].Target)
	}
	want := []string{"/sibling", "/data/repo", "/data/repo/deep", "/data/repo/deep/nested/child"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("mount order = %v, want %v", got, want)
		}
	}
}

func TestMountOrderStableWithinDepth(t *testing.T) {
	t.Parallel()
	// Same-depth units keep declaration order — the sort only fixes the
	// parent/child hazard, it does not reorder the rest.
	units := []SnapshotUnitSpec{
		{Target: "/data/one"},
		{Target: "/data/two"},
		{Target: "/data/one/sub"},
	}
	got := make([]string, 0, len(units))
	for _, i := range mountOrder(units) {
		got = append(got, units[i].Target)
	}
	want := []string{"/data/one", "/data/two", "/data/one/sub"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("stable order = %v, want %v", got, want)
		}
	}
}

func TestMountOrderParentAlwaysBeforeChild(t *testing.T) {
	t.Parallel()
	units := []SnapshotUnitSpec{
		{Target: "/r/a/b/c"},
		{Target: "/r"},
		{Target: "/r/a"},
		{Target: "/r/x"},
		{Target: "/r/a/b"},
	}
	order := mountOrder(units)
	position := make(map[string]int, len(units))
	for pos, i := range order {
		position[units[i].Target] = pos
	}
	for _, pair := range [][2]string{
		{"/r", "/r/a"},
		{"/r/a", "/r/a/b"},
		{"/r/a/b", "/r/a/b/c"},
		{"/r", "/r/x"},
	} {
		if position[pair[0]] >= position[pair[1]] {
			t.Errorf("parent %s (pos %d) must mount before child %s (pos %d)",
				pair[0], position[pair[0]], pair[1], position[pair[1]])
		}
	}
}

func TestMountOrderEmpty(t *testing.T) {
	t.Parallel()
	if got := mountOrder(nil); len(got) != 0 {
		t.Fatalf("no units → no order, got %v", got)
	}
}
