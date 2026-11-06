package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/andr36oid/andr36oid-sdflasher/internal/firmware"
)

func TestRequested(t *testing.T) {
	t.Setenv("A36_UI", "")
	for _, tc := range []struct {
		args []string
		want bool
	}{
		{nil, false},
		{[]string{"-image", "x.img"}, false},
		{[]string{"-cli"}, true},
		{[]string{"--cli", "-action", "hotfix"}, true},
	} {
		if got := Requested(tc.args); got != tc.want {
			t.Errorf("Requested(%v) = %v, want %v", tc.args, got, tc.want)
		}
	}
	t.Setenv("A36_UI", "cli")
	if !Requested(nil) {
		t.Error("A36_UI=cli should request text mode")
	}
}

func TestStripCLIFlag(t *testing.T) {
	got := stripCLIFlag([]string{"-cli", "-image", "a.img", "--cli", "--started-elevated", "-y"})
	want := []string{"-image", "a.img", "-y"}
	if len(got) != len(want) {
		t.Fatalf("stripCLIFlag = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("stripCLIFlag = %v, want %v", got, want)
		}
	}
}

func offer() *firmware.HotfixOffer {
	return &firmware.HotfixOffer{
		Build: "b",
		Hotfixes: []firmware.Hotfix{
			{File: "fix1.zip", Title: "One", Type: "bugfix", Build: "b"},
			{File: "extra2.zip", Title: "Two", Type: "extra", Build: "b"},
			{File: "fix3.zip", Title: "Three", Type: "bugfix", Build: "b"},
		},
	}
}

func TestPreselectedHotfixes(t *testing.T) {
	got := preselectedHotfixes(offer())
	if len(got) != 2 || got[0] != "fix1.zip" || got[1] != "fix3.zip" {
		t.Fatalf("preselectedHotfixes = %v, want [fix1.zip fix3.zip]", got)
	}
}

func TestParseHotfixSelection(t *testing.T) {
	o := offer()
	for _, tc := range []struct {
		line string
		ok   bool
		want []string
	}{
		{"1 3", true, []string{"fix1.zip", "fix3.zip"}},
		{"2", true, []string{"extra2.zip"}},
		{"3 1 1", true, []string{"fix1.zip", "fix3.zip"}}, // list order, deduped
		{"", true, nil},                                   // no tokens -> empty selection
		{"0", false, nil},
		{"4", false, nil},
		{"x", false, nil},
	} {
		got, ok := parseHotfixSelection(tc.line, o)
		if ok != tc.ok {
			t.Errorf("parseHotfixSelection(%q) ok = %v, want %v", tc.line, ok, tc.ok)
			continue
		}
		if !ok {
			continue
		}
		if len(got) != len(tc.want) {
			t.Errorf("parseHotfixSelection(%q) = %v, want %v", tc.line, got, tc.want)
			continue
		}
		for i := range tc.want {
			if got[i] != tc.want[i] {
				t.Errorf("parseHotfixSelection(%q) = %v, want %v", tc.line, got, tc.want)
				break
			}
		}
	}
}

func TestResolveRecovery(t *testing.T) {
	dir := t.TempDir()
	rec := filepath.Join(dir, "recovery")
	if err := os.Mkdir(rec, 0700); err != nil {
		t.Fatal(err)
	}
	jsonPath := filepath.Join(rec, "recovery.json")
	if err := os.WriteFile(jsonPath, []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}

	// The folder that holds recovery.json resolves to itself.
	if got, err := resolveRecovery(nil, rec); err != nil || got != rec {
		t.Errorf("resolveRecovery(folder) = %q, %v; want %q", got, err, rec)
	}
	// The recovery.json file resolves to its folder.
	if got, err := resolveRecovery(nil, jsonPath); err != nil || got != rec {
		t.Errorf("resolveRecovery(file) = %q, %v; want %q", got, err, rec)
	}
	// A folder without recovery.json is rejected.
	if _, err := resolveRecovery(nil, dir); err == nil {
		t.Error("resolveRecovery on a folder without recovery.json should fail")
	}
	// A wrong file name is rejected.
	other := filepath.Join(dir, "something.json")
	if err := os.WriteFile(other, []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveRecovery(nil, other); err == nil {
		t.Error("resolveRecovery on a non-recovery.json file should fail")
	}
	// A missing path is rejected.
	if _, err := resolveRecovery(nil, filepath.Join(dir, "nope")); err == nil {
		t.Error("resolveRecovery on a missing path should fail")
	}
}
