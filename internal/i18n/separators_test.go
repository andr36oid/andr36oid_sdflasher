package i18n

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

// The interface uses plain hyphens and three dots: no middle dots (U+00B7), en
// dashes (U+2013), em dashes (U+2014), ellipsis characters (U+2026) or bullets
// (U+2022), neither in the English texts and translations nor in the strings the
// desktop app builds itself. Names that come as data (profile names) are changed
// when they are shown (ui.shown).
const banned = "\u00b7\u2013\u2014\u2026\u2022"

func TestNoDotsOrDashes(t *testing.T) {
	for code, m := range catalogs {
		for k, v := range m {
			if strings.ContainsAny(k, banned) || strings.ContainsAny(v, banned) {
				t.Errorf("%s: %q", code, v)
			}
		}
	}
	fset := token.NewFileSet()
	for _, dir := range []string{"internal/ui", "internal/platform", "internal/firmware", "internal/cli", "internal/flash", "internal/catalog", "internal/jobs", "cmd/andr36oid-sdflasher"} {
		files, _ := filepath.Glob(filepath.Join(repoRoot, dir, "*.go"))
		if len(files) == 0 {
			t.Fatalf("no sources in %s", dir)
		}
		for _, p := range files {
			if strings.HasSuffix(p, "_test.go") {
				continue
			}
			f, e := parser.ParseFile(fset, p, nil, 0)
			if e != nil {
				t.Fatal(e)
			}
			ast.Inspect(f, func(n ast.Node) bool {
				if s, ok := strLit(n); ok && strings.ContainsAny(s, banned) {
					t.Errorf("%s: %q", fset.Position(n.Pos()), s)
				}
				return true
			})
		}
	}
}
