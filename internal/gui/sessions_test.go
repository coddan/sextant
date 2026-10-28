package gui

import (
	"testing"

	"github.com/coddan/sextant/internal/model"
)

func TestSessionStopWriterIdempotent(t *testing.T) {
	var empty terminalSession
	empty.stopWriter() // nil end must not panic

	s := &terminalSession{writeEnd: make(chan struct{})}
	s.stopWriter()
	s.stopWriter() // sync.Once must make the second close a no-op
	select {
	case <-s.writeEnd:
	default:
		t.Fatal("writeEnd was not closed")
	}
}

func TestUniqueSessionTitle(t *testing.T) {
	g := &App{sessions: []*terminalSession{
		{title: "web"},
		{title: "web (2)"},
	}}
	if got := g.uniqueSessionTitle(model.Host{Name: "web"}); got != "web (3)" {
		t.Fatalf("title = %q, want %q", got, "web (3)")
	}
	if got := g.uniqueSessionTitle(model.Host{Name: "db"}); got != "db" {
		t.Fatalf("title = %q, want %q", got, "db")
	}
	if got := g.uniqueSessionTitle(model.Host{Hostname: "10.0.0.5"}); got != "10.0.0.5" {
		t.Fatalf("fallback title = %q, want hostname", got)
	}
}
