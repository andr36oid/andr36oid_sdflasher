package i18n

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
)

// A Format is a message for the user that comes as an error: its key is the English
// text with %s for each argument (an en.json key), and its Error() is that text
// filled in. The message often crosses a process (the desktop's disk helper) or a
// language (the Android app) as plain text, so Find reads a registered message back
// out of any error text, to show it translated.
type Format struct {
	key string
	re  *regexp.Regexp
}

var (
	formatsMu sync.Mutex
	formats   []*Format
)

// NewFormat registers key (only %s verbs) and returns it; call it from a package
// level var so that every binary knows all formats.
func NewFormat(key string) *Format {
	parts := strings.Split(key, "%s")
	pattern := regexp.QuoteMeta(parts[0])
	for _, part := range parts[1:] {
		// arguments are short values (sizes, names) on one line
		pattern += `([^\n]*?)` + regexp.QuoteMeta(part)
	}
	f := &Format{key: key, re: regexp.MustCompile(pattern)}
	formatsMu.Lock()
	formats = append(formats, f)
	formatsMu.Unlock()
	return f
}

// Key is the format's en.json key.
func (f *Format) Key() string { return f.key }

// Error is a message of the format with its arguments.
type Error struct {
	Format *Format
	Args   []string
}

func (e *Error) Error() string {
	args := make([]any, len(e.Args))
	for i, a := range e.Args {
		args[i] = a
	}
	if len(args) == 0 {
		return e.Format.key
	}
	return fmt.Sprintf(e.Format.key, args...)
}

// New is the message with args, one for each %s.
func (f *Format) New(args ...string) error { return &Error{Format: f, Args: args} }

// Is: an error of the format, whatever its arguments.
func (f *Format) Is(e error) bool {
	var m *Error
	for e != nil {
		if errors.As(e, &m) {
			if m.Format == f {
				return true
			}
			e = errors.Unwrap(m)
			continue
		}
		return f.re.MatchString(e.Error())
	}
	return false
}

// Formats are the registered keys, for the tests.
func Formats() []string {
	formatsMu.Lock()
	defer formatsMu.Unlock()
	var out []string
	for _, f := range formats {
		out = append(out, f.key)
	}
	return out
}

// Find is the first registered message in an error (or its text): its key and
// arguments. ok is false when it has none.
func Find(e error) (key string, args []any, ok bool) {
	if e == nil {
		return "", nil, false
	}
	var m *Error
	if errors.As(e, &m) {
		for _, a := range m.Args {
			args = append(args, a)
		}
		return m.Format.key, args, true
	}
	return FindText(e.Error())
}

// FindText is Find for an error's text.
func FindText(raw string) (key string, args []any, ok bool) {
	formatsMu.Lock()
	list := append([]*Format(nil), formats...)
	formatsMu.Unlock()
	best := -1
	for _, f := range list {
		loc := f.re.FindStringSubmatchIndex(raw)
		if loc == nil || (best >= 0 && loc[0] >= best) {
			continue
		}
		best = loc[0]
		key, args = f.key, nil
		for i := 2; i+1 < len(loc); i += 2 {
			args = append(args, raw[loc[i]:loc[i+1]])
		}
	}
	return key, args, best >= 0
}

// Explain is the registered message in raw, translated, or "" when it has none.
func Explain(locale, raw string) string {
	key, args, ok := FindText(raw)
	if !ok {
		return ""
	}
	return Text(locale, key, args...)
}

// Has: key is in en.json.
func Has(key string) bool { _, ok := catalogs["en"][key]; return ok }
