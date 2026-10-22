package sshclient

import (
	"context"
	"fmt"
	"net"
	"time"
)

// ProbeResult is the outcome of a reachability check.
type ProbeResult struct {
	Address string        `json:"address"`
	Online  bool          `json:"online"`
	Latency time.Duration `json:"latency"`
	Err     string        `json:"error,omitempty"`
}

// ProbeTCP checks whether addr accepts a TCP connection. It is the monitor for
// hosts with no POSIX shell, such as network appliances: it never logs in, so
// there are no credentials to store and nothing to time out.
func ProbeTCP(ctx context.Context, addr string, timeout time.Duration) ProbeResult {
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	start := time.Now()
	dialer := &net.Dialer{Timeout: timeout}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	latency := time.Since(start)
	if err != nil {
		return ProbeResult{Address: addr, Online: false, Latency: latency, Err: err.Error()}
	}
	_ = conn.Close()
	return ProbeResult{Address: addr, Online: true, Latency: latency}
}

// ProbeHost probes a host using its monitor address. It is a thin wrapper so
// callers do not repeat the address resolution.
func ProbeHost(ctx context.Context, addr string, timeout time.Duration) ProbeResult {
	return ProbeTCP(ctx, addr, timeout)
}

// ProbeError renders a probe failure for the dashboard.
func ProbeError(r ProbeResult) string {
	if r.Online {
		return ""
	}
	return fmt.Sprintf("tcp %s: %s", r.Address, r.Err)
}
