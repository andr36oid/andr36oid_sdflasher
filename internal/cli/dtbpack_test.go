package cli

import (
	"bufio"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/andr36oid/andr36oid-sdflasher/internal/firmware"
	"github.com/andr36oid/andr36oid-sdflasher/internal/flash"
	"github.com/andr36oid/andr36oid-sdflasher/internal/jobs"
	"github.com/andr36oid/andr36oid-sdflasher/internal/platform"
)

func TestDTBUndoJobsAndConfirmation(t *testing.T) {
	for _, tc := range []struct {
		name              string
		changed, yes, ack bool
		input             string
		fail              string
		malformed         string
		want              int
		actions           []string
	}{
		{name: "changed with explicit ack", changed: true, yes: true, ack: true, actions: []string{"dtb-info", "dtb-restore-plan", "dtb-restore"}},
		{name: "unchanged screen needs no ack", yes: true, actions: []string{"dtb-info", "dtb-restore-plan", "dtb-restore"}},
		{name: "generic yes cannot skip screen ack", changed: true, yes: true, input: "n\n", actions: []string{"dtb-info", "dtb-restore-plan"}},
		{name: "normal confirmation cancelled", input: "n\n", actions: []string{"dtb-info", "dtb-restore-plan"}},
		{name: "both interactive confirmations accepted", changed: true, input: "y\ny\n", actions: []string{"dtb-info", "dtb-restore-plan", "dtb-restore"}},
		{name: "info error", yes: true, fail: "dtb-info", want: 1, actions: []string{"dtb-info"}},
		{name: "plan error", yes: true, fail: "dtb-restore-plan", want: 1, actions: []string{"dtb-info", "dtb-restore-plan"}},
		{name: "restore error", yes: true, fail: "dtb-restore", want: 1, actions: []string{"dtb-info", "dtb-restore-plan", "dtb-restore"}},
		{name: "missing fingerprint", yes: true, malformed: "fingerprint", want: 1, actions: []string{"dtb-info"}},
		{name: "missing card", yes: true, malformed: "card", want: 1, actions: []string{"dtb-info"}},
		{name: "missing build", yes: true, malformed: "build", want: 1, actions: []string{"dtb-info"}},
		{name: "missing plan", yes: true, malformed: "plan", want: 1, actions: []string{"dtb-info", "dtb-restore-plan"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			drive := platform.Drive{Path: "file-fixture", Size: 4 << 30, SectorSize: 512}
			var actions []string
			run := func(req jobs.Request, progress func(flash.Progress)) (jobs.Result, error) {
				actions = append(actions, req.Action)
				if !reflect.DeepEqual(req.Drive, drive) {
					t.Fatal("wrong drive", req.Drive)
				}
				if req.Action == tc.fail {
					return jobs.Result{}, errors.New("injected " + req.Action)
				}
				switch req.Action {
				case "dtb-info":
					info := jobs.Result{BuildIdentity: "vtest|eng.fixture", Card: &flash.Card{Fingerprint: "fingerprint"}, Setup: &firmware.CardSetup{History: []string{"00000000000000000001"}}}
					switch tc.malformed {
					case "fingerprint":
						info.Card.Fingerprint = ""
					case "card":
						info.Card = nil
					case "build":
						info.BuildIdentity = ""
					}
					return info, nil
				case "dtb-restore-plan", "dtb-restore":
					if req.DTBExpected != "vtest|eng.fixture" || req.CardFingerprint != "fingerprint" || req.DTBFile != "" || req.DTBOnline {
						t.Fatal("restore not bound to inspected card", req)
					}
					if req.Action == "dtb-restore-plan" {
						if progress != nil {
							t.Fatal("plan should not write")
						}
						if tc.malformed == "plan" {
							return jobs.Result{}, nil
						}
						return jobs.Result{DTBPlan: &firmware.DTBPlan{ScreenChanged: tc.changed}}, nil
					}
					if req.DTBOptions.ScreenAccepted != tc.changed || progress == nil {
						t.Fatal("missing screen acknowledgement or progress")
					}
					return jobs.Result{DTBResult: &firmware.DTBResult{}}, nil
				}
				t.Fatal("unexpected action", req.Action)
				return jobs.Result{}, nil
			}
			got := undoDTBOnDrive(opts{assumeYes: tc.yes, screenAccepted: tc.ack, in: bufio.NewReader(strings.NewReader(tc.input))}, drive, run)
			if got != tc.want || !reflect.DeepEqual(actions, tc.actions) {
				t.Fatal(got, actions, "want", tc.want, tc.actions)
			}
		})
	}
}

func TestDTBUndoWithoutHistoryNeverPlansOrWrites(t *testing.T) {
	for _, setup := range []*firmware.CardSetup{nil, {}, {History: []string{}}} {
		calls := 0
		code := undoDTBOnDrive(opts{assumeYes: true, screenAccepted: true}, platform.Drive{}, func(req jobs.Request, _ func(flash.Progress)) (jobs.Result, error) {
			calls++
			if req.Action != "dtb-info" {
				t.Fatal("write or plan without saved set", req.Action)
			}
			return jobs.Result{Setup: setup}, nil
		})
		if code != 1 || calls != 1 {
			t.Fatal("no-history refusal", code, calls)
		}
	}
}

func TestDTBUndoFlagMisuseRefusedBeforeHelper(t *testing.T) {
	for _, args := range [][]string{{"--undo"}, {"-action", "flash", "--undo"}, {"-action", "dtbs"}, {"-action", "dtbs", "--undo", "unexpected"}} {
		if code := run("test", args, func() {}); code != 2 {
			t.Fatal(args, code)
		}
	}
	if code := runDTBs(opts{}, []string{"undo", "one", "two"}); code != 2 {
		t.Fatal("extra undo paths", code)
	}
	if code := runDTBs(opts{drive: "one"}, []string{"undo", "two"}); code != 2 {
		t.Fatal("conflicting undo drive", code)
	}
}
