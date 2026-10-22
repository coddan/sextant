package sshclient

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/coddan/sextant/internal/model"
)

// testDialer starts an in-process SSH server and returns a dialer that trusts
// it, plus a matching host. Used by the snippet runner tests.
func testDialer(t *testing.T, name string) (*Dialer, model.Host) {
	t.Helper()
	addr, hostKey := startTestServer(t, "pw")
	host, portStr, _ := net.SplitHostPort(addr)
	port := 0
	fmt.Sscanf(portStr, "%d", &port)
	dir := t.TempDir()
	khPath := filepath.Join(dir, "known_hosts")
	if err := os.WriteFile(khPath, []byte(lineFor(host, hostKey)), 0o600); err != nil {
		t.Fatal(err)
	}
	d := &Dialer{
		Prompts:        Prompts{Password: func(string, int) (string, error) { return "pw", nil }},
		KnownHostsPath: khPath,
		Timeout:        5 * time.Second,
		Cache:          NewAuthCache(),
	}
	h := model.NewHost(name, host)
	h.Port = port
	h.User = "tester"
	return d, h
}

func TestSelectHosts(t *testing.T) {
	hosts := []model.Host{
		{ID: "1", Name: "a", Group: "prod"},
		{ID: "2", Name: "b", Group: "dev"},
		{ID: "3", Name: "c", Group: "prod"},
	}
	if got := SelectHosts(hosts, SnippetTarget{All: true}); len(got) != 3 {
		t.Fatalf("all = %d", len(got))
	}
	if got := SelectHosts(hosts, SnippetTarget{Group: "prod"}); len(got) != 2 || got[0].ID != "1" || got[1].ID != "3" {
		t.Fatalf("group = %+v", got)
	}
	if got := SelectHosts(hosts, SnippetTarget{HostIDs: []string{"2"}}); len(got) != 1 || got[0].ID != "2" {
		t.Fatalf("ids = %+v", got)
	}
	if got := SelectHosts(hosts, SnippetTarget{}); len(got) != 0 {
		t.Fatalf("empty target = %+v", got)
	}
}

func TestRunSnippetExec(t *testing.T) {
	d, h := testDialer(t, "one")

	results := RunSnippet(context.Background(), d, []model.Host{h}, []model.Host{h}, nil, "echo hi", SnippetRunOptions{})
	if len(results) != 1 {
		t.Fatalf("results = %+v", results)
	}
	if results[0].Err != "" {
		t.Fatalf("err = %q", results[0].Err)
	}
	if results[0].Output != "ran:echo hi\n" {
		t.Fatalf("output = %q", results[0].Output)
	}
}

func TestRunSnippetStream(t *testing.T) {
	d, h := testDialer(t, "stream")
	var lines []string
	var finished bool
	results := RunSnippet(context.Background(), d, []model.Host{h}, []model.Host{h}, nil, "uptime", SnippetRunOptions{
		Stream:        true,
		MaxConcurrent: 2,
		OnEvent: func(e SnippetEvent) {
			if e.Line != "" {
				lines = append(lines, e.Line)
			}
			if e.Result != nil {
				finished = true
			}
		},
	})
	if !finished {
		t.Fatal("expected a completion event")
	}
	if len(results) != 1 || results[0].Err != "" {
		t.Fatalf("results = %+v", results)
	}
	if len(lines) == 0 || lines[0] != "ran:uptime" {
		t.Fatalf("streamed lines = %v", lines)
	}
}

func TestRunSnippetRecordsDialFailure(t *testing.T) {
	d, h := testDialer(t, "noaccess")
	// Point at a dead port to force a dial error.
	h.Port = freePort(t)
	results := RunSnippet(context.Background(), d, []model.Host{h}, []model.Host{h}, nil, "echo hi", SnippetRunOptions{})
	if len(results) != 1 || results[0].Err == "" {
		t.Fatalf("expected a dial error, got %+v", results)
	}
}
