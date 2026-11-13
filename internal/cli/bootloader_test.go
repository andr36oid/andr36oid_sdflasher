package cli

import (
	"bufio"
	"bytes"
	"github.com/andr36oid/andr36oid-sdflasher/internal/firmware"
	"strings"
	"testing"
)

func TestBootloaderFlags(t *testing.T) {
	for _, c := range []string{"image", "standard", "new"} {
		v, e := cliBootloader(opts{bootloader: c})
		if e != nil || v.Choice != c {
			t.Fatal(c, e)
		}
	}
	if _, e := cliBootloader(opts{bootloader: "bad"}); e == nil {
		t.Fatal("bad accepted")
	}
	if _, e := cliBootloader(opts{bootloader: "new", bootloaderFile: "x"}); e == nil {
		t.Fatal("both accepted")
	}
	v, e := cliBootloader(opts{bootloaderFile: "x"})
	if e != nil || v.Choice != "file" {
		t.Fatal(e)
	}
}

func TestDTBScreenAckIsIndependentOfGenericYes(t *testing.T) {
	if cliConfirmDTBScreen(opts{assumeYes: true, in: bufio.NewReader(strings.NewReader("n\n"))}, true) {
		t.Fatal("generic yes bypassed screen acknowledgment")
	}
	if !cliConfirmDTBScreen(opts{in: bufio.NewReader(strings.NewReader("y\n"))}, true) {
		t.Fatal("explicit acknowledgment refused")
	}
	if !cliConfirmDTBScreen(opts{screenAccepted: true}, true) {
		t.Fatal("explicit script flag refused")
	}
	if !cliConfirmDTBScreen(opts{}, false) {
		t.Fatal("unchanged screen asked for input")
	}
}

func TestBootChainFlags(t *testing.T) {
	for _, o := range []opts{{bootChain: "mainline", sleepFirmware: "vendor"}, {bootChain: "unknown"}, {bootChain: "mainline", bootloader: "new"}} {
		if _, e := cliBootloader(o); e == nil {
			t.Fatal("impossible accepted", o)
		}
	}
	c, e := cliBootloader(opts{bootChain: "mainline", forceBootOptions: true})
	if e != nil || c.ChainID() != "mainline" || !c.Force {
		t.Fatal(c, e)
	}
	c, e = cliBootloader(opts{sleepFirmware: "vendor"})
	if e != nil || c.SleepID() != "vendor" || c.LoaderID() != "image" {
		t.Fatal(c, e)
	}
}

func TestCLIBootWarningAndRefusalLevels(t *testing.T) {
	target := firmware.BootTarget{Storage: "sd", ImageAvailable: true, Console: "Unknown"}
	c := firmware.BootloaderChoice{Choice: "new"}
	var w bytes.Buffer
	rules := firmware.BootRules()
	if e := cliBootIssues(c, target, rules, &w); e != nil || !strings.Contains(w.String(), "Warning: Not tested on this console yet.") {
		t.Fatal(w.String(), e)
	}
	rules[1].Broken = []firmware.BootBrokenPair{{Field: "storage", Value: "sd", Reason: "Fixture broken pair"}}
	w.Reset()
	if e := cliBootIssues(c, target, rules, &w); e == nil || w.Len() != 0 {
		t.Fatal("known bad pair not refused before progress", e)
	}
	c.Force = true
	if e := cliBootIssues(c, target, rules, &w); e != nil {
		t.Fatal(e)
	}
	target.Storage = "emmc"
	if e := cliBootIssues(c, target, rules, &w); e == nil {
		t.Fatal("force bypassed impossible target")
	}
}
