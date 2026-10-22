package tui

import (
	"errors"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/coddan/sextant/internal/i18n"
)

// errPromptCancelled aborts an authentication attempt the user cancelled.
var errPromptCancelled = errors.New("tui: authentication prompt cancelled")

// promptState is the modal shown for host-key decisions and credential prompts.
type promptState struct {
	req      promptRequest
	input    textinput.Model
	confirm  bool
	answers  []string
	index    int
	question string
	err      string
}

func newPromptState(req promptRequest) *promptState {
	p := &promptState{req: req}
	switch req.kind {
	case promptHostKey:
		p.confirm = true
	case promptKeyboard:
		p.answers = make([]string, len(req.questions))
		p.question = firstString(req.questions)
		p.input = secretInput(2048, firstEcho(req.echos))
	case promptPassphrase:
		p.input = secretInput(1024, false)
	default: // password
		p.input = secretInput(1024, false)
	}
	if !p.confirm {
		p.input.Focus()
	}
	return p
}

func secretInput(limit int, echo bool) textinput.Model {
	in := textinput.New()
	in.CharLimit = limit
	in.Width = 40
	if !echo {
		in.EchoMode = textinput.EchoPassword
		in.EchoCharacter = '•'
	}
	return in
}

// handleKey processes a keystroke, returning true when the prompt is answered.
func (p *promptState) handleKey(key tea.KeyMsg) (bool, promptReply) {
	if p.confirm {
		switch key.String() {
		case "y", "Y", "enter":
			return true, promptReply{ok: true}
		case "n", "N", "esc", "q":
			return true, promptReply{ok: false, err: errPromptCancelled}
		}
		return false, promptReply{}
	}
	switch key.Type {
	case tea.KeyEsc:
		return true, promptReply{err: errPromptCancelled}
	case tea.KeyEnter:
		if p.req.kind == promptKeyboard && p.index < len(p.req.questions)-1 {
			p.answers[p.index] = p.input.Value()
			p.index++
			p.input.SetValue("")
			p.question = p.req.questions[p.index]
			p.input.EchoMode = echoModeFor(p.req.echos, p.index)
			return false, promptReply{}
		}
		if p.req.kind == promptKeyboard {
			p.answers[p.index] = p.input.Value()
			return true, promptReply{values: p.answers}
		}
		return true, promptReply{value: p.input.Value()}
	}
	var cmd tea.Cmd
	p.input, cmd = p.input.Update(key)
	_ = cmd
	return false, promptReply{}
}

func echoModeFor(echos []bool, index int) textinput.EchoMode {
	if index < len(echos) && echos[index] {
		return textinput.EchoNormal
	}
	return textinput.EchoPassword
}

// view renders the modal.
func (p *promptState) view(m *Model, width int) string {
	var b string
	switch p.req.kind {
	case promptHostKey:
		b = m.tr(i18n.HostKeyChangedBody, p.req.host)
		if p.req.keyType != "" {
			b = m.tr(i18n.HostKeyUnknownBody, p.req.host)
		}
		if p.req.detail != "" {
			b += "\n" + m.tr(i18n.HostKeyFingerprint) + ": " + p.req.detail
		}
		b += "\n\n" + m.tr(i18n.HostKeyAccept) + " [y]    " + m.tr(i18n.HostKeyReject) + " [n]"
	case promptKeyboard:
		b = p.req.host
		if p.req.detail != "" {
			b += "\n" + p.req.detail
		}
		b += "\n\n" + p.question + "\n" + p.input.View()
	case promptPassphrase:
		b = m.tr(i18n.HostPassphrase) + ": " + p.req.host + "\n" + p.input.View()
	default:
		b = m.tr(i18n.HostPassword) + " (" + p.req.host + ")\n" + p.input.View()
	}
	if p.err != "" {
		b += "\n" + errorStyle.Render(p.err)
	}
	return boxStyle.Width(minInt(width-4, 72)).Render(b)
}

func firstString(list []string) string {
	if len(list) == 0 {
		return ""
	}
	return list[0]
}

func firstEcho(echos []bool) bool {
	if len(echos) == 0 {
		return false
	}
	return echos[0]
}
