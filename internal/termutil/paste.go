package termutil

import "strings"

// PasteInput converts clipboard text into the bytes to send to the remote
// program. Line endings are normalised first: CRLF becomes LF, so a Windows
// clipboard cannot turn one line break into two.
//
// Terminals send CR for Enter, so a plain paste uses CR for every line break.
// A bracketed paste is inserted literally by the program, so it keeps LF and is
// wrapped in the bracketed-paste markers; without those markers editors treat
// each newline as typed input and apply auto-indent to every pasted line.
func PasteInput(text string, bracketed bool) []byte {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	if bracketed {
		text = strings.ReplaceAll(text, "\r", "\n")
	} else {
		text = strings.ReplaceAll(text, "\n", "\r")
	}
	text = sanitizePaste(text)
	if bracketed {
		return []byte("\x1b[200~" + text + "\x1b[201~")
	}
	return []byte(text)
}

// sanitizePaste drops control characters that a remote program could interpret
// as input. ESC is the important one: pasted text could otherwise inject key
// sequences or close a bracketed paste early. Tab and line breaks survive.
func sanitizePaste(text string) string {
	var b strings.Builder
	b.Grow(len(text))
	for _, r := range text {
		switch {
		case r == '\t' || r == '\n' || r == '\r':
			b.WriteRune(r)
		case r < 0x20 || r == 0x7f:
			// Other C0 controls and DEL are not text.
		case r >= 0x80 && r <= 0x9f:
			// C1 controls, including the single-byte CSI, are not text.
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// PasteLines counts the lines in clipboard text, treating CRLF and CR as line
// breaks. It is used to decide whether a paste needs confirmation.
func PasteLines(text string) int {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	// A trailing line break ends the last line, it does not start a new one.
	text = strings.TrimRight(text, "\n")
	if text == "" {
		return 0
	}
	return strings.Count(text, "\n") + 1
}
