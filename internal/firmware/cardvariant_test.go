package firmware

import (
	"bytes"
	"compress/gzip"
	"strings"
	"testing"
)

// fakeKernel is an uncompressed kernel with its banner some way in, past the first
// read of KernelLine, so that a banner across two reads is found too.
func fakeKernel(banner string, at int) []byte {
	b := bytes.Repeat([]byte{0x5a}, at)
	b = append(b, banner...)
	return append(b, bytes.Repeat([]byte{0}, 4096)...)
}

func gz(t *testing.T, b []byte) string {
	t.Helper()
	var out bytes.Buffer
	w := gzip.NewWriter(&out)
	w.Write(b)
	w.Close()
	return out.String()
}

func cardVariant(t *testing.T, files map[string]string) CardVariant {
	t.Helper()
	f, tab := bootOnlyImage(t, files)
	boot, e := OpenBoot(f, tab, true)
	if e != nil {
		t.Fatal(e)
	}
	return ReadCardVariant(boot)
}

func TestCardVariantFromBuildFile(t *testing.T) {
	// a kernel that says otherwise: the build file wins
	other := gz(t, fakeKernel("Linux version 4.19.232-rk3326-itskenny0 (kenny@build)", 3000))
	for _, kind := range [][2]string{{"4.19", "gles"}, {"4.19", "vulkan"}, {"5.10", "gles"}, {"5.10", "vulkan"}} {
		info := "format=2\nbootloader_chain=classic\nkernel_line=" + kind[0] + "\ngpu_variant=" + kind[1] + "\nsystem_fs=erofs\n"
		v := cardVariant(t, map[string]string{BuildInfoV2File: info, KernelGzipFile: other})
		if v.Kernel != kind[0] || v.Graphics != kind[1] || v.From != VariantFromInfo {
			t.Fatal(kind, v)
		}
	}
}

func TestCardVariantFromKernelVersion(t *testing.T) {
	// format 1 (2026-10-18 to 2026-11-12): no kernel_line, no gpu_variant
	for _, c := range []struct {
		files map[string]string
		want  string
	}{
		{map[string]string{BuildInfoFile: testBuildInfo, KernelGzipFile: gz(t, fakeKernel("Linux version 5.10.198-rk3326-itskenny0 (kenny@build) #42 SMP", 3<<20))}, "5.10"},
		{map[string]string{BuildInfoFile: testBuildInfo, KernelGzipFile: gz(t, fakeKernel("Linux version 4.19.172 (x@y) #1", 100))}, "4.19"},
		// before 2026-10-18: no build file, the kernel as Image; the banner across a read boundary
		{map[string]string{KernelImageFile: string(fakeKernel("Linux version 4.19.172 (x@y) #1", 1<<20-20))}, "4.19"},
		// a v2 file without the keys falls back too
		{map[string]string{BuildInfoV2File: "format=2\n", KernelImageFile: string(fakeKernel("Linux version 5.10.1 (x@y)", 10))}, "5.10"},
	} {
		v := cardVariant(t, c.files)
		if v.Kernel != c.want || v.Graphics != "" || v.From != VariantFromKernel {
			t.Fatal(c.want, v)
		}
	}
}

func TestCardVariantUnknown(t *testing.T) {
	for _, files := range []map[string]string{
		{},
		{BuildInfoFile: testBuildInfo},
		{KernelImageFile: string(fakeKernel("Linux version 6.1.0 (x@y)", 10))},
		{KernelImageFile: string(fakeKernel("no banner at all", 10))},
		{KernelGzipFile: "not gzip at all"},
		{BuildInfoV2File: "format=2\nkernel_line=6.1\ngpu_variant=vulkan\n"},
	} {
		if v := cardVariant(t, files); v != (CardVariant{}) {
			t.Fatal(files, v)
		}
	}
	// a known kernel line with a graphics driver this flasher does not know
	v := cardVariant(t, map[string]string{BuildInfoV2File: "format=2\nkernel_line=5.10\ngpu_variant=opencl\n"})
	if v.Kernel != "5.10" || v.Graphics != "" {
		t.Fatal(v)
	}
}

func TestKernelLineStopsAtTheBanner(t *testing.T) {
	if k := KernelLine(strings.NewReader("Linux version 5.10.")); k != "5.10" {
		t.Fatal(k)
	}
	if k := KernelLine(strings.NewReader("Linux version %s\nLinux version 4.19.1 (x)")); k != "4.19" {
		t.Fatal(k)
	}
}
