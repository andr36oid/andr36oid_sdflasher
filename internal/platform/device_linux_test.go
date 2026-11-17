package platform

import (
	"encoding/json"
	"testing"
)

// A card with its lock switch on is listed as write-protected, a USB hard drive as
// external; a read-only fixed disk and tiny disks are not listed.
func TestLinuxDrives(t *testing.T) {
	raw := `[
	{"name":"sdx1","path":"/dev/sdx1","type":"disk","size":31914983424,"model":"Card Reader","serial":"1","rm":true,"ro":true,"tran":"usb","log-sec":512,"mountpoints":[null]},
	{"name":"sdx2","path":"/dev/sdx2","type":"disk","size":2000398934016,"model":"Expansion HDD","serial":"2","rm":false,"ro":false,"tran":"usb","log-sec":512,"mountpoints":[null]},
	{"name":"sdx3","path":"/dev/sdx3","type":"disk","size":63864569856,"model":"SD Reader","serial":"3","rm":true,"ro":false,"tran":"usb","log-sec":512,"mountpoints":[null]},
	{"name":"sdx4","path":"/dev/sdx4","type":"disk","size":500107862016,"model":"Internal SSD","serial":"4","rm":false,"ro":true,"tran":"sata","log-sec":512,"mountpoints":[null]},
	{"name":"sdx5","path":"/dev/sdx5","type":"disk","size":536870912,"model":"Tiny","serial":"5","rm":true,"ro":false,"tran":"usb","log-sec":512,"mountpoints":[null]}
	]`
	var bs []block
	if e := json.Unmarshal([]byte(raw), &bs); e != nil {
		t.Fatal(e)
	}
	got := map[string]Drive{}
	for _, d := range drives(bs, true) {
		got[d.Name] = d
	}
	if len(got) != 3 {
		t.Fatalf("listed %v", got)
	}
	if d := got["Card Reader"]; !d.WriteProtected || d.External || d.Large() {
		t.Errorf("locked card: %+v", d)
	}
	if d := got["Expansion HDD"]; !d.External || d.WriteProtected || !d.Large() {
		t.Errorf("USB hard drive: %+v", d)
	}
	if d := got["SD Reader"]; d.WriteProtected || d.External || d.Large() {
		t.Errorf("card: %+v", d)
	}
}
