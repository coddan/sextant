package config

import (
	"testing"
	"time"

	"github.com/coddan/sextant/internal/model"
)

func TestNotificationsTimeoutFor(t *testing.T) {
	n := Notifications{TimeoutSeconds: 5, ErrorTimeoutSeconds: 12}
	if got := n.TimeoutFor(model.NotifyInfo); got != 5*time.Second {
		t.Fatalf("info timeout = %v", got)
	}
	if got := n.TimeoutFor(model.NotifySuccess); got != 5*time.Second {
		t.Fatalf("success timeout = %v", got)
	}
	if got := n.TimeoutFor(model.NotifyError); got != 12*time.Second {
		t.Fatalf("error timeout = %v", got)
	}

	n.StickyErrors = true
	if got := n.TimeoutFor(model.NotifyError); got != 0 {
		t.Fatalf("sticky error timeout = %v, want 0", got)
	}
}

func TestNotificationsApplyTimeout(t *testing.T) {
	n := DefaultSettings().Notifications
	note := model.NewNotification(model.NotifyError, "boom")
	note = n.ApplyTimeout(note)
	if note.Timeout != time.Duration(n.ErrorTimeoutSeconds)*time.Second {
		t.Fatalf("timeout = %v", note.Timeout)
	}
}
