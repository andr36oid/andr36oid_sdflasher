package dtbconv

import (
	"bytes"
	"compress/gzip"
	"embed"
	"io"
)

// The base DTBs come from the andr36oid kernel and device trees.
// scripts/update-dtbconv-bases.sh rebuilds them; bases/SOURCES.txt says
// which kernel commit they were built from.
//
//go:embed bases/*.dtb.gz
var baseFiles embed.FS

func readBase(name string) []byte {
	f, e := baseFiles.Open("bases/" + name + ".dtb.gz")
	if e != nil {
		panic(e)
	}
	defer f.Close()
	z, e := gzip.NewReader(f)
	if e != nil {
		panic(e)
	}
	var b bytes.Buffer
	if _, e := io.Copy(&b, z); e != nil {
		panic(e)
	}
	return b.Bytes()
}

// EmbeddedBases returns the bases built into the program.
func EmbeddedBases() Bases {
	return Bases{Genuine: readBase("r36s"), EE: readBase("ee"), Soy: readBase("soy"), UBoot: readBase("uboot")}
}

// BaseProfiles are the BOOT folders of a release image that hold the same
// DTBs as the embedded bases. The flasher prefers these, so the result
// matches the kernel of the release it writes.
var BaseProfiles = map[Family]string{Genuine: "Panels/Panel4", EE: "Experimental/EE-G80-Panel8", Soy: "Experimental/Soy-Panel5"}
