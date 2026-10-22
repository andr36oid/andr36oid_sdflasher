package catalog

import (
	"testing"
	"time"
)

func TestUsableSkipsOldBuilds(t *testing.T) {
	day := func(s string) time.Time { d, _ := time.Parse("2006-01-02", s); return d }
	got := usable([]release{
		{Tag: "20250902", Published: day("2025-09-02"), Assets: []Asset{{Name: "lineage-18.1-20250902-2052-r36s-android.img.zip"}}},
		{Tag: "rc2", Published: day("2026-10-21"), Assets: []Asset{{Name: "andr36oid-20261021-0354-rk3326.img.zip"}}},
		{Tag: "rc3", Published: day("2026-10-22"), Assets: []Asset{
			{Name: "andr36oid-20261022-1200-rk3326.img.xz"},
			{Name: "lineage-18.1-20251109-0900-r46h-android.img.zip"},
			{Name: "notes.txt"},
		}},
		{Tag: "draft", Draft: true, Published: day("2026-10-23"), Assets: []Asset{{Name: "andr36oid-20261023-0900-rk3326.img.xz"}}},
	})
	if len(got) != 1 || got[0].Name != "andr36oid-20261022-1200-rk3326.img.xz" || got[0].Release != "rc3" {
		t.Fatalf("got %+v", got)
	}
}

func TestUsablePrefersXZ(t *testing.T) {
	day := func(s string) time.Time { d, _ := time.Parse("2006-01-02", s); return d }
	got := usable([]release{
		{Tag: "rc3", Published: day("2026-10-22"), Assets: []Asset{
			{Name: "andr36oid-20261022-1200-rk3326.img.zip"},
			{Name: "andr36oid-20261022-1200-rk3326.img.xz"},
			{Name: "andr36oid-20261022-1200-rk3326.img.xz.sha256"},
		}},
		{Tag: "rc4", Published: day("2026-10-23"), Assets: []Asset{{Name: "andr36oid-20261023-0900-rk3326.img.zip"}}},
	})
	if len(got) != 2 || got[0].Name != "andr36oid-20261022-1200-rk3326.img.xz" || got[1].Name != "andr36oid-20261023-0900-rk3326.img.zip" {
		t.Fatalf("got %+v", got)
	}
}
