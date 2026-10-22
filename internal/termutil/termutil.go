// Package termutil holds small helpers shared by the terminal frontends.
package termutil

import (
	"strings"
	"time"
)

// SudoPrompt reports whether a chunk of terminal output ends with sudo asking
// for a password. Only the tail is inspected, so an old prompt in the
// scrollback cannot trigger a password send later.
//
// The match is deliberately narrow: sudo prefixes its prompt with "[sudo]".
// Matching a bare "Password:" would also fire on login and database prompts,
// where sending the SSH password would be wrong. The bracketed marker is not
// translated, so this also works on non-English systems.
func SudoPrompt(data []byte) bool {
	if len(data) > 512 {
		data = data[len(data)-512:]
	}
	lower := strings.ToLower(string(data))
	for _, line := range strings.Split(lower, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[sudo]") {
			return true
		}
	}
	return false
}

// Throttle limits repeated actions to one per interval.
type Throttle struct {
	every time.Duration
	last  time.Time
}

// NewThrottle returns a throttle that allows one action per interval.
func NewThrottle(every time.Duration) *Throttle {
	return &Throttle{every: every}
}

// Allow reports whether the action may run now and records the time.
func (t *Throttle) Allow() bool {
	now := time.Now()
	if !t.last.IsZero() && now.Sub(t.last) < t.every {
		return false
	}
	t.last = now
	return true
}

// Reset forgets the last action.
func (t *Throttle) Reset() { t.last = time.Time{} }
