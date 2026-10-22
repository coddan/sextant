package tui

import (
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/coddan/sextant/internal/app"
	"github.com/coddan/sextant/internal/i18n"
	"github.com/coddan/sextant/internal/model"
)

type formKind int

const (
	formText formKind = iota
	formSecret
	formToggle
	formChoice
)

// formField is one line of the host editor.
type formField struct {
	key     string
	label   string
	kind    formKind
	input   textinput.Model
	toggled bool
	choices []string
	choice  int
}

// hostForm is the add/edit host editor.
type hostForm struct {
	title    string
	host     model.Host
	isNew    bool
	fields   []formField
	focus    int
	err      string
	password string
	passphr  string
	tr       *i18n.Translator
}

func newHostForm(tr *i18n.Translator, title string, host model.Host) *hostForm {
	isNew := host.ID == ""
	f := &hostForm{title: title, host: host, isNew: isNew, tr: tr}
	groups := []string{""}
	if isNew {
		host.Port = 22
	}

	f.text(i18n.HostName, host.Name, 128)
	f.text(i18n.HostHostname, host.Hostname, 255)
	f.text(i18n.HostUser, host.User, 64)
	f.text(i18n.HostPort, strconv.Itoa(host.Port), 5)
	f.choice(i18n.HostGroup, groups, indexOf(groups, host.Group))
	f.secret(i18n.HostPassword, "", 255)
	f.text(i18n.HostIdentityFiles, strings.Join(host.IdentityFiles, ", "), 512)
	f.text(i18n.HostCertificateFile, host.CertificateFile, 512)
	f.text(i18n.HostProxyJump, host.ProxyJump, 255)
	f.text(i18n.HostLocalForwards, strings.Join(host.LocalForwards, ", "), 512)
	f.text(i18n.HostDynamicForward, host.DynamicForward, 64)
	f.choice(i18n.HostMonitoring, []string{"ssh", "tcp"}, monitorIndex(host.Monitoring))
	f.toggle(i18n.HostIdentitiesOnly, host.IdentitiesOnly)
	f.toggle(i18n.HostForwardAgent, host.ForwardAgent)
	f.toggle(i18n.HostTunnelAutostart, host.TunnelAutostart)
	f.toggle(i18n.HostHidden, host.Hidden)
	f.text(i18n.HostNotes, host.Notes, 512)

	f.fields[0].input.Focus()
	return f
}

func (f *hostForm) label(key i18n.Key) string { return f.tr.T(key) }

func (f *hostForm) text(key i18n.Key, value string, limit int) {
	in := textinput.New()
	in.CharLimit = limit
	in.SetValue(value)
	f.fields = append(f.fields, formField{key: string(key), label: f.label(key), kind: formText, input: in})
}

func (f *hostForm) secret(key i18n.Key, value string, limit int) {
	in := textinput.New()
	in.CharLimit = limit
	in.EchoMode = textinput.EchoPassword
	in.EchoCharacter = '•'
	in.SetValue(value)
	f.fields = append(f.fields, formField{key: string(key), label: f.label(key), kind: formSecret, input: in})
}

func (f *hostForm) toggle(key i18n.Key, on bool) {
	f.fields = append(f.fields, formField{key: string(key), label: f.label(key), kind: formToggle, toggled: on})
}

func (f *hostForm) choice(key i18n.Key, choices []string, idx int) {
	f.fields = append(f.fields, formField{key: string(key), label: f.label(key), kind: formChoice, choices: choices, choice: idx})
}

func (f *hostForm) setGroups(groups []string) {
	for i := range f.fields {
		if f.fields[i].key == string(i18n.HostGroup) {
			opts := append([]string{""}, groups...)
			f.fields[i].choices = opts
			f.fields[i].choice = minInt(f.fields[i].choice, len(opts)-1)
		}
	}
}

// moveFocus advances focus by delta, wrapping.
func (f *hostForm) moveFocus(delta int) {
	n := len(f.fields)
	f.focus = (f.focus + delta + n) % n
	for i := range f.fields {
		if i == f.focus && f.fields[i].kind != formToggle && f.fields[i].kind != formChoice {
			f.fields[i].input.Focus()
		} else {
			f.fields[i].input.Blur()
		}
	}
}

func (f *hostForm) focused() *formField {
	if f.focus < 0 || f.focus >= len(f.fields) {
		return nil
	}
	return &f.fields[f.focus]
}

func (f *hostForm) updateInput(msg tea.Msg) tea.Cmd {
	if fl := f.focused(); fl != nil && (fl.kind == formText || fl.kind == formSecret) {
		var cmd tea.Cmd
		fl.input, cmd = fl.input.Update(msg)
		return cmd
	}
	return nil
}

// cycle changes a choice or toggle field.
func (f *hostForm) cycle(delta int) {
	fl := f.focused()
	if fl == nil {
		return
	}
	switch fl.kind {
	case formToggle:
		fl.toggled = !fl.toggled
	case formChoice:
		if len(fl.choices) == 0 {
			return
		}
		fl.choice = (fl.choice + delta + len(fl.choices)) % len(fl.choices)
	}
}

// save validates and writes the form back to the store.
func (f *hostForm) save(rt *app.Runtime) error {
	name := f.value(i18n.HostName)
	hostname := f.value(i18n.HostHostname)
	if hostname == "" {
		return errFieldRequired(string(i18n.HostHostname))
	}
	if name == "" {
		name = hostname
	}
	port := 22
	if p := f.value(i18n.HostPort); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil || n < 1 || n > 65535 {
			return errFieldRequired(string(i18n.ErrorInvalidPort))
		}
		port = n
	}

	host := f.host
	if host.ID == "" {
		host = model.NewHost(name, hostname)
	}
	host.Name = name
	host.Hostname = hostname
	host.User = f.value(i18n.HostUser)
	host.Port = port
	host.Group = f.value(i18n.HostGroup)
	host.IdentityFiles = splitList(f.value(i18n.HostIdentityFiles))
	host.CertificateFile = f.value(i18n.HostCertificateFile)
	host.ProxyJump = f.value(i18n.HostProxyJump)
	host.LocalForwards = splitList(f.value(i18n.HostLocalForwards))
	host.DynamicForward = f.value(i18n.HostDynamicForward)
	host.Notes = f.value(i18n.HostNotes)
	host.Monitoring = model.MonitorMode(orDefault(f.value(i18n.HostMonitoring), string(model.MonitorSSH)))
	host.IdentitiesOnly = f.boolValue(i18n.HostIdentitiesOnly)
	host.ForwardAgent = f.boolValue(i18n.HostForwardAgent)
	host.TunnelAutostart = f.boolValue(i18n.HostTunnelAutostart)
	host.Hidden = f.boolValue(i18n.HostHidden)

	if pw := f.value(i18n.HostPassword); pw != "" {
		if err := rt.Store.HostPassword(&host, pw); err != nil {
			return err
		}
	}
	if host.Group != "" {
		rt.Store.AddGroup(host.Group)
	}
	rt.Store.UpsertHost(host)
	return rt.Save()
}

func (f *hostForm) value(label i18n.Key) string {
	for i := range f.fields {
		if f.fields[i].key == string(label) {
			if f.fields[i].kind == formChoice {
				if len(f.fields[i].choices) == 0 {
					return ""
				}
				return f.fields[i].choices[f.fields[i].choice]
			}
			return strings.TrimSpace(f.fields[i].input.Value())
		}
	}
	return ""
}

func (f *hostForm) boolValue(label i18n.Key) bool {
	for i := range f.fields {
		if f.fields[i].key == string(label) {
			return f.fields[i].toggled
		}
	}
	return false
}

func splitList(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	parts := strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == '\n' })
	var out []string
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func indexOf(list []string, value string) int {
	for i, v := range list {
		if v == value {
			return i
		}
	}
	return 0
}

func monitorIndex(m model.MonitorMode) int {
	if m == model.MonitorTCP {
		return 1
	}
	return 0
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

type errFieldRequired string

func (e errFieldRequired) Error() string { return string(e) + ": required" }
