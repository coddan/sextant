package termutil

import (
	"testing"
	"time"
)

func TestSudoPrompt(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want bool
	}{
		{"plain prompt", "[sudo] password for alice: ", true},
		{"mid stream", "some output\r\n[sudo] password for alice: ", true},
		{"localized prefix", "[sudo] Passwort für alice: ", true},
		{"ssh login", "alice@example.com's password: ", false},
		{"database", "Password for user postgres: ", false},
		{"sudo error", "sudo: a password is required", false},
		{"empty", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SudoPrompt([]byte(tt.in)); got != tt.want {
				t.Fatalf("SudoPrompt(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestSudoPromptIgnoresOldOutput(t *testing.T) {
	old := make([]byte, 2048)
	for i := range old {
		old[i] = 'x'
	}
	in := string(old) + "\n[sudo] password for alice: "
	// The prompt is inside the tail window, so it is still detected.
	if !SudoPrompt([]byte(in)) {
		t.Fatal("prompt in the tail window should be detected")
	}
	in = "[sudo] password for alice: " + string(old)
	if SudoPrompt([]byte(in)) {
		t.Fatal("prompt far outside the tail window must be ignored")
	}
}

func TestThrottle(t *testing.T) {
	th := NewThrottle(20 * time.Millisecond)
	if !th.Allow() {
		t.Fatal("first call must be allowed")
	}
	if th.Allow() {
		t.Fatal("second immediate call must be blocked")
	}
	time.Sleep(25 * time.Millisecond)
	if !th.Allow() {
		t.Fatal("call after the interval must be allowed")
	}
	th.Reset()
	if !th.Allow() {
		t.Fatal("call after Reset must be allowed")
	}
}

func TestPasteInput(t *testing.T) {
	tests := []struct {
		name      string
		in        string
		bracketed bool
		want      string
	}{
		{"plain CRLF", "a\r\nb", false, "a\rb"},
		{"plain LF", "one\ntwo", false, "one\rtwo"},
		{"plain trailing newline", "ls\n", false, "ls\r"},
		{"bracketed CRLF", "a\r\nb", true, "\x1b[200~a\nb\x1b[201~"},
		{"bracketed LF kept", "one\ntwo", true, "\x1b[200~one\ntwo\x1b[201~"},
		{"bracketed lone CR", "a\rb", true, "\x1b[200~a\nb\x1b[201~"},
		{"empty", "", false, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := string(PasteInput(tc.in, tc.bracketed)); got != tc.want {
				t.Fatalf("PasteInput(%q, %v) = %q, want %q", tc.in, tc.bracketed, got, tc.want)
			}
		})
	}
}
