package cli

import (
	"bufio"
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
