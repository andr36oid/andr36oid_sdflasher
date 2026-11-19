package ui

import (
 "testing"
 "fyne.io/fyne/v2/test"
 "github.com/andr36oid/andr36oid-sdflasher/internal/firmware"
)

func TestOfflineImageMainlinePair(t *testing.T) {
 a:=test.NewApp(); defer a.Quit()
 s:=&screen{locale:"en"}
 p:=s.newBootloaderPicker(firmware.BootloaderChoice{Choice:"image",Chain:"mainline",Sleep:"image"},nil)
 p.cardTarget=true
 p.failed=true
 p.imageMainline=true
 p.target=firmware.BootTarget{Storage:"sd",KernelLine:"5.10",Console:"R36 Ultra"}
 p.setTarget(true,false)
 assertMainline:=func(allowed bool){
  t.Helper();got:=p.selection();if (got!=nil)!=allowed{t.Fatalf("offline image selection: got %+v, allowed %v",got,allowed)}
  var reason string;for _,o:=range p.controls[2].options{if o.id=="mainline"{reason=o.reason}}
  if (reason=="")!=allowed{t.Fatalf("mainline row: %q, allowed %v",reason,allowed)}
  if allowed&&(got.Build!=nil||got.LoaderID()!="image"||got.SleepID()!="image"){t.Fatal("used an online offer instead of the image pair")}
 }
 assertMainline(true)
 p.imageMainline=false;p.refresh();assertMainline(false)
 p.imageMainline=true;p.setTarget(false,false);assertMainline(false)
 p.setTarget(true,false);assertMainline(true)
 p.choice.Chain="standard";p.choice.Choice="new";p.refresh();if p.selection()!=nil{t.Fatal("download choice enabled offline")}
}
