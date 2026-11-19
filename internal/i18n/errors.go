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

// Is: an error of the format, whatever its arguments: one of its messages is, or
// its text has the message (an error that crossed as text).
func (f *Format) Is(e error) bool {
	if e == nil {
		return false
	}
	for _, m := range messages(e) {
		if m.Format == f {
			return true
		}
	}
	return f.re.MatchString(e.Error())
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
// arguments. A message about the card's state (NewState) only counts when there is
// nothing else, and a cause (NewCause) only when the error has no message at all.
// ok is false when it has none.
func Find(e error) (key string, args []any, ok bool) {
	if e == nil {
		return "", nil, false
	}
	var state *Error
	for _, m := range messages(e) {
		if isState(m.Format) {
			if state == nil {
				state = m
			}
			continue
		}
		return m.Format.key, m.args(), true
	}
	if key, args, ok = findText(e.Error(), false); ok {
		return key, args, true
	}
	if f, ok := findCause(e.Error()); ok {
		return f.key, nil, true
	}
	if state != nil {
		return state.Format.key, state.args(), true
	}
	return findText(e.Error(), true)
}

// messages are the *Error in e's tree, in the order errors.As would find them.
func messages(e error) []*Error {
	var out []*Error
	var walk func(error)
	walk = func(e error) {
		for e != nil {
			if m, ok := e.(*Error); ok {
				out = append(out, m)
				return
			}
			if multi, ok := e.(interface{ Unwrap() []error }); ok {
				for _, inner := range multi.Unwrap() {
					walk(inner)
				}
				return
			}
			e = errors.Unwrap(e)
		}
	}
	walk(e)
	return out
}

func (m *Error) args() []any {
	var out []any
	for _, a := range m.Args {
		out = append(out, a)
	}
	return out
}

// FindText is Find for an error's text.
func FindText(raw string) (key string, args []any, ok bool) {
	if key, args, ok = findText(raw, false); ok {
		return key, args, true
	}
	if f, ok := findCause(raw); ok {
		return f.key, nil, true
	}
	return findText(raw, true)
}

// findText is the registered message that comes first in raw, of the state
// messages when states is true, of the others when not.
func findText(raw string, states bool) (key string, args []any, ok bool) {
	formatsMu.Lock()
	list := append([]*Format(nil), formats...)
	formatsMu.Unlock()
	best := -1
	for _, f := range list {
		if isState(f) != states {
			continue
		}
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

// A state message says what an error left the card like ("the card is incomplete,
// write it again"). It is added after an error's own message, or its cause.
var (
	statesMu sync.Mutex
	states   = map[*Format]bool{}
)

// NewState registers key as a state message (NewFormat).
func NewState(key string) *Format {
	f := NewFormat(key)
	statesMu.Lock()
	states[f] = true
	statesMu.Unlock()
	return f
}

func isState(f *Format) bool {
	statesMu.Lock()
	defer statesMu.Unlock()
	return states[f]
}

// Explain is the registered message in raw, translated, or "" when it has none.
// When raw also says what the error left the card like (NewState), that follows on
// its own paragraph.
func Explain(locale, raw string) string {
	key, args, ok := FindText(raw)
	if !ok {
		return ""
	}
	out := Text(locale, key, args...)
	if skey, sargs, ok := findText(raw, true); ok && skey != key {
		out += "\n\n" + Text(locale, skey, sargs...)
	}
	return out
}

// ExplainError is Explain for an error, which may carry its messages as values.
func ExplainError(locale string, e error) string {
	key, args, ok := Find(e)
	if !ok {
		return ""
	}
	out := Text(locale, key, args...)
	if skey, sargs, ok := FindState(e); ok && skey != key {
		out += "\n\n" + Text(locale, skey, sargs...)
	}
	return out
}

// FindState is the state message in e (NewState), ok false for none.
func FindState(e error) (string, []any, bool) {
	for _, m := range messages(e) {
		if isState(m.Format) {
			return m.Format.key, m.args(), true
		}
	}
	return findText(e.Error(), true)
}

// Has: key is in en.json.
func Has(key string) bool { _, ok := catalogs["en"][key]; return ok }
