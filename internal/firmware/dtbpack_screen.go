package firmware

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
)

const DTBScreenWarningTitle = "Watch the screen at the first start"
const DTBScreenWarning = "This changes the files that drive the console's screen. If they don't fit this console, the picture can come up wrong: stripes, a washed-out or bleeding picture, or flicker. Left on like that, it can leave lasting marks on the screen.\n\nIf you see that, switch the console off at once: hold the power button until the screen goes dark. If that doesn't work, press the reset button and switch it off before it starts again. Then put the card back into this computer and choose \"Put the previous files back\"."
const DTBScreenSuccess = "Put the card back into the console and watch the screen while it starts. If the picture looks wrong, switch the console off at once, then open this screen again and choose \"Put the previous files back\"."

var ErrDTBScreenConfirmation = errors.New("screen change needs confirmation")

func PlanRestoreDTB(disk HotfixDisk, size int64, expected, dir string) (*DTBPlan, error) {
	stage, e := stageSetup(disk, size, expected, dir)
	if e != nil {
		return nil, e
	}
	defer stage.Close()
	setup, e := ReadCardSetup(stage.boot)
	if e != nil {
		return nil, e
	}
	if len(setup.History) == 0 {
		return nil, errors.New("no previous console files on this card")
	}
	id := setup.History[len(setup.History)-1]
	if !historyID.MatchString(id) {
		return nil, errors.New("invalid history entry")
	}
	directory := SetupHistoryDir + "/" + id
	b, e := ReadFile(stage.boot, "/"+directory+"/setup.json", 1<<20)
	if e != nil {
		return nil, e
	}
	var previous CardSetup
	if e = json.Unmarshal(b, &previous); e != nil || previous.Build != setup.Build {
		return nil, errors.New("previous files belong to another build")
	}
	plan := &DTBPlan{Setup: setup, Profile: previous.Profile}
	for _, n := range append(append([]string{}, DTBRootFiles...), PictureFiles...) {
		b, e := ReadFile(stage.boot, "/"+directory+"/"+n, 16<<20)
		if e != nil {
			return nil, e
		}
		if !bytes.Equal(b, dtbRead(stage.boot, n, 16<<20)) {
			plan.ScreenChanged = true
			plan.Changed = true
		}
	}
	return plan, nil
}
func screenChangeInFiles(bootFiles map[string][]byte, name string) bool {
	if strings.Contains(name, "/") {
		return false
	}
	for _, n := range append(append([]string{}, DTBRootFiles...), PictureFiles...) {
		if n == name {
			_, ok := bootFiles[n]
			return ok
		}
	}
	return false
}
