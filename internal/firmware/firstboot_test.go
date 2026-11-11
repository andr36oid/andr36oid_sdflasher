package firmware

import (
	"testing"

	"github.com/andr36oid/andr36oid-sdflasher/internal/layout"
)

func TestCardFirstBoot(t *testing.T) {
	const id = "v2026-11-20-release|eng.kenny.20261120.120000"
	with := func(tab layout.Table, names ...string) layout.Table {
		vendor, _ := tab.Find("vendor")
		for i, n := range names {
			q := vendor
			q.Name, q.Number = n, 20+i
			tab.Parts = append(tab.Parts, q)
		}
		return tab
	}
	prepared := testBuildInfo + "layout=prepared-1\n"
	for _, c := range []struct {
		name          string
		files         map[string]string
		parts         []string
		done, started bool
	}{
		{"old release, never started", map[string]string{BuildInfoFile: testBuildInfo}, []string{"AUTOAPK"}, false, false},
		{"old release, apps not installed yet", map[string]string{BuildInfoFile: testBuildInfo}, []string{"userdata", "AUTOAPK"}, false, true},
		{"old release, set up", map[string]string{BuildInfoFile: testBuildInfo}, []string{"userdata"}, true, true},
		{"prepared, never started", map[string]string{BuildInfoFile: prepared}, []string{"userdata", "AUTOAPK"}, false, false},
		{"prepared, apps gone but no record", map[string]string{BuildInfoFile: prepared}, []string{"userdata"}, false, false},
		{"prepared, set up", map[string]string{BuildInfoFile: prepared, FirstBootFile: "done=1\nbuild=" + id + "\nlayout=prepared-1\n"}, []string{"userdata"}, true, true},
		{"record of another build", map[string]string{BuildInfoFile: prepared, FirstBootFile: "done=1\nbuild=other|x\n"}, []string{"userdata"}, false, true},
		{"record not done", map[string]string{BuildInfoFile: prepared, FirstBootFile: "done=0\nbuild=" + id + "\n"}, []string{"userdata"}, false, true},
	} {
		f, tab := bootOnlyImage(t, c.files)
		got := CardFirstBoot(f, with(tab, c.parts...), id)
		if got.Done != c.done || got.Started != c.started {
			t.Fatalf("%s: %+v", c.name, got)
		}
	}
}
