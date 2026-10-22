package sshclient

import (
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/coddan/sextant/internal/model"
)

// MaxJumpHops bounds a ProxyJump chain, as ssh does.
const MaxJumpHops = 10

// ResolveChain flattens a ProxyJump value into the hosts to connect through,
// nearest first. An empty result means a direct connection.
//
// Each hop is looked up by alias in known (the app's host list, including
// hosts imported from ~/.ssh/config), so it inherits HostName, User, Port,
// IdentityFile and certificate settings. Unknown aliases are used literally.
func ResolveChain(target model.Host, known []model.Host) ([]model.Host, error) {
	seen := map[string]bool{}
	return resolveChain(target, known, seen, 0)
}

func resolveChain(target model.Host, known []model.Host, seen map[string]bool, depth int) ([]model.Host, error) {
	value := strings.TrimSpace(target.ProxyJump)
	if value == "" || strings.EqualFold(value, "none") {
		return nil, nil
	}
	if depth > MaxJumpHops {
		return nil, fmt.Errorf("ssh: ProxyJump chain longer than %d hops", MaxJumpHops)
	}
	key := jumpKey(target)
	if seen[key] {
		return nil, fmt.Errorf("ssh: ProxyJump cycle at %s", target.Name)
	}
	seen[key] = true
	defer delete(seen, key)

	var out []model.Host
	for _, spec := range splitJumpSpecs(value) {
		hop, err := resolveHop(spec, known)
		if err != nil {
			return nil, err
		}
		if seen[jumpKey(hop)] {
			return nil, fmt.Errorf("ssh: ProxyJump cycle at %s", hop.Name)
		}
		prefix, err := resolveChain(hop, known, seen, depth+1)
		if err != nil {
			return nil, err
		}
		out = append(out, prefix...)
		// The chain is flattened: a caller connects hop by hop.
		hop.ProxyJump = ""
		out = append(out, hop)
	}
	if len(out) > MaxJumpHops {
		return nil, fmt.Errorf("ssh: ProxyJump chain has %d hops, maximum is %d", len(out), MaxJumpHops)
	}
	return out, nil
}

func jumpKey(h model.Host) string {
	if h.ID != "" {
		return "id:" + h.ID
	}
	return "name:" + strings.ToLower(h.Name)
}

func splitJumpSpecs(value string) []string {
	fields := strings.FieldsFunc(value, func(r rune) bool { return r == ',' })
	var out []string
	for _, f := range fields {
		if f = strings.TrimSpace(f); f != "" {
			out = append(out, f)
		}
	}
	return out
}

// resolveHop parses [user@]host[:port] and applies a known host's settings.
func resolveHop(spec string, known []model.Host) (model.Host, error) {
	user := ""
	rest := spec
	if at := strings.LastIndex(rest, "@"); at >= 0 {
		user = rest[:at]
		rest = rest[at+1:]
	}
	host, port, err := splitHostPort(rest)
	if err != nil {
		return model.Host{}, fmt.Errorf("ssh: invalid ProxyJump hop %q: %w", spec, err)
	}
	if host == "" {
		return model.Host{}, fmt.Errorf("ssh: invalid ProxyJump hop %q", spec)
	}
	for _, h := range known {
		if strings.EqualFold(h.Name, host) {
			hop := h.Clone()
			if user != "" {
				hop.User = user
			}
			if port > 0 {
				hop.Port = port
			}
			if hop.Hostname == "" {
				hop.Hostname = host
			}
			return hop, nil
		}
	}
	hop := model.NewHost(host, host)
	hop.Source = model.SourceManual
	hop.User = user
	hop.Port = port
	if hop.Port == 0 {
		hop.Port = 22
	}
	return hop, nil
}

func splitHostPort(s string) (string, int, error) {
	if strings.HasPrefix(s, "[") {
		end := strings.Index(s, "]")
		if end < 0 {
			return "", 0, fmt.Errorf("unclosed '['")
		}
		host := s[1:end]
		if end+1 == len(s) {
			return host, 0, nil
		}
		if s[end+1] != ':' {
			return "", 0, fmt.Errorf("expected ':' after ']'")
		}
		port, err := parsePort(s[end+2:])
		return host, port, err
	}
	if strings.Count(s, ":") > 1 {
		// Bare IPv6 address without a port.
		if net.ParseIP(s) == nil {
			return "", 0, fmt.Errorf("invalid address")
		}
		return s, 0, nil
	}
	if idx := strings.LastIndex(s, ":"); idx >= 0 {
		port, err := parsePort(s[idx+1:])
		if err != nil {
			return "", 0, err
		}
		return s[:idx], port, nil
	}
	return s, 0, nil
}

func parsePort(s string) (int, error) {
	p, err := strconv.Atoi(s)
	if err != nil || p < 1 || p > 65535 {
		return 0, fmt.Errorf("invalid port %q", s)
	}
	return p, nil
}
