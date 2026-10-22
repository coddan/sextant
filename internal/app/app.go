// Package app holds the pieces shared by the terminal and desktop frontends:
// the configuration store, the SSH dialer, the tunnel manager and the small
// amount of session bookkeeping both need. Keeping it here means a fix lands in
// both frontends at once.
package app

import (
	"context"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/coddan/sextant/internal/config"
	"github.com/coddan/sextant/internal/i18n"
	"github.com/coddan/sextant/internal/model"
	"github.com/coddan/sextant/internal/sshclient"
)

// Runtime is the shared application state.
type Runtime struct {
	Store    *config.Store
	Dialer   *sshclient.Dialer
	Forwards *sshclient.ForwardManager
	Tr       *i18n.Translator

	// HostKeyPrompt is asked before trusting an unknown host key. Changed keys
	// are always rejected; the engine never allows those through. Returning
	// false, or leaving the callback nil, accepts a new key on first use.
	HostKeyPrompt func(host, fingerprint, keyType string) (bool, error)
	// PasswordPrompt is asked when no password is stored. Returning an error
	// aborts the login.
	PasswordPrompt func(hostName string, attempt int) (string, error)
	// PassphrasePrompt is asked when a key passphrase is neither cached nor
	// stored.
	PassphrasePrompt func(keyPath string, attempt int) (string, error)
	// KeyboardInteractive handles 2FA and other challenge questions.
	KeyboardInteractive func(hostName, name, instruction string, questions []string, echos []bool) ([]string, error)

	// OnStatus is notified when a host's connection status changes.
	OnStatus func(hostID string, status model.ConnectionStatus)

	mu      sync.Mutex
	clients map[string]*sshclient.Client
	status  map[string]model.ConnectionStatus
}

// New builds a runtime around a store.
func New(store *config.Store) *Runtime {
	rt := &Runtime{
		Store:    store,
		Forwards: sshclient.NewForwardManager(),
		Tr:       i18n.New(store.Settings.UI.Language),
		clients:  map[string]*sshclient.Client{},
		status:   map[string]model.ConnectionStatus{},
	}
	cache := sshclient.NewAuthCache()
	// Seed the in-memory cache with saved passphrases; they are never written
	// back out by the cache.
	for _, h := range store.Hosts {
		if pp, ok := store.Passphrase(h); ok {
			for _, id := range h.IdentityFiles {
				cache.SetPassphrase(id, pp)
			}
		}
	}
	rt.Dialer = &sshclient.Dialer{
		Prompts:        rt.prompts(),
		Cache:          cache,
		Timeout:        20 * time.Second,
		KnownHostsPath: store.Paths.KnownHosts,
		// Unknown keys go through HostKeyPrompt; a nil prompt accepts them on
		// first use (TOFU). Changed keys are refused regardless.
		AcceptNewHostKeys: true,
		AskHostKeys:       true,
	}
	rt.Forwards.Dialer = rt.Dialer
	return rt
}

func (rt *Runtime) prompts() sshclient.Prompts {
	return sshclient.Prompts{
		Password: func(hostName string, attempt int) (string, error) {
			if rt.PasswordPrompt == nil {
				return "", fmt.Errorf("no password stored for %s", hostName)
			}
			return rt.PasswordPrompt(hostName, attempt)
		},
		Passphrase: func(keyPath string) (string, error) {
			if rt.PassphrasePrompt == nil {
				return "", fmt.Errorf("no passphrase stored for %s", keyPath)
			}
			return rt.PassphrasePrompt(keyPath, 1)
		},
		HostKey: func(host, fingerprint, keyType string) (bool, error) {
			if rt.HostKeyPrompt == nil {
				return true, nil
			}
			return rt.HostKeyPrompt(host, fingerprint, keyType)
		},
		KeyboardInteractive: func(hostName, name, instruction string, questions []string, echos []bool) ([]string, error) {
			if rt.KeyboardInteractive == nil {
				return nil, fmt.Errorf("keyboard-interactive authentication is not available")
			}
			return rt.KeyboardInteractive(hostName, name, instruction, questions, echos)
		},
	}
}

// Resolver returns stored credentials for a host, for the dialer's convenience
// callbacks. It is what lets the engine try a saved password before prompting.
func (rt *Runtime) Resolver() sshclient.PasswordResolver {
	return func(h model.Host) (string, bool) {
		if pw, ok := rt.Store.Password(h); ok {
			return pw, true
		}
		return "", false
	}
}

// Status returns a host's cached status.
func (rt *Runtime) Status(hostID string) model.ConnectionStatus {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if s, ok := rt.status[hostID]; ok {
		return s
	}
	return model.StatusUnknown
}

func (rt *Runtime) setStatus(hostID string, status model.ConnectionStatus) {
	rt.mu.Lock()
	rt.status[hostID] = status
	cb := rt.OnStatus
	rt.mu.Unlock()
	if cb != nil {
		cb(hostID, status)
	}
}

// Client returns the live connection for a host, if any.
func (rt *Runtime) Client(hostID string) (*sshclient.Client, bool) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	c, ok := rt.clients[hostID]
	return c, ok
}

// Connect dials a host and remembers the connection.
func (rt *Runtime) Connect(ctx context.Context, host model.Host) (*sshclient.Client, error) {
	rt.setStatus(host.ID, model.StatusConnecting)
	client, err := rt.Dialer.Dial(ctx, host, rt.Store.Hosts, rt.Resolver())
	if err != nil {
		rt.setStatus(host.ID, model.StatusOffline)
		return nil, err
	}
	rt.mu.Lock()
	rt.clients[host.ID] = client
	rt.mu.Unlock()
	rt.setStatus(host.ID, model.StatusOnline)
	if host.TunnelAutostart || len(host.LocalForwards) > 0 || host.DynamicForward != "" {
		_ = rt.Forwards.StartHost(ctx, client, host)
	}
	return client, nil
}

// Disconnect closes a host's connection and its tunnels.
func (rt *Runtime) Disconnect(hostID string) {
	rt.Forwards.StopHost(hostID)
	rt.mu.Lock()
	client := rt.clients[hostID]
	delete(rt.clients, hostID)
	rt.mu.Unlock()
	if client != nil {
		_ = client.Close()
	}
	rt.setStatus(hostID, model.StatusOffline)
}

// Metrics collects a dashboard sample for a host, connecting first if needed.
func (rt *Runtime) Metrics(ctx context.Context, host model.Host) model.Metrics {
	client, ok := rt.Client(host.ID)
	if !ok {
		var err error
		client, err = rt.Connect(ctx, host)
		if err != nil {
			return model.Metrics{Hostname: host.Name, CollectedAt: time.Now(), Error: err.Error()}
		}
	}
	return sshclient.Collect(ctx, client, host)
}

// Probe checks TCP reachability for hosts that are monitored without SSH.
func (rt *Runtime) Probe(ctx context.Context, host model.Host) sshclient.ProbeResult {
	addr := host.Address()
	if host.MonitorPort > 0 {
		addr = net.JoinHostPort(host.Hostname, fmt.Sprint(host.MonitorPort))
	}
	return sshclient.ProbeTCP(ctx, addr, 5*time.Second)
}

// Save persists the store and applies the language to the translator.
func (rt *Runtime) Save() error {
	rt.Tr.SetLang(rt.Store.Settings.UI.Language)
	return rt.Store.Save()
}

// Close releases every connection and tunnel.
func (rt *Runtime) Close() {
	rt.Forwards.StopAll()
	rt.mu.Lock()
	clients := rt.clients
	rt.clients = map[string]*sshclient.Client{}
	rt.mu.Unlock()
	for _, c := range clients {
		_ = c.Close()
	}
}
