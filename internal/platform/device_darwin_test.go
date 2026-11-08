package platform

import "testing"

func TestWholeDisk(t *testing.T) {
	// Keys as "diskutil info -plist" prints them for a card and for one of its partitions.
	card := map[string]any{"WholeDisk": true, "Writable": true, "Ejectable": true, "RemovableMediaOrExternalDevice": true}
	part := map[string]any{"WholeDisk": false, "Writable": true, "Ejectable": true, "RemovableMediaOrExternalDevice": true}
	if !wholeDisk(card) {
		t.Fatal("whole disk with WholeDisk=true not recognised")
	}
	if wholeDisk(part) {
		t.Fatal("partition with WholeDisk=false taken for a whole disk")
	}
	if !wholeDisk(map[string]any{"Whole": true}) {
		t.Fatal("legacy Whole key not honoured")
	}
	if wholeDisk(map[string]any{}) {
		t.Fatal("disk without either key taken for a whole disk")
	}
}
