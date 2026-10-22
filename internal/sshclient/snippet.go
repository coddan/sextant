package sshclient

import (
	"context"
	"sync"
	"time"

	"github.com/coddan/sextant/internal/model"
)

// SnippetTarget selects which hosts a snippet runs on.
type SnippetTarget struct {
	// All ignores the other fields and selects every host.
	All bool
	// HostIDs selects specific hosts by ID.
	HostIDs []string
	// Group selects every host in a group.
	Group string
}

// SelectHosts applies a target to the full host list, preserving input order.
func SelectHosts(hosts []model.Host, target SnippetTarget) []model.Host {
	if target.All && len(target.HostIDs) == 0 && target.Group == "" {
		return append([]model.Host(nil), hosts...)
	}
	ids := make(map[string]struct{}, len(target.HostIDs))
	for _, id := range target.HostIDs {
		ids[id] = struct{}{}
	}
	var out []model.Host
	for _, h := range hosts {
		switch {
		case len(ids) > 0:
			if _, ok := ids[h.ID]; ok {
				out = append(out, h)
			}
		case target.Group != "":
			if h.Group == target.Group {
				out = append(out, h)
			}
		case target.All:
			out = append(out, h)
		}
	}
	return out
}

// SnippetResult is one host's outcome.
type SnippetResult struct {
	HostID   string        `json:"host_id"`
	HostName string        `json:"host_name"`
	Output   string        `json:"output,omitempty"`
	Err      string        `json:"error,omitempty"`
	ExitCode int           `json:"exit_code"`
	Duration time.Duration `json:"duration"`
}

// SnippetEvent is emitted while a run is in progress. Line and Stream are set
// for streamed output; Result is set once a host finishes.
type SnippetEvent struct {
	HostID   string
	HostName string
	Line     string
	Stream   string
	Result   *SnippetResult
}

// SnippetRunOptions configures a run.
type SnippetRunOptions struct {
	// Stream keeps each process alive and streams output line by line, for
	// commands like tail -f or journalctl -f.
	Stream bool
	// MaxConcurrent caps simultaneous hosts. Zero means one at a time.
	MaxConcurrent int
	// OnEvent is called for streamed lines and host completion. It may be
	// called concurrently from several host goroutines.
	OnEvent func(SnippetEvent)
}

// RunSnippet runs command on each host, opening a separate connection per host.
// It returns when every host has finished or ctx is cancelled; cancelling ctx
// interrupts the remote processes.
func RunSnippet(ctx context.Context, d *Dialer, hosts []model.Host, known []model.Host, passwords PasswordResolver, command string, opts SnippetRunOptions) []SnippetResult {
	results := make([]SnippetResult, len(hosts))
	limit := opts.MaxConcurrent
	if limit < 1 {
		limit = 1
	}
	sem := make(chan struct{}, limit)
	var wg sync.WaitGroup

	for i, host := range hosts {
		wg.Add(1)
		go func(i int, host model.Host) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				results[i] = SnippetResult{HostID: host.ID, HostName: host.Name, Err: ctx.Err().Error()}
				if opts.OnEvent != nil {
					r := results[i]
					opts.OnEvent(SnippetEvent{HostID: host.ID, HostName: host.Name, Result: &r})
				}
				return
			}
			defer func() { <-sem }()

			results[i] = runSnippetHost(ctx, d, host, known, passwords, command, opts)
			if opts.OnEvent != nil {
				r := results[i]
				opts.OnEvent(SnippetEvent{HostID: host.ID, HostName: host.Name, Result: &r})
			}
		}(i, host)
	}
	wg.Wait()
	return results
}

func runSnippetHost(ctx context.Context, d *Dialer, host model.Host, known []model.Host, passwords PasswordResolver, command string, opts SnippetRunOptions) SnippetResult {
	start := time.Now()
	res := SnippetResult{HostID: host.ID, HostName: host.Name}

	client, err := d.Dial(ctx, host, known, passwords)
	if err != nil {
		res.Err = err.Error()
		res.Duration = time.Since(start)
		return res
	}
	defer client.Close()

	if opts.Stream {
		err = client.RunStreaming(ctx, command, func(stream, line string) {
			if opts.OnEvent != nil {
				opts.OnEvent(SnippetEvent{
					HostID:   host.ID,
					HostName: host.Name,
					Line:     line,
					Stream:   stream,
				})
			}
		})
		if err != nil && ctx.Err() == nil {
			res.Err = err.Error()
		}
	} else {
		out, execErr := client.Run(ctx, command)
		res.Output = string(out)
		if execErr != nil && ctx.Err() == nil {
			res.Err = execErr.Error()
		}
	}
	res.Duration = time.Since(start)
	return res
}
