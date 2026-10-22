package tui

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/coddan/sextant/internal/model"
	"github.com/coddan/sextant/internal/sshclient"
)

// hostsChangedMsg asks the dashboard to re-read the store.
type hostsChangedMsg struct{}

// metricsMsg carries one host's dashboard sample.
type metricsMsg struct {
	hostID string
	m      model.Metrics
}

// statusMsg shows a transient message in the status bar.
type statusMsg string

// errMsg shows an error in the status bar.
type errMsg struct{ err error }

// connectedMsg reports the outcome of a connection attempt.
type connectedMsg struct {
	host   model.Host
	client *sshclient.Client
	err    error
}

// termMsg carries output from the remote shell or its end.
type termMsg struct {
	data   []byte
	err    error
	closed bool
}

// promptMsg delivers a blocking authentication prompt to the update loop.
type promptMsg struct{ req promptRequest }

// promptKind selects the prompt layout.
type promptKind int

const (
	promptHostKey promptKind = iota
	promptPassword
	promptPassphrase
	promptKeyboard
)

// promptRequest is sent from a dialing goroutine and answered from the UI.
type promptRequest struct {
	kind      promptKind
	host      string
	detail    string
	keyType   string
	questions []string
	echos     []bool
	reply     chan promptReply
}

// promptReply is the user's answer.
type promptReply struct {
	ok     bool
	value  string
	values []string
	err    error
}

// waitPromptCmd waits for the next prompt request and turns it into a message.
func (m *Model) waitPromptCmd() tea.Cmd {
	ch := m.promptCh
	return func() tea.Msg { return promptMsg{req: <-ch} }
}

// waitKeyCmd waits for a message produced by a background panel operation.
func (m *Model) waitKeyCmd() tea.Cmd {
	ch := m.keyCh
	return func() tea.Msg { return <-ch }
}

// askPassword is the dialer's password prompt.
func (m *Model) askPassword(hostName string, attempt int) (string, error) {
	return m.promptValue(promptRequest{kind: promptPassword, host: hostName})
}

// askPassphrase is the dialer's key passphrase prompt.
func (m *Model) askPassphrase(keyPath string, attempt int) (string, error) {
	return m.promptValue(promptRequest{kind: promptPassphrase, host: keyPath})
}

// askHostKey asks before trusting an unknown host key.
func (m *Model) askHostKey(host, fingerprint, keyType string) (bool, error) {
	req := promptRequest{kind: promptHostKey, host: host, detail: fingerprint, keyType: keyType, reply: make(chan promptReply, 1)}
	if !m.sendPrompt(req) {
		return false, m.ctx.Err()
	}
	select {
	case r := <-req.reply:
		return r.ok, r.err
	case <-m.ctx.Done():
		return false, m.ctx.Err()
	}
}

// askKeyboardInteractive answers 2FA and other challenge questions.
func (m *Model) askKeyboardInteractive(hostName, name, instruction string, questions []string, echos []bool) ([]string, error) {
	req := promptRequest{
		kind:      promptKeyboard,
		host:      hostName,
		detail:    instruction,
		questions: questions,
		echos:     echos,
		reply:     make(chan promptReply, 1),
	}
	if !m.sendPrompt(req) {
		return nil, m.ctx.Err()
	}
	select {
	case r := <-req.reply:
		return r.values, r.err
	case <-m.ctx.Done():
		return nil, m.ctx.Err()
	}
}

func (m *Model) promptValue(req promptRequest) (string, error) {
	req.reply = make(chan promptReply, 1)
	if !m.sendPrompt(req) {
		return "", m.ctx.Err()
	}
	select {
	case r := <-req.reply:
		return r.value, r.err
	case <-m.ctx.Done():
		return "", m.ctx.Err()
	}
}

func (m *Model) sendPrompt(req promptRequest) bool {
	select {
	case m.promptCh <- req:
		return true
	case <-m.ctx.Done():
		return false
	}
}
