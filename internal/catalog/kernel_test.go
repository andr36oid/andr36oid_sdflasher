package catalog

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/andr36oid/andr36oid-sdflasher/internal/firmware"
)

const kernelTestBuild = "v2026-10-22-release|eng.kenny.20261022.1200"
const kernelTestBase = "https://github.com/andr36oid/kernel-releases/releases/download/v1/"

func kernelTestRelease(version string) KernelRelease {
	return KernelRelease{Version: version, Title: "Btrfs support", Description: "Adds btrfs with the existing module ABI.", File: strings.TrimPrefix(version, "v") + ".zip", Size: 100, SHA256: strings.Repeat("a", 64), CompatibleBuilds: []string{kernelTestBuild}}
}
func kernelTestIndex(t *testing.T, releases ...KernelRelease) []byte {
	t.Helper()
	b, err := json.Marshal(KernelIndex{Format: 1, BaseURL: kernelTestBase, Kernels: releases})
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func TestParseKernelIndex(t *testing.T) {
	if index, err := ParseKernelIndex(kernelTestIndex(t, kernelTestRelease("4.4.194-btrfs.1"))); err != nil || len(index.Kernels) != 1 {
		t.Fatalf("index=%+v err=%v", index, err)
	}
	for _, raw := range []string{"", `{"format":2}`, `{"format":1,"base_url":"http://github.com/"}`, `{"format":1,"base_url":"https://example.com/releases/download/v1/"}`} {
		if _, err := ParseKernelIndex([]byte(raw)); err == nil {
			t.Fatalf("accepted %q", raw)
		}
	}
	for _, tc := range []struct {
		name   string
		change func(*KernelRelease)
	}{
		{"sha", func(k *KernelRelease) { k.SHA256 = strings.Repeat("x", 64) }},
		{"short sha", func(k *KernelRelease) { k.SHA256 = "aa" }},
		{"oversize", func(k *KernelRelease) { k.Size = firmware.KernelArchiveLimit + 1 }},
		{"empty size", func(k *KernelRelease) { k.Size = 0 }},
		{"bad version", func(k *KernelRelease) { k.Version = "latest" }},
		{"missing build", func(k *KernelRelease) { k.CompatibleBuilds = nil }},
		{"incomplete build", func(k *KernelRelease) { k.CompatibleBuilds = []string{"v1"} }},
		{"duplicate build", func(k *KernelRelease) { k.CompatibleBuilds = []string{kernelTestBuild, kernelTestBuild} }},
		{"missing title", func(k *KernelRelease) { k.Title = "" }},
		{"traversal", func(k *KernelRelease) { k.File = "../Image.zip" }},
		{"nested", func(k *KernelRelease) { k.File = "BOOT/kernel.zip" }},
		{"backslash", func(k *KernelRelease) { k.File = `..\kernel.zip` }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			k := kernelTestRelease("v1.0.0")
			tc.change(&k)
			if _, err := ParseKernelIndex(kernelTestIndex(t, k)); err == nil {
				t.Fatal("accepted invalid entry")
			}
		})
	}
	k := kernelTestRelease("v1.0.0")
	if _, err := ParseKernelIndex(kernelTestIndex(t, k, k)); err == nil {
		t.Fatal("accepted duplicate release")
	}
	for _, base := range []string{kernelTestBase + "../", kernelTestBase + "nested/", kernelTestBase + "?x", strings.Replace(kernelTestBase, "github.com", "github.com.evil", 1), strings.Replace(kernelTestBase, "/v1/", "/", 1)} {
		b, _ := json.Marshal(KernelIndex{Format: 1, BaseURL: base, Kernels: []KernelRelease{k}})
		if _, err := ParseKernelIndex(b); err == nil {
			t.Fatalf("accepted base %s", base)
		}
	}
}
func TestCheckKernelUpdate(t *testing.T) {
	older, newer, incompatible := kernelTestRelease("v1.9.0"), kernelTestRelease("v1.10.0"), kernelTestRelease("v9.0.0")
	incompatible.CompatibleBuilds = []string{"other|build"}
	client := appUpdateClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") == "" {
			t.Error("missing user agent")
		}
		w.Write(kernelTestIndex(t, newer, incompatible, older))
	}))
	for _, tc := range []struct{ name, build, version, want string }{
		{"newest numeric", kernelTestBuild, "1.8.0", "v1.10.0"},
		{"unknown kernel", kernelTestBuild, "", "v1.10.0"},
		{"up to date", kernelTestBuild, "v1.10.0", ""},
		{"newer card", kernelTestBuild, "v2.0.0", ""},
		{"incompatible", "unknown|build", "", ""},
		{"missing build", "", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state := firmware.KernelIdentity{Build: tc.build, Version: tc.version}
			offer, err := checkKernelUpdate(context.Background(), client, "https://index.test", state)
			if err != nil {
				t.Fatal(err)
			}
			if tc.want == "" {
				if offer != nil {
					t.Fatalf("unexpected %+v", offer)
				}
				return
			}
			if offer == nil || offer.Version != tc.want || offer.Current != state || offer.URL != kernelTestBase+newer.File {
				t.Fatalf("offer %+v", offer)
			}
		})
	}
	if _, err := checkKernelUpdate(context.Background(), client, "https://index.test", firmware.KernelIdentity{Build: kernelTestBuild, Version: "invalid"}); err == nil {
		t.Fatal("accepted unreliable current version")
	}
}
func TestFetchKernelIndexBoundsAndFailures(t *testing.T) {
	for _, tc := range []struct {
		body   string
		status int
	}{
		{strings.Repeat(" ", (2<<20)+1), 200}, {`{`, 200}, {`{"format":2}`, 200}, {"offline", 503},
	} {
		client := appUpdateClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(tc.status); io.WriteString(w, tc.body) }))
		if _, err := fetchKernelIndex(context.Background(), client, "https://index.test"); err == nil {
			t.Fatal("accepted invalid response")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := fetchKernelIndex(ctx, http.DefaultClient, KernelIndexURL); err == nil {
		t.Fatal("ignored cancellation")
	}
}
func TestDownloadKernelVerifiedAndRestricted(t *testing.T) {
	const data = "verified kernel ZIP bytes"
	old := http.DefaultTransport
	http.DefaultTransport = appUpdateTransport{handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, data) })}
	t.Cleanup(func() { http.DefaultTransport = old })
	k := kernelTestRelease("v1.0.0")
	k.Size = int64(len(data))
	sum := sha256.Sum256([]byte(data))
	k.SHA256 = hex.EncodeToString(sum[:])
	offer := &KernelOffer{KernelRelease: k, URL: kernelTestBase + k.File, Current: firmware.KernelIdentity{Build: kernelTestBuild}}
	dir := t.TempDir()
	path, err := DownloadKernel(context.Background(), offer, dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path); string(got) != data {
		t.Fatal("download changed")
	}
	os.Remove(path)
	for _, change := range []func(*KernelOffer){
		func(o *KernelOffer) { o.SHA256 = strings.Repeat("0", 64) },
		func(o *KernelOffer) { o.Size-- }, func(o *KernelOffer) { o.Size++ },
		func(o *KernelOffer) { o.URL = "https://example.com/" + o.File },
		func(o *KernelOffer) { o.URL = kernelTestBase + o.File + "?x" },
		func(o *KernelOffer) { o.URL = strings.Replace(o.URL, "/kernel-releases/", "/other/", 1) },
		func(o *KernelOffer) { o.Current.Build = "other|build" },
	} {
		bad := *offer
		change(&bad)
		if _, err := DownloadKernel(context.Background(), &bad, dir, nil); err == nil {
			t.Fatal("accepted invalid download")
		}
		entries, _ := os.ReadDir(dir)
		if len(entries) != 0 {
			t.Fatal("failed download left a file")
		}
	}
}
