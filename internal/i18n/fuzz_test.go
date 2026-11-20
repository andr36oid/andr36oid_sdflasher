package i18n

import (
	"strings"
	"testing"
)

// Whatever text an error has, finding its message never panics, and the explanation
// never shows a broken formatting verb.
func FuzzExplain(f *testing.F) {
	for _, s := range []string{"write /dev/sdb: input/output error", "Not enough free space in /tmp/c: 9.9 GB needed, 1.0 GB free. Free up space on that disk, then try again.", "%s %d %!", "Not enough free space: %s needed"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		for _, l := range []string{"en", "de", "ja"} {
			got := Explain(l, raw)
			if strings.Contains(got, "%!") && !strings.Contains(raw, "%!") {
				t.Fatalf("%q explained as %q", raw, got)
			}
		}
	})
}
