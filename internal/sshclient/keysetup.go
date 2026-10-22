package sshclient

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/coddan/sextant/internal/model"
)

// KeySetupStep identifies a stage of the automated key setup.
type KeySetupStep int

const (
	StepGenerateKey KeySetupStep = iota + 1
	StepCopyPublicKey
	StepVerifyKeyAuth
	StepDisablePassword
	StepReloadSshd
	StepFinalCheck
)

// AllKeySetupSteps lists the steps in order.
func AllKeySetupSteps() []KeySetupStep {
	return []KeySetupStep{
		StepGenerateKey, StepCopyPublicKey, StepVerifyKeyAuth,
		StepDisablePassword, StepReloadSshd, StepFinalCheck,
	}
}

// Description is the text shown next to the step in the UI.
func (s KeySetupStep) Description() string {
	switch s {
	case StepGenerateKey:
		return "Generating Ed25519 key pair"
	case StepCopyPublicKey:
		return "Copying public key to server"
	case StepVerifyKeyAuth:
		return "Verifying key authentication"
	case StepDisablePassword:
		return "Disabling password authentication"
	case StepReloadSshd:
		return "Reloading SSH service"
	case StepFinalCheck:
		return "Final verification"
	}
	return "Unknown step"
}

// KeySetupState is the outcome of the whole process. The distinction between
// FailedSafe and NeedsRollback matters: only the latter means password login
// might already be off.
type KeySetupState string

const (
	StateNotStarted     KeySetupState = "not_started"
	StateInProgress     KeySetupState = "in_progress"
	StateSuccess        KeySetupState = "success"
	StatePartialSuccess KeySetupState = "partial_success"
	StateFailedSafe     KeySetupState = "failed_safe"
	StateNeedsRollback  KeySetupState = "needs_rollback"
	StateRolledBack     KeySetupState = "rolled_back"
)

// KeySetupOptions configures the wizard.
type KeySetupOptions struct {
	// DisablePassword turns off password authentication after the key works.
	// Left off by default: many users want password login kept as a fallback.
	DisablePassword bool
	// KeyDir is where the key pair is written. Defaults to ~/.ssh.
	KeyDir string
	// LogPath records every step. Defaults to a file in the config dir.
	LogPath string
	// Progress is called when a step starts, succeeds or fails.
	Progress func(step KeySetupStep, state KeySetupState, message string)
}

// KeySetupResult reports what happened.
type KeySetupResult struct {
	State                KeySetupState `json:"state"`
	PrivateKeyPath       string        `json:"private_key_path"`
	PublicKeyPath        string        `json:"public_key_path"`
	PublicKey            string        `json:"public_key"`
	HostKeyVerified      bool          `json:"host_key_verified"`
	PasswordAuthDisabled bool          `json:"password_auth_disabled"`
	BackupPath           string        `json:"backup_path,omitempty"`
	Message              string        `json:"message,omitempty"`
	Error                string        `json:"error,omitempty"`
}

// Timeouts for the key setup flow.
const (
	keySetupTotalTimeout = 60 * time.Second
	keySetupStepTimeout  = 15 * time.Second
	maxHostnameLength    = 64
)

var unsafeHostnameChars = regexp.MustCompile(`[^a-zA-Z0-9._-]`)

// sanitizeHostname produces a filesystem-safe fragment for a key filename.
func sanitizeHostname(name string) string {
	clean := unsafeHostnameChars.ReplaceAllString(name, "_")
	clean = strings.Trim(clean, "._-")
	if clean == "" {
		clean = "host"
	}
	if len(clean) > maxHostnameLength {
		clean = clean[:maxHostnameLength]
	}
	return clean
}

// GenerateKeyPair writes a new unencrypted Ed25519 key pair and returns the
// paths. The private key is 0600 and never leaves the machine.
func GenerateKeyPair(hostName, keyDir string) (privPath, pubPath, publicKey string, err error) {
	if keyDir == "" {
		home, herr := os.UserHomeDir()
		if herr != nil {
			return "", "", "", herr
		}
		keyDir = filepath.Join(home, ".ssh")
	}
	if err := os.MkdirAll(keyDir, 0o700); err != nil {
		return "", "", "", err
	}
	_ = os.Chmod(keyDir, 0o700)

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", "", "", err
	}
	comment := "sextant-" + sanitizeHostname(hostName)
	block, err := ssh.MarshalPrivateKey(priv, comment)
	if err != nil {
		return "", "", "", err
	}
	base := filepath.Join(keyDir, "sextant_"+sanitizeHostname(hostName)+"_ed25519")
	privPath = base
	pubPath = base + ".pub"

	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		return "", "", "", err
	}
	authorized := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sshPub))) + " " + comment

	if err := writeFileAtomicMode(privPath, pem.EncodeToMemory(block), 0o600); err != nil {
		return "", "", "", err
	}
	if err := writeFileAtomicMode(pubPath, []byte(authorized+"\n"), 0o644); err != nil {
		return "", "", "", err
	}
	return privPath, pubPath, authorized, nil
}

// buildAuthorizedKeysCommand appends the public key without ever overwriting
// authorized_keys. The key is single-quoted and quotes are escaped, so a key
// comment cannot break out of the shell string.
func buildAuthorizedKeysCommand(publicKey string) (string, error) {
	publicKey = strings.TrimSpace(publicKey)
	if strings.ContainsAny(publicKey, "\n\r\x00") {
		return "", errors.New("keysetup: public key contains invalid characters")
	}
	if !strings.HasPrefix(publicKey, "ssh-ed25519 ") &&
		!strings.HasPrefix(publicKey, "ssh-rsa ") &&
		!strings.HasPrefix(publicKey, "ecdsa-sha2-") &&
		!strings.HasPrefix(publicKey, "sk-") {
		return "", fmt.Errorf("keysetup: unrecognized key type in %q", publicKey)
	}
	escaped := strings.ReplaceAll(publicKey, "'", `'\''`)
	return fmt.Sprintf("mkdir -p ~/.ssh && chmod 700 ~/.ssh && "+
		"grep -qxF '%s' ~/.ssh/authorized_keys 2>/dev/null || echo '%s' >> ~/.ssh/authorized_keys; "+
		"chmod 600 ~/.ssh/authorized_keys", escaped, escaped), nil
}

// forceSshdDirective rewrites existing occurrences and prepends the directive
// when the file has none, because sed alone would leave sshd's compiled-in
// default in place.
func forceSshdDirective(directive, value string) string {
	return fmt.Sprintf(
		"sudo sed -i.bak 's/^#\\?%s.*/%s %s/' /etc/ssh/sshd_config && "+
			"{ sudo grep -qE '^%s[[:space:]]' /etc/ssh/sshd_config || "+
			"sudo sed -i '1i %s %s' /etc/ssh/sshd_config; }",
		directive, directive, value, directive, directive, value)
}

// buildDisablePasswordCommand backs up the config, neutralises Include
// overrides, forces the authentication directives, validates with `sshd -t`,
// and restores the backup if validation fails.
func buildDisablePasswordCommand() string {
	timestamp := time.Now().UTC().Format("20060102_150405")
	password := forceSshdDirective("PasswordAuthentication", "no")
	challenge := forceSshdDirective("ChallengeResponseAuthentication", "no")
	kbd := forceSshdDirective("KbdInteractiveAuthentication", "no")
	pam := forceSshdDirective("UsePAM", "no")
	return fmt.Sprintf(
		`sudo -n true 2>/dev/null || { echo "SEXTANT_NO_SUDO"; exit 1; }; `+
			`sudo cp /etc/ssh/sshd_config /etc/ssh/sshd_config.sextant_backup.%s && `+
			`sudo sed -i.bak 's|^Include /etc/ssh/sshd_config.d/|#Include /etc/ssh/sshd_config.d/|' /etc/ssh/sshd_config && `+
			`%s && %s && %s && %s && `+
			`sudo sshd -t || { echo "SEXTANT_CONFIG_ERROR"; sudo cp /etc/ssh/sshd_config.sextant_backup.%s /etc/ssh/sshd_config; exit 1; }`,
		timestamp, password, challenge, kbd, pam, timestamp)
}

func buildReloadSshdCommand() string {
	return `if command -v systemctl >/dev/null 2>&1; then ` +
		`sudo systemctl reload sshd 2>/dev/null || sudo systemctl reload ssh 2>/dev/null; ` +
		`elif command -v service >/dev/null 2>&1; then ` +
		`sudo service sshd reload 2>/dev/null || sudo service ssh reload 2>/dev/null; ` +
		`else echo "SEXTANT_NO_INIT_SYSTEM"; exit 1; fi`
}

func buildRollbackCommand() string {
	return `BACKUP=$(find /etc/ssh -maxdepth 1 -name 'sshd_config.sextant_backup.*' 2>/dev/null | sort | tail -1); ` +
		`if [ -n "$BACKUP" ]; then sudo cp "$BACKUP" /etc/ssh/sshd_config && ` +
		`(sudo systemctl reload sshd 2>/dev/null || sudo systemctl reload ssh 2>/dev/null || ` +
		`sudo service sshd reload 2>/dev/null || sudo service ssh reload); ` +
		`else echo "SEXTANT_NO_BACKUP"; fi`
}

// SetupKey runs the full automated key setup against host, using the existing
// credentials for the first connection. Password authentication is only
// disabled after key authentication has been verified with a fresh connection.
func (d *Dialer) SetupKey(ctx context.Context, host model.Host, known []model.Host, passwords PasswordResolver, opts KeySetupOptions) (KeySetupResult, error) {
	ctx, cancel := context.WithTimeout(ctx, keySetupTotalTimeout)
	defer cancel()

	logf := func(format string, args ...any) {
		line := fmt.Sprintf("%s %s", time.Now().UTC().Format(time.RFC3339), fmt.Sprintf(format, args...))
		if opts.LogPath == "" {
			return
		}
		f, err := os.OpenFile(opts.LogPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			return
		}
		defer f.Close()
		_, _ = f.WriteString(line + "\n")
	}
	emit := func(step KeySetupStep, state KeySetupState, msg string) {
		logf("[%s] %s: %s", state, step.Description(), msg)
		if opts.Progress != nil {
			opts.Progress(step, state, msg)
		}
	}

	result := KeySetupResult{State: StateInProgress}

	// Step 1: generate the key pair locally.
	emit(StepGenerateKey, StateInProgress, "")
	privPath, pubPath, publicKey, err := GenerateKeyPair(host.Name, opts.KeyDir)
	if err != nil {
		emit(StepGenerateKey, StateFailedSafe, err.Error())
		result.State = StateFailedSafe
		result.Error = err.Error()
		return result, err
	}
	result.PrivateKeyPath = privPath
	result.PublicKeyPath = pubPath
	result.PublicKey = publicKey
	emit(StepGenerateKey, StateSuccess, privPath)

	// The first connection uses the credentials the user already has.
	client, err := d.withStepTimeout(ctx, func(sctx context.Context) (*Client, error) {
		return d.Dial(sctx, host, known, passwords)
	})
	if err != nil {
		result.State = StateFailedSafe
		result.Error = err.Error()
		emit(StepCopyPublicKey, StateFailedSafe, err.Error())
		return result, err
	}
	defer client.Close()

	// Step 2: install the public key.
	emit(StepCopyPublicKey, StateInProgress, "")
	cmd, err := buildAuthorizedKeysCommand(publicKey)
	if err != nil {
		result.State = StateFailedSafe
		result.Error = err.Error()
		emit(StepCopyPublicKey, StateFailedSafe, err.Error())
		return result, err
	}
	if out, _, _, err := client.Exec(ctx, cmd, keySetupStepTimeout); err != nil {
		result.State = StateFailedSafe
		result.Error = fmt.Sprintf("install public key: %v: %s", err, strings.TrimSpace(string(out)))
		emit(StepCopyPublicKey, StateFailedSafe, result.Error)
		return result, err
	}
	emit(StepCopyPublicKey, StateSuccess, "")

	// Step 3: prove the key works before touching sshd_config.
	emit(StepVerifyKeyAuth, StateInProgress, "")
	if err := d.verifyKeyAuth(ctx, host, known, privPath); err != nil {
		result.State = StateFailedSafe
		result.Error = "key verification failed: " + err.Error()
		emit(StepVerifyKeyAuth, StateFailedSafe, result.Error)
		// Clean up the key we installed so we do not leave an unused key.
		_, _, _, _ = client.Exec(ctx, "sed -i '/sextant-/d' ~/.ssh/authorized_keys 2>/dev/null; true", keySetupStepTimeout)
		return result, err
	}
	result.HostKeyVerified = true
	emit(StepVerifyKeyAuth, StateSuccess, "")

	if !opts.DisablePassword {
		result.State = StateSuccess
		result.Message = "Key installed and verified. Password authentication left enabled."
		emit(StepDisablePassword, StateSuccess, "skipped by user preference")
		return result, nil
	}

	// Step 4: disable password authentication.
	emit(StepDisablePassword, StateInProgress, "")
	out, _, _, err := client.Exec(ctx, buildDisablePasswordCommand(), keySetupStepTimeout)
	output := string(out)
	switch {
	case strings.Contains(output, "SEXTANT_NO_SUDO"):
		result.State = StatePartialSuccess
		result.Message = "Key works, but the account has no passwordless sudo, so password authentication was left enabled."
		emit(StepDisablePassword, StatePartialSuccess, result.Message)
		return result, nil
	case strings.Contains(output, "SEXTANT_CONFIG_ERROR"), err != nil:
		// The command restored the backup itself; still report a safe failure.
		result.State = StateFailedSafe
		result.Error = "sshd_config validation failed; original configuration restored"
		emit(StepDisablePassword, StateFailedSafe, result.Error)
		return result, errors.New(result.Error)
	}
	emit(StepDisablePassword, StateSuccess, "")

	// Step 5: reload the daemon.
	emit(StepReloadSshd, StateInProgress, "")
	if out, _, _, err := client.Exec(ctx, buildReloadSshdCommand(), keySetupStepTimeout); err != nil {
		result.State = StateNeedsRollback
		result.Error = fmt.Sprintf("reload failed: %v: %s", err, strings.TrimSpace(string(out)))
		emit(StepReloadSshd, StateFailedSafe, result.Error)
		rolled, rollbackErr := d.rollback(ctx, client)
		if rolled {
			result.State = StateRolledBack
			result.Message = "Password authentication change was rolled back."
		} else if rollbackErr != nil {
			result.Message = "Password authentication may be disabled and rollback failed: " + rollbackErr.Error()
		}
		return result, errors.New(result.Error)
	}
	emit(StepReloadSshd, StateSuccess, "")

	// Step 6: final check with a fresh key-only connection.
	emit(StepFinalCheck, StateInProgress, "")
	if err := d.verifyKeyAuth(ctx, host, known, privPath); err != nil {
		result.State = StateNeedsRollback
		result.Error = "final verification failed: " + err.Error()
		emit(StepFinalCheck, StateFailedSafe, result.Error)
		if rolled, _ := d.rollback(ctx, client); rolled {
			result.State = StateRolledBack
		}
		return result, errors.New(result.Error)
	}
	result.State = StateSuccess
	result.PasswordAuthDisabled = true
	result.Message = "Key installed, verified and password authentication disabled."
	emit(StepFinalCheck, StateSuccess, "")
	return result, nil
}

func (d *Dialer) withStepTimeout(ctx context.Context, fn func(context.Context) (*Client, error)) (*Client, error) {
	sctx, cancel := context.WithTimeout(ctx, keySetupStepTimeout)
	defer cancel()
	type res struct {
		client *Client
		err    error
	}
	ch := make(chan res, 1)
	go func() {
		c, err := fn(sctx)
		ch <- res{c, err}
	}()
	select {
	case r := <-ch:
		return r.client, r.err
	case <-sctx.Done():
		return nil, errors.New("keysetup: timed out")
	}
}

// verifyKeyAuth connects with only the new key, so a server that still accepts
// the account's password cannot produce a false positive.
func (d *Dialer) verifyKeyAuth(ctx context.Context, host model.Host, known []model.Host, privPath string) error {
	checkHost := host.Clone()
	checkHost.IdentityFiles = []string{privPath}
	checkHost.CertificateFile = ""
	checkHost.IdentitiesOnly = true
	checkHost.ForwardAgent = false

	// A prompts-free dialer means key auth is the only method offered.
	verify := &Dialer{
		Prompts:           Prompts{},
		Cache:             NewAuthCache(),
		Timeout:           keySetupStepTimeout,
		KnownHostsPath:    d.KnownHostsPath,
		AcceptNewHostKeys: true,
	}
	client, err := verify.Dial(ctx, checkHost, known, func(model.Host) (string, bool) { return "", false })
	if err != nil {
		return err
	}
	defer client.Close()
	if _, _, _, err := client.Exec(ctx, "true", keySetupStepTimeout); err != nil {
		return err
	}
	return nil
}

// rollback restores the most recent sshd_config backup.
func (d *Dialer) rollback(ctx context.Context, client *Client) (bool, error) {
	out, _, _, err := client.Exec(ctx, buildRollbackCommand(), keySetupStepTimeout)
	text := strings.TrimSpace(string(out))
	if strings.Contains(text, "SEXTANT_NO_BACKUP") {
		return false, errors.New("no sshd_config backup found; password authentication may still be disabled")
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func writeFileAtomicMode(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}
