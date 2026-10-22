package model

import (
	"testing"
	"time"
)

func TestNotificationExpiry(t *testing.T) {
	now := time.Now()
	n := NewNotification(NotifyInfo, "hello")
	if n.ID == "" {
		t.Fatal("missing id")
	}
	if n.Expired(now) {
		t.Fatal("a sticky notification (timeout 0) should never expire")
	}
	if n.Remaining(now) != 0 {
		t.Fatal("sticky notification should have no remaining time")
	}

	n.Timeout = 5 * time.Second
	now = n.Created
	if n.Expired(now) {
		t.Fatal("should not expire immediately")
	}
	if got := n.Remaining(now); got <= 0 || got > 5*time.Second {
		t.Fatalf("remaining = %v", got)
	}
	if !n.Expired(n.Created.Add(5 * time.Second)) {
		t.Fatal("should expire exactly at the deadline")
	}
	if !n.Expired(n.Created.Add(time.Hour)) {
		t.Fatal("should expire well after the deadline")
	}
	if n.Remaining(n.Created.Add(time.Hour)) != 0 {
		t.Fatal("expired notification should have no remaining time")
	}
}
