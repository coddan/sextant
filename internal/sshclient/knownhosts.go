// Package sshclient is the connection engine: authentication, jump hosts,
// host-key verification, PTYs, SFTP, forwarding, metrics and key setup.
package sshclient

import (
	"bufio"
	"crypto/hmac"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"golang.org/x/crypto/ssh"
)

// Verdict is what known_hosts says about a server key.
type Verdict int

const (
	// KeyUnknown means no key is pinned for this host.
	KeyUnknown Verdict = iota
	// KeyKnown means the offered key is pinned.
	KeyKnown
	// KeyChanged means the host has pinned keys and this is not one of them.
	KeyChanged
	// KeyRevoked means the key is explicitly revoked.
	KeyRevoked
)

// KnownHostEntry is one line of known_hosts.
type KnownHostEntry struct {
	Hosts     []string
	Key       ssh.PublicKey
	Marker    string // "", "@cert-authority", "@revoked"
	Line      int
	hashed    bool
	hashSalt  []byte
	hashValue []byte
}

// KnownHosts is a client's known_hosts file.
type KnownHosts struct {
	path    string
	entries []KnownHostEntry
	mu      sync.Mutex
}

// LoadKnownHosts reads path. A missing file is an empty store, not an error:
// Sextant creates it on first use, sharing it with OpenSSH.
func LoadKnownHosts(path string) (*KnownHosts, error) {
	k := &KnownHosts{path: path}
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return k, nil
		}
		return nil, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	line := 0
	for sc.Scan() {
		line++
		entry, ok := parseKnownHostLine(sc.Text())
		if !ok {
			continue
		}
		entry.Line = line
		k.entries = append(k.entries, entry)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return k, nil
}

// Path returns the file path.
func (k *KnownHosts) Path() string { return k.path }

func parseKnownHostLine(line string) (KnownHostEntry, bool) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return KnownHostEntry{}, false
	}
	fields := strings.Fields(line)
	var marker string
	if len(fields) > 0 && strings.HasPrefix(fields[0], "@") {
		marker = fields[0]
		fields = fields[1:]
	}
	if len(fields) < 3 {
		return KnownHostEntry{}, false
	}
	key, err := ssh.ParsePublicKey(mustDecode(fields[2]))
	if err != nil {
		return KnownHostEntry{}, false
	}
	entry := KnownHostEntry{Marker: marker, Key: key}
	for _, host := range strings.Split(fields[0], ",") {
		host = strings.TrimSpace(host)
		if host == "" {
			continue
		}
		if strings.HasPrefix(host, "|1|") {
			parts := strings.Split(host, "|")
			if len(parts) == 4 {
				salt, err1 := base64.StdEncoding.DecodeString(parts[2])
				hash, err2 := base64.StdEncoding.DecodeString(parts[3])
				if err1 == nil && err2 == nil {
					entry.hashed = true
					entry.hashSalt = salt
					entry.hashValue = hash
				}
			}
			continue
		}
		entry.Hosts = append(entry.Hosts, host)
	}
	return entry, true
}

func mustDecode(s string) []byte {
	b, _ := base64.StdEncoding.DecodeString(s)
	return b
}

// Lookup classifies key for host:port.
func (k *KnownHosts) Lookup(host string, port int, key ssh.PublicKey) (Verdict, *KnownHostEntry) {
	candidates := []string{host}
	if port > 0 && port != 22 {
		candidates = append(candidates, "["+host+"]:"+strconv.Itoa(port))
	}
	var found bool
	var changed *KnownHostEntry
	for i := range k.entries {
		e := &k.entries[i]
		if !e.matches(candidates) {
			continue
		}
		if e.Marker == "@cert-authority" {
			if cert, ok := key.(*ssh.Certificate); ok {
				if err := verifyHostCert(host, cert, e.Key); err == nil {
					return KeyKnown, e
				}
			}
			continue
		}
		if e.Marker == "@revoked" && keyEqual(e.Key, key) {
			return KeyRevoked, e
		}
		found = true
		if keyEqual(e.Key, key) {
			return KeyKnown, e
		}
		if changed == nil {
			changed = e
		}
	}
	if changed != nil {
		return KeyChanged, changed
	}
	if found {
		// A key of a type nobody pinned is refused, as ssh(1) does: a
		// man-in-the-middle must not clear the check by offering another type.
		return KeyChanged, nil
	}
	return KeyUnknown, nil
}

func (e *KnownHostEntry) matches(candidates []string) bool {
	if e.hashed {
		for _, c := range candidates {
			if matchHashed(e.hashSalt, e.hashValue, c) {
				return true
			}
		}
		return false
	}
	for _, pattern := range e.Hosts {
		for _, c := range candidates {
			if matchHostPattern(pattern, c) {
				return true
			}
		}
	}
	return false
}

func matchHostPattern(pattern, host string) bool {
	p := strings.ToLower(pattern)
	h := strings.ToLower(host)
	pi, hi := 0, 0
	star, match := -1, 0
	for hi < len(h) {
		if pi < len(p) && (p[pi] == '?' || p[pi] == h[hi]) {
			pi++
			hi++
			continue
		}
		if pi < len(p) && p[pi] == '*' {
			star = pi
			match = hi
			pi++
			continue
		}
		if star >= 0 {
			pi = star + 1
			match++
			hi = match
			continue
		}
		return false
	}
	for pi < len(p) && p[pi] == '*' {
		pi++
	}
	return pi == len(p)
}

func matchHashed(salt, want []byte, host string) bool {
	mac := hmac.New(sha1.New, salt)
	mac.Write([]byte(host))
	return hmac.Equal(mac.Sum(nil), want)
}

func keyEqual(a, b ssh.PublicKey) bool {
	return a != nil && b != nil && a.Type() == b.Type() &&
		hmac.Equal(a.Marshal(), b.Marshal())
}

func verifyHostCert(host string, cert *ssh.Certificate, ca ssh.PublicKey) error {
	if cert.CertType != ssh.HostCert {
		return errors.New("not a host certificate")
	}
	if !keyEqual(cert.SignatureKey, ca) {
		return errors.New("certificate is not signed by this CA")
	}
	checker := &ssh.CertChecker{
		IsHostAuthority: func(auth ssh.PublicKey, _ string) bool { return keyEqual(auth, ca) },
	}
	if len(cert.ValidPrincipals) > 0 {
		principal := host
		if h, _, ok := strings.Cut(host, ":"); ok {
			principal = h
		}
		return checker.CheckCert(principal, cert)
	}
	return checker.CheckCert(host, cert)
}

// Add pins key for host:port, creating the file if needed. The format is the
// one OpenSSH understands; keys are shared, never private.
func (k *KnownHosts) Add(host string, port int, key ssh.PublicKey) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	name := host
	if port > 0 && port != 22 {
		name = "[" + host + "]:" + strconv.Itoa(port)
	}
	line := fmt.Sprintf("%s %s %s\n", name, key.Type(), base64.StdEncoding.EncodeToString(key.Marshal()))
	if err := os.MkdirAll(filepath.Dir(k.path), 0o700); err != nil {
		return err
	}
	// Post-quantum and certificate lines can be long; write in one syscall with
	// O_APPEND so concurrent sessions do not interleave.
	f, err := os.OpenFile(k.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.WriteString(line); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	k.entries = append(k.entries, KnownHostEntry{
		Hosts: []string{name},
		Key:   key,
	})
	return nil
}

// Fingerprint returns the SHA256 fingerprint of a key, as ssh prints it.
func Fingerprint(key ssh.PublicKey) string {
	if key == nil {
		return ""
	}
	sum := sha256.Sum256(key.Marshal())
	return "SHA256:" + base64.RawStdEncoding.EncodeToString(sum[:])
}
