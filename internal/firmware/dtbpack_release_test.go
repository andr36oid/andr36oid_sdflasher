package firmware

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
)

func dtbAPIAsset(name, url string, b []byte) dtbAsset {
	h := sha256.Sum256(b)
	return dtbAsset{Name: name, URL: url, Size: int64(len(b)), Digest: "sha256:" + hex.EncodeToString(h[:])}
}
func TestDTBReleaseManifestWinsAndSurvivesHandoff(t *testing.T) {
	p := packFixture(t)
	zipFile := packZip(t, p, nil)
	zipBytes, _ := os.ReadFile(zipFile)
	m := p.Manifest
	m.Revision = 2
	manifest, _ := json.Marshal(m)
	tag := "test-build"
	prefix := "https://github.com/" + DTBBetaRepository + "/releases/download/" + tag + "/"
	release := dtbRelease{Tag: tag, Assets: []dtbAsset{dtbAPIAsset("manifest.json", prefix+"manifest.json", manifest), dtbAPIAsset("build-dtbs.zip", prefix+"build-dtbs.zip", zipBytes)}}
	api, _ := json.Marshal([]dtbRelease{release})
	old := hotfixHTTP
	defer func() { hotfixHTTP = old }()
	var requests []string
	hotfixHTTP = &http.Client{Transport: loaderTransport(func(req *http.Request) (*http.Response, error) {
		requests = append(requests, req.URL.String())
		var b []byte
		switch {
		case req.URL.String() == "https://api.github.com/repos/"+DTBBetaRepository+"/releases?per_page=100":
			b = api
		case req.URL.String() == "https://api.github.com/repos/"+DTBReleaseRepository+"/releases?per_page=100":
			b = []byte("[]")
		case req.URL.String() == prefix+"manifest.json":
			b = manifest
		case req.URL.String() == prefix+"build-dtbs.zip":
			b = zipBytes
		default:
			t.Fatalf("unexpected request %s", req.URL)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(b)), Header: make(http.Header)}, nil
	})}
	setup := CardSetup{Release: p.Manifest.Version, Build: p.Manifest.Version + "|" + p.Manifest.CompatibleBuilds[0], KernelLine: "4.19"}
	got, e := FetchDTBPack(context.Background(), setup, t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	if got.Manifest.Revision != 2 || got.Source.Repository != DTBBetaRepository {
		t.Fatal(got.Manifest, got.Source)
	}
	again, e := ReadDTBPackReference(got.Path, got.ManifestPath)
	if e != nil || again.Manifest.Revision != 2 {
		t.Fatal("fetched manifest lost across worker handoff", e)
	}
	manifestIndex, zipIndex := -1, -1
	for n, r := range requests {
		if strings.HasSuffix(r, "manifest.json") {
			manifestIndex = n
		}
		if strings.HasSuffix(r, "-dtbs.zip") {
			zipIndex = n
		}
	}
	if manifestIndex < 0 || zipIndex < manifestIndex {
		t.Fatal("zip fetched before manifest", requests)
	}
}
