package firmware

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type loaderTransport func(*http.Request) (*http.Response, error)

func (f loaderTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func bootloaderHTTP(t *testing.T, repo string, data []byte, edit func(map[string]any), badSum bool) *int {
	t.Helper()
	calls := new(int)
	sum := sha256.Sum256(data)
	digest := hex.EncodeToString(sum[:])
	if badSum {
		digest = strings.Repeat("0", 64)
	}
	base := "https://github.com/" + repo + "/releases/download/20261113-0524/"
	release := map[string]any{"tag_name": "20261113-0524", "assets": []map[string]any{{"name": "uboot.img", "size": BootloaderSize, "browser_download_url": base + "uboot.img"}, {"name": "SHA256SUMS", "size": 80, "browser_download_url": base + "SHA256SUMS"}}}
	if edit != nil {
		edit(release)
	}
	raw, _ := json.Marshal(release)
	previous := hotfixHTTP
	hotfixHTTP = &http.Client{Transport: loaderTransport(func(r *http.Request) (*http.Response, error) {
		*calls++
		if r.Header.Get("Authorization") != "" || r.URL.RawQuery != "" || r.Header.Get("User-Agent") != "andr36oid-sdflasher" {
			t.Error("unexpected request information")
		}
		var b []byte
		switch r.URL.String() {
		case "https://api.github.com/repos/" + repo + "/releases/latest":
			b = raw
		case base + "SHA256SUMS":
			b = []byte(digest + "  uboot.img\n")
		case base + "uboot.img":
			b = data
		default:
			return nil, errors.New("unexpected URL")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(b)), Header: make(http.Header)}, nil
	})}
	t.Cleanup(func() { hotfixHTTP = previous })
	return calls
}
func TestBootloaderReleaseDownloadAndCache(t *testing.T) {
	for _, choice := range []string{"standard", "new"} {
		t.Run(choice, func(t *testing.T) {
			repo, _, _ := bootloaderRepository(choice)
			data := testBootloader()
			calls := bootloaderHTTP(t, repo, data, nil, false)
			b, e := FetchBootloaderBuild(context.Background(), choice)
			if e != nil {
				t.Fatal(e)
			}
			dir := t.TempDir()
			got, e := DownloadBootloader(context.Background(), b, dir)
			if e != nil || !bytes.Equal(got, data) {
				t.Fatal(e)
			}
			n := *calls
			hotfixHTTP = &http.Client{Transport: loaderTransport(func(*http.Request) (*http.Response, error) { return nil, errors.New("offline") })}
			if _, e = DownloadBootloader(context.Background(), b, dir); e != nil || *calls != n {
				t.Fatal("verified cache not reusable", e)
			}
			p := filepath.Join(dir, b.SHA256+"-uboot.img")
			if e = os.WriteFile(p, make([]byte, 4), 0600); e != nil {
				t.Fatal(e)
			}
			if _, e = DownloadBootloader(context.Background(), b, dir); e == nil {
				t.Fatal("corrupt cache accepted offline")
			}
		})
	}
}
func TestBootloaderReleaseRefusals(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(map[string]any)
	}{{"tag", func(v map[string]any) { v["tag_name"] = "../other" }}, {"draft", func(v map[string]any) { v["draft"] = true }}, {"wrong-size", func(v map[string]any) { v["assets"].([]map[string]any)[0]["size"] = 123 }}, {"wrong-host", func(v map[string]any) {
		v["assets"].([]map[string]any)[0]["browser_download_url"] = "https://example.com/uboot.img"
	}}, {"trust-file", func(v map[string]any) {
		v["assets"].([]map[string]any)[0]["browser_download_url"] = "https://github.com/andr36oid/u-boot/releases/download/20261113-0524/trust.img"
	}}} {
		t.Run(tc.name, func(t *testing.T) {
			bootloaderHTTP(t, StandardBootloaderRepo, testBootloader(), tc.edit, false)
			if _, e := FetchBootloaderBuild(context.Background(), "standard"); e == nil {
				t.Fatal("accepted bad release")
			}
		})
	}
	t.Run("sha", func(t *testing.T) {
		bootloaderHTTP(t, StandardBootloaderRepo, testBootloader(), nil, true)
		b, e := FetchBootloaderBuild(context.Background(), "standard")
		if e != nil {
			t.Fatal(e)
		}
		if _, e = DownloadBootloader(context.Background(), b, t.TempDir()); e == nil {
			t.Fatal("accepted wrong SHA")
		}
	})
	t.Run("offline", func(t *testing.T) {
		old := hotfixHTTP
		hotfixHTTP = &http.Client{Transport: loaderTransport(func(*http.Request) (*http.Response, error) { return nil, errors.New("offline") })}
		defer func() { hotfixHTTP = old }()
		if _, e := FetchBootloaderBuild(context.Background(), "new"); e == nil {
			t.Fatal("offline accepted")
		}
	})
	for _, text := range []string{"", strings.Repeat("0", 64) + " uboot.img\n" + strings.Repeat("0", 64) + " uboot.img\n", "bad uboot.img\n"} {
		if _, e := bootloaderSum([]byte(text)); e == nil {
			t.Fatal("invalid sums accepted")
		}
	}
}
