package firmware

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func write(t *testing.T, path string) {
	t.Helper()
	if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(path, []byte("x"), 0600); e != nil {
		t.Fatal(e)
	}
}

func backdate(t *testing.T, root string) {
	t.Helper()
	old := time.Now().Add(-time.Hour)
	filepath.Walk(root, func(p string, _ os.FileInfo, _ error) error {
		os.Chtimes(p, old, old)
		return nil
	})
}

func gone(t *testing.T, path string) {
	t.Helper()
	if _, e := os.Stat(path); !os.IsNotExist(e) {
		t.Errorf("expected removed: %s", path)
	}
}

func present(t *testing.T, path string) {
	t.Helper()
	if _, e := os.Stat(path); e != nil {
		t.Errorf("expected kept: %s (%v)", path, e)
	}
}

func TestCleanWorkDir(t *testing.T) {
	dir := t.TempDir()
	staged := filepath.Join(dir, "image-123.img")
	download := filepath.Join(dir, "download-abc")
	cached := filepath.Join(dir, "deadbeef-andr36oid.img.xz") // content-addressed cache: keep
	spentReq := filepath.Join(dir, "operation-1", "request.json")
	spentSys := filepath.Join(dir, "operation-1", "system-9.img")
	keptReq := filepath.Join(dir, "operation-2", "request.json")
	keptScratch := filepath.Join(dir, "operation-2", "image-7.img")
	keptBackup := filepath.Join(dir, "operation-2", "recovery", "backup.bin")
	for _, p := range []string{staged, download, cached, spentReq, spentSys, keptReq, keptScratch, keptBackup} {
		write(t, p)
	}
	backdate(t, dir)

	CleanWorkDir(dir)

	gone(t, staged)
	gone(t, download)
	present(t, cached)
	gone(t, filepath.Join(dir, "operation-1")) // spent, no backup: whole dir removed
	present(t, keptReq)                        // kept because it holds a recovery backup
	present(t, keptBackup)
	gone(t, keptScratch) // but its large scratch image is stripped
}

func TestCleanWorkDirSkipsRecent(t *testing.T) {
	dir := t.TempDir()
	fresh := filepath.Join(dir, "image-now.img")
	write(t, fresh) // fresh mtime: inside recentWindow
	CleanWorkDir(dir)
	present(t, fresh) // left alone so a concurrently-running instance isn't disturbed
}

func TestCleanStagedImage(t *testing.T) {
	dir := t.TempDir()
	staged := filepath.Join(dir, "image-5.img")
	user := filepath.Join(dir, "myrom.img")
	write(t, staged)
	write(t, user)

	CleanStagedImage(dir, staged)                                     // our scratch: removed
	CleanStagedImage(dir, user)                                       // user's own .img: kept
	CleanStagedImage(dir, filepath.Join("/elsewhere", "image-5.img")) // outside dir: ignored
	CleanStagedImage(dir, "")

	gone(t, staged)
	present(t, user)
}
