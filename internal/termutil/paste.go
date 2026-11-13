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
		return []byte("\x1b[200~" + text + "\x1b[201~")
	}
	text = strings.ReplaceAll(text, "\n", "\r")
	return []byte(text)
}

// PasteLines counts the lines in clipboard text, treating CRLF and CR as line
// breaks. It is used to decide whether a paste needs confirmation.
func PasteLines(text string) int {
	if text == "" {
		return 0
	}
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	return strings.Count(text, "\n") + 1
}
