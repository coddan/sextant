package putty

import (
	"strings"
	"testing"
	"unicode/utf16"
)

const sampleReg = `Windows Registry Editor Version 5.00

[HKEY_CURRENT_USER\Software\SimonTatham\PuTTY\Sessions\Default%20Settings]
"HostName"=""
"Protocol"="ssh"

[HKEY_CURRENT_USER\Software\SimonTatham\PuTTY\Sessions\my%20web%20server]
"HostName"="web.example.com"
"UserName"="alice"
"PortNumber"=dword:000008ae
"Protocol"="ssh"
"LocalPortForwardings"="L8080:localhost:80,L9000:db:5432"
"PublicKeyFile"="C:\\Users\\alice\\keys\\id_rsa.ppk"

[HKEY_CURRENT_USER\Software\SimonTatham\PuTTY\Sessions\telnet%20box]
"HostName"="telnet.example.com"
"Protocol"="telnet"
"PortNumber"=dword:00000017

[HKEY_CURRENT_USER\Software\SimonTatham\PuTTY\Sessions\proxied]
"HostName"="proxied.example.com"
"Protocol"="ssh"
"ProxyType"=dword:00000003
"ProxyHost"="proxy.example.com"
"ProxyPort"=dword:00000438
"RemotePortForwardings"="R5000:localhost:5000"
`

func TestParseReg(t *testing.T) {
	sessions, err := ParseReg([]byte(sampleReg))
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 4 {
		t.Fatalf("sessions = %d: %+v", len(sessions), sessions)
	}
	web := sessions[1]
	if web.Name != "my web server" {
		t.Fatalf("name = %q", web.Name)
	}
	if web.Value("HostName") != "web.example.com" || web.Value("UserName") != "alice" {
		t.Fatalf("values = %+v", web.String)
	}
	if web.Num("PortNumber") != 2222 {
		t.Fatalf("port = %d", web.Num("PortNumber"))
	}
	// Backslash escapes are unescaped.
	if got := web.Value("PublicKeyFile"); got != `C:\Users\alice\keys\id_rsa.ppk` {
		t.Fatalf("public key file = %q", got)
	}
}

func TestParseRegUTF16(t *testing.T) {
	data := encodeUTF16LE(sampleReg)
	sessions, err := ParseReg(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 4 {
		t.Fatalf("sessions = %d", len(sessions))
	}
	if sessions[1].Name != "my web server" {
		t.Fatalf("name = %q", sessions[1].Name)
	}
}

func TestImportSessions(t *testing.T) {
	sessions, _ := ParseReg([]byte(sampleReg))
	res := ImportSessions(sessions, ImportOptions{})

	// Default Settings (empty host) and the telnet session are skipped.
	if len(res.Hosts) != 2 {
		t.Fatalf("hosts = %+v", res.Hosts)
	}
	if len(res.Warnings) != 2 {
		t.Fatalf("warnings = %v", res.Warnings)
	}

	byName := map[string]int{}
	for i, h := range res.Hosts {
		byName[h.Name] = i
	}
	web := res.Hosts[byName["my web server"]]
	if web.Hostname != "web.example.com" || web.Port != 2222 || web.Source != "putty" {
		t.Fatalf("web = %+v", web)
	}
	if len(web.LocalForwards) != 2 || web.LocalForwards[0] != "8080:localhost:80" {
		t.Fatalf("forwards = %v", web.LocalForwards)
	}
	if len(web.IdentityFiles) != 1 {
		t.Fatalf("identity files = %v", web.IdentityFiles)
	}

	proxied := res.Hosts[byName["proxied"]]
	if !strings.Contains(proxied.Notes, "SOCKS5") || !strings.Contains(proxied.Notes, "proxy.example.com:1080") {
		t.Fatalf("notes = %q", proxied.Notes)
	}
	// Remote forwards are not local forwards.
	if len(proxied.LocalForwards) != 0 {
		t.Fatalf("proxied forwards = %v", proxied.LocalForwards)
	}
}

func TestImportSessionsGroupAndTimestamp(t *testing.T) {
	sessions, _ := ParseReg([]byte(sampleReg))
	res := ImportSessions(sessions, ImportOptions{Group: "PuTTY"})
	for _, h := range res.Hosts {
		if h.Group != "PuTTY" {
			t.Fatalf("group = %q", h.Group)
		}
		if h.CreatedAt.IsZero() {
			t.Fatal("timestamp not set")
		}
	}
}

func encodeUTF16LE(s string) []byte {
	u := utf16.Encode([]rune(s))
	out := []byte{0xFF, 0xFE}
	for _, r := range u {
		out = append(out, byte(r), byte(r>>8))
	}
	return out
}
