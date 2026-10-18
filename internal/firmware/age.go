package firmware

import (
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"time"
)

// OldestBuild: builds from before this date don't work with this flasher: they have no
// andr36oid-build.txt on BOOT. Release lists leave them out, and a chosen file whose name
// carries an older date is refused before it is opened.
var OldestBuild = time.Date(2026, time.October, 18, 0, 0, 0, 0, time.UTC)

// the build date in a file name: 20261016 in andr36oid-20261016-2212-rk3326.img.xz (and in
// the older lineage-18.1-20261011-1531-r36s-android.img.zip), also with the time run on
// (202507181828)
var buildDate = regexp.MustCompile(`(?:^|[^0-9])(20[0-9]{2})(0[1-9]|1[0-2])(0[1-9]|[12][0-9]|3[01])(?:[0-9]{4})?(?:[^0-9]|$)`)

// BuildDate is the build date in an image's file name, if it has one.
func BuildDate(name string) (time.Time, bool) {
	m := buildDate.FindStringSubmatch(path.Base(filepath.ToSlash(name)))
	if m == nil {
		return time.Time{}, false
	}
	y, _ := strconv.Atoi(m[1])
	mo, _ := strconv.Atoi(m[2])
	d, _ := strconv.Atoi(m[3])
	t := time.Date(y, time.Month(mo), d, 0, 0, 0, 0, time.UTC)
	// 20250231 isn't a date
	if t.Day() != d {
		return time.Time{}, false
	}
	return t, true
}

// TooOld reports a file name dated before OldestBuild, with that date as 2006-01-02.
// Names without a date pass.
func TooOld(name string) (string, bool) {
	t, ok := BuildDate(name)
	if !ok || !t.Before(OldestBuild) {
		return "", false
	}
	return t.Format("2006-01-02"), true
}
