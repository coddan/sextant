package putty

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"unicode/utf16"
)

// SessionsKey is where PuTTY stores its saved sessions.
const SessionsKey = `HKEY_CURRENT_USER\Software\SimonTatham\PuTTY\Sessions`

// Session is one saved PuTTY session. Values is keyed by the registry value
// name exactly as exported, so unknown values are preserved for future use.
type Session struct {
	Name   string
	String map[string]string
	DWord  map[string]uint32
}

// Value returns a string value, or "" when it is absent.
func (s Session) Value(name string) string { return s.String[name] }

// Num returns a DWORD value, or 0 when it is absent.
func (s Session) Num(name string) uint32 { return s.DWord[name] }

// IsSSH reports whether the session uses the SSH protocol. PuTTY defaults to
// SSH when the value is missing.
func (s Session) IsSSH() bool {
	p := strings.ToLower(s.Value("Protocol"))
	return p == "" || p == "ssh"
}

// ParseReg reads a registry export as produced by `reg export` or regedit.
//
// Both the UTF-16LE exports of modern Windows and the older ANSI "REGEDIT4"
// files are supported, because users have both kinds lying around.
func ParseReg(data []byte) ([]Session, error) {
	data = decodeRegBytes(data)
	var sessions []Session
	var cur *Session

	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, ";") || strings.HasPrefix(line, "Windows Registry Editor") || strings.HasPrefix(line, "REGEDIT") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			key := strings.TrimSuffix(strings.TrimPrefix(line, "["), "]")
			name, ok := sessionName(key)
			if !ok {
				cur = nil
				continue
			}
			sessions = append(sessions, Session{
				Name:   name,
				String: map[string]string{},
				DWord:  map[string]uint32{},
			})
			cur = &sessions[len(sessions)-1]
			continue
		}
		if cur == nil {
			continue
		}
		name, value, isDword, err := parseValue(line)
		if err != nil {
			continue
		}
		if isDword {
			cur.DWord[name] = value.(uint32)
		} else {
			cur.String[name] = value.(string)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return sessions, nil
}

// LoadRegFile reads and parses a .reg file from disk.
func LoadRegFile(path string) ([]Session, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	sessions, err := ParseReg(data)
	if err != nil {
		return nil, fmt.Errorf("putty: parse %s: %w", path, err)
	}
	return sessions, nil
}

// ReadRegistry exports the PuTTY sessions key and parses it. It is only
// available on Windows, where the sessions actually live in the registry.
func ReadRegistry() ([]Session, error) {
	if runtime.GOOS != "windows" {
		return nil, errors.New("putty: reading the Windows registry is only possible on Windows; import a .reg file instead")
	}
	tmp, err := os.CreateTemp("", "sextant-putty-*.reg")
	if err != nil {
		return nil, err
	}
	path := tmp.Name()
	_ = tmp.Close()
	defer os.Remove(path)

	cmd := exec.Command("reg", "export", SessionsKey, path, "/y")
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("putty: reg export failed: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return LoadRegFile(path)
}

// sessionName extracts the session name from a registry key path, returning
// false for keys that are not sessions (for example Default%20Settings).
func sessionName(key string) (string, bool) {
	idx := strings.LastIndex(strings.ToLower(key), strings.ToLower(`\Sessions\`))
	if idx < 0 {
		return "", false
	}
	name := key[idx+len(`\Sessions\`):]
	if name == "" {
		return "", false
	}
	return decodeRegName(name), true
}

// decodeRegName reverses PuTTY's percent-encoding of session names.
func decodeRegName(name string) string {
	if !strings.Contains(name, "%") {
		return name
	}
	decoded, err := url.PathUnescape(name)
	if err != nil {
		return name
	}
	return decoded
}

// parseValue reads one `"Name"=value` line.
func parseValue(line string) (name string, value any, isDword bool, err error) {
	if !strings.HasPrefix(line, "\"") {
		return "", nil, false, errors.New("not a value")
	}
	closeQuote := indexUnescaped(line, '"', 1)
	if closeQuote < 0 {
		return "", nil, false, errors.New("unterminated name")
	}
	name = unescapeRegString(line[1:closeQuote])
	rest := strings.TrimSpace(line[closeQuote+1:])
	rest = strings.TrimPrefix(rest, "=")
	rest = strings.TrimSpace(rest)

	switch {
	case strings.HasPrefix(rest, "dword:"):
		hexVal := strings.TrimPrefix(rest, "dword:")
		n, err := strconv.ParseUint(hexVal, 16, 32)
		if err != nil {
			return "", nil, false, err
		}
		return name, uint32(n), true, nil
	case strings.HasPrefix(rest, "\""):
		end := indexUnescaped(rest, '"', 1)
		if end < 0 {
			return "", nil, false, errors.New("unterminated value")
		}
		return name, unescapeRegString(rest[1:end]), false, nil
	default:
		// hex:, hex(2): and friends carry binary data we do not use.
		return "", nil, false, errors.New("unsupported value type")
	}
}

// indexUnescaped finds the next quote at or after start that is not preceded by
// a backslash escape.
func indexUnescaped(s string, ch byte, start int) int {
	for i := start; i < len(s); i++ {
		if s[i] != ch {
			continue
		}
		backslashes := 0
		for j := i - 1; j >= 0 && s[j] == '\\'; j-- {
			backslashes++
		}
		if backslashes%2 == 0 {
			return i
		}
	}
	return -1
}

// unescapeRegString reverses the `\\` and `\"` escapes used inside .reg string
// values.
func unescapeRegString(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) && (s[i+1] == '\\' || s[i+1] == '"') {
			i++
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// decodeRegBytes converts a UTF-16LE registry export into UTF-8. ANSI files are
// returned unchanged.
func decodeRegBytes(data []byte) []byte {
	if len(data) >= 2 && data[0] == 0xFF && data[1] == 0xFE {
		u16 := make([]uint16, 0, (len(data)-2)/2)
		for i := 2; i+1 < len(data); i += 2 {
			u16 = append(u16, uint16(data[i])|uint16(data[i+1])<<8)
		}
		return []byte(string(utf16.Decode(u16)))
	}
	return data
}
