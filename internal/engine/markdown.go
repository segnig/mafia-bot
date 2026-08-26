package engine

import "strings"

// EscapeMD escapes the characters Telegram's legacy Markdown parser treats as
// entity delimiters.
//
// Telegram rejects an entire message with a 400 error when it cannot parse the
// entities, so any value that did not come from us — usernames (which commonly
// contain underscores), defense statements, whispers — must pass through this
// before being interpolated into a formatted message.
func EscapeMD(s string) string {
	// The backslash has to be escaped too, otherwise a name like `a\_b`
	// becomes `a\\_b`, whose second backslash is read as literal and leaves
	// the underscore opening an entity that never closes.
	if !strings.ContainsAny(s, "_*`[\\") {
		return s
	}
	var b strings.Builder
	b.Grow(len(s) + 8)
	for _, r := range s {
		switch r {
		case '_', '*', '`', '[', '\\':
			b.WriteRune('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// TruncateRunes shortens s to at most n runes, appending an ellipsis when it
// had to cut. Slicing by byte would split multi-byte characters and produce
// invalid UTF-8 that Telegram rejects.
func TruncateRunes(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + "..."
}
