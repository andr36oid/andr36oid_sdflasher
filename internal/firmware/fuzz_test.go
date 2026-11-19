package firmware

import (
	"strings"
	"testing"
)

// The parsers of what images, cards and the hotfix and bootloader repositories hand
// the flasher: whatever the bytes, an answer or an error, never a panic.

func FuzzProfileList(f *testing.F) {
	f.Add([]byte("Panels/Panel4|R36S|Panel 4|experimental,also=R36H;RG351MP\n# comment\n\r\n"))
	f.Add([]byte("|||\n/|x|y\nPanels/Panel4|R36S\r\nA|B|C|also=;;\n"))
	f.Fuzz(func(t *testing.T, b []byte) {
		for dir, p := range parseProfileList(b) {
			if dir == "" || strings.HasPrefix(dir, "/") || strings.HasSuffix(dir, "/") {
				t.Fatalf("folder %q from %q", dir, b)
			}
			for _, c := range p.AlsoFor {
				if strings.TrimSpace(c) == "" {
					t.Fatal("empty console")
				}
			}
		}
	})
}

func FuzzHotfixIndex(f *testing.F) {
	f.Add([]byte(`{"format":1,"hotfixes":[{"id":"a","build":"any","type":"bugfix","file":"a.fix.zip","size":5,"sha256":"00"}]}`))
	f.Add([]byte(`{"hotfixes":null}`))
	f.Add([]byte(`[]`))
	f.Fuzz(func(t *testing.T, b []byte) {
		index, e := ParseHotfixIndex(b)
		if e == nil && index == nil {
			t.Fatal("no index and no error")
		}
		if e == nil {
			index.ForBuild("andr36oid 2026-11-19|eng.kenny.20261119.021722")
			index.ForBuild("")
		}
	})
}

func FuzzBuildInfo(f *testing.F) {
	f.Add([]byte("version=v2026-11-19\nincremental=eng.kenny.20261119.021722\n"))
	f.Add([]byte("=\n==\nversion\r\n"))
	f.Fuzz(func(t *testing.T, b []byte) { ParseBuildInfo(b) })
}

func FuzzBootCatalog(f *testing.F) {
	f.Add([]byte(`{"format":1}`))
	f.Add([]byte(`{"format":1,"loaders":[{}],"rules":[{}]}`))
	f.Fuzz(func(t *testing.T, b []byte) {
		c, e := ParseBootCatalog(b)
		if e == nil && c == nil {
			t.Fatal("no catalog and no error")
		}
	})
}

func FuzzDTBManifest(f *testing.F) {
	f.Add([]byte(`{"format":1,"build":"x","files":{}}`))
	f.Add([]byte(`{"files":{"../a":{}}}`))
	f.Fuzz(func(t *testing.T, b []byte) { decodeDTBManifest(b) })
}
