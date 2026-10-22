package putty

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/coddan/sextant/internal/model"
)

// ImportOptions configures how sessions become Sextant hosts.
type ImportOptions struct {
	// KeyDir is where converted PPK keys are written. Defaults to ~/.ssh.
	KeyDir string
	// ConvertKeys rewrites .ppk identity files into OpenSSH format, because
	// Sextant reads OpenSSH private keys only.
	ConvertKeys bool
	// Passphrase is called for encrypted PPK keys. Returning an error skips
	// the key and records a warning. May be nil.
	Passphrase func(keyPath string, info KeyInfo) ([]byte, error)
	// Group, when set, is assigned to every imported host.
	Group string
	// Now stamps the imported hosts; defaults to the current time.
	Now time.Time
}

// ImportResult is the outcome of an import run.
type ImportResult struct {
	Hosts    []model.Host
	Warnings []string
}

func (r *ImportResult) warn(format string, args ...any) {
	r.Warnings = append(r.Warnings, fmt.Sprintf(format, args...))
}

// ImportSessions converts PuTTY sessions into hosts. Sessions that are not SSH
// sessions are skipped with a warning rather than failing the whole import.
func ImportSessions(sessions []Session, opts ImportOptions) ImportResult {
	now := opts.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	result := ImportResult{}
	for _, s := range sessions {
		if !s.IsSSH() {
			result.warn("skipped %q: protocol is %q, not SSH", s.Name, s.Value("Protocol"))
			continue
		}
		if s.Value("HostName") == "" || s.Value("HostName") == "-" {
			result.warn("skipped %q: no host name", s.Name)
			continue
		}
		host := s.ToHost(now)
		host.Group = opts.Group
		if len(host.LocalForwards) == 0 {
			host.LocalForwards = parsePortForwardings(s.Value("LocalPortForwardings"), "L")
		}
		if len(host.LocalForwards) == 0 {
			host.LocalForwards = parsePortForwardings(s.Value("PortForwardings"), "L")
		}
		if proxy := describeProxy(s); proxy != "" {
			host.Notes = strings.TrimSpace(host.Notes + "\n" + proxy)
		}
		if opts.ConvertKeys {
			if keyPath := s.Value("PublicKeyFile"); keyPath != "" && IsPPK(keyPath) {
				converted, err := convertIdentity(keyPath, host.Name, opts)
				if err != nil {
					result.warn("key for %q: %v", s.Name, err)
				} else {
					host.IdentityFiles = []string{converted}
				}
			}
		}
		result.Hosts = append(result.Hosts, host)
	}
	return result
}

// ToHost maps the registry values we understand onto the shared data model.
func (s Session) ToHost(now time.Time) model.Host {
	name := s.Name
	if name == "" {
		name = s.Value("HostName")
	}
	hostname := s.Value("HostName")
	if hostname == "-" {
		hostname = ""
	}
	host := model.NewHost(name, hostname)
	host.Source = model.SourcePutty
	host.CreatedAt = now
	host.UpdatedAt = now

	if user := s.Value("UserName"); user != "" && user != "-" {
		host.User = user
	}
	if port := s.Num("PortNumber"); port > 0 {
		host.Port = int(port)
	}
	if host.Port == 0 {
		host.Port = 22
	}
	if keyPath := s.Value("PublicKeyFile"); keyPath != "" && keyPath != "-" {
		host.IdentityFiles = []string{keyPath}
	}
	return host
}

// parsePortForwardings reads PuTTY's comma separated forwarding list. Each
// entry may carry a leading direction letter (L/R/D) in the registry form.
func parsePortForwardings(value, wantDir string) []string {
	if value == "" || value == "-" {
		return nil
	}
	var out []string
	for _, entry := range strings.Split(value, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		dir := wantDir
		if len(entry) > 0 && (entry[0] == 'L' || entry[0] == 'R' || entry[0] == 'D') {
			dir = string(entry[0])
			entry = entry[1:]
		}
		if dir != wantDir {
			continue
		}
		out = append(out, entry)
	}
	return out
}

// describeProxy renders a PuTTY proxy configuration as a note. Sextant models
// jump hosts, not arbitrary proxies, so pretending the proxy was imported would
// be worse than telling the user.
func describeProxy(s Session) string {
	proxyType := s.Num("ProxyType")
	if proxyType == 0 {
		return ""
	}
	names := map[uint32]string{
		1: "HTTP", 2: "SOCKS4", 3: "SOCKS5", 4: "Telnet", 5: "local command",
	}
	kind := names[proxyType]
	if kind == "" {
		kind = fmt.Sprintf("type %d", proxyType)
	}
	host := s.Value("ProxyHost")
	port := s.Num("ProxyPort")
	if proxyType == 5 {
		return fmt.Sprintf("PuTTY proxy: %s (%s)", kind, s.Value("ProxyTelnetCommand"))
	}
	if host == "" {
		return fmt.Sprintf("PuTTY proxy: %s", kind)
	}
	return fmt.Sprintf("PuTTY proxy: %s via %s:%s", kind, host, strconv.Itoa(int(port)))
}

// convertIdentity writes an OpenSSH copy of a PPK key next to the user's other
// keys and returns the new path.
func convertIdentity(keyPath, hostName string, opts ImportOptions) (string, error) {
	info, err := Inspect(keyPath)
	if err != nil {
		return "", err
	}
	var passphrase []byte
	if info.Encrypted {
		if opts.Passphrase == nil {
			return "", fmt.Errorf("%s is encrypted and no passphrase source is configured", keyPath)
		}
		passphrase, err = opts.Passphrase(keyPath, info)
		if err != nil {
			return "", err
		}
	}
	converted, err := Convert(keyPath, passphrase)
	if err != nil {
		return "", err
	}

	dir := opts.KeyDir
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		dir = filepath.Join(home, ".ssh")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	base := filepath.Join(dir, "sextant_putty_"+sanitize(hostName))
	if err := writeKeyPair(base, converted); err != nil {
		return "", err
	}
	return base, nil
}

func writeKeyPair(base string, converted ConvertResult) error {
	tmp := base + ".tmp"
	if err := os.WriteFile(tmp, converted.PrivateKey, 0o600); err != nil {
		return err
	}
	_ = os.Chmod(tmp, 0o600)
	if err := os.Rename(tmp, base); err != nil {
		return err
	}
	pub := converted.PublicKey
	if !strings.HasSuffix(pub, "\n") {
		pub += "\n"
	}
	return os.WriteFile(base+".pub", []byte(pub), 0o644)
}

// sanitize makes a host name safe for use in a filename.
func sanitize(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	out := strings.Trim(b.String(), "._-")
	if out == "" {
		out = "host"
	}
	if len(out) > 64 {
		out = out[:64]
	}
	return out
}
