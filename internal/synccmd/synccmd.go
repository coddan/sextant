// Package synccmd exposes the encrypted configuration sync (sync part)
// as a command shared by both clients:
//
//	sextant sync push --local ~/Dropbox/sextant
//	sextant sync pull --webdav https://example.com/dav --user me
//
// The bundle is one AES-256-GCM file whose key is derived with Argon2id, so it
// can sit in a synced folder or on a WebDAV server without exposing hosts or
// credentials.
package synccmd

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/coddan/sextant/internal/config"
	"github.com/coddan/sextant/internal/sync"
)

// PassphraseEnv is read before prompting, for non-interactive use.
const PassphraseEnv = "SEXTANT_SYNC_PASSPHRASE"

// IsSyncCommand reports whether args start with the sync subcommand.
func IsSyncCommand(args []string) bool {
	return len(args) > 0 && args[0] == "sync"
}

// Run executes the sync subcommand. It returns an error for the caller to
// report; handled is false when args are not a sync invocation.
func Run(ctx context.Context, store *config.Store, args []string, stdout, stderr io.Writer) (handled bool, err error) {
	if !IsSyncCommand(args) {
		return false, nil
	}
	if len(args) < 2 {
		printUsage(stderr)
		return true, errors.New("sync: missing action")
	}
	action := args[1]
	if action != "push" && action != "pull" {
		printUsage(stderr)
		return true, fmt.Errorf("sync: unknown action %q", action)
	}

	fs := flag.NewFlagSet("sync "+action, flag.ContinueOnError)
	fs.SetOutput(stderr)
	local := fs.String("local", "", "directory holding the bundle (can be a synced folder)")
	webdav := fs.String("webdav", "", "WebDAV base URL")
	user := fs.String("user", "", "WebDAV username")
	password := fs.String("password", "", "WebDAV password")
	passphrase := fs.String("passphrase", "", "bundle passphrase (otherwise $"+PassphraseEnv+" or a prompt)")
	if err := fs.Parse(args[2:]); err != nil {
		return true, err
	}

	provider, err := providerFor(*local, *webdav, *user, *password)
	if err != nil {
		return true, err
	}
	phrase, err := resolvePassphrase(*passphrase)
	if err != nil {
		return true, err
	}
	if phrase == "" {
		return true, errors.New("sync: a passphrase is required")
	}

	switch action {
	case "push":
		if err := sync.Push(ctx, provider, store, phrase); err != nil {
			return true, err
		}
		fmt.Fprintf(stdout, "pushed configuration to %s\n", provider.Describe())
	case "pull":
		if err := sync.Pull(ctx, provider, store, phrase); err != nil {
			return true, err
		}
		fmt.Fprintf(stdout, "pulled configuration from %s\n", provider.Describe())
	}
	return true, nil
}

func providerFor(local, webdav, user, password string) (sync.Provider, error) {
	switch {
	case local != "" && webdav != "":
		return nil, errors.New("sync: choose either --local or --webdav")
	case local != "":
		return sync.NewLocalProvider(local), nil
	case webdav != "":
		return sync.NewWebDAVProvider(webdav, user, password)
	default:
		return nil, errors.New("sync: one of --local or --webdav is required")
	}
}

func resolvePassphrase(flagValue string) (string, error) {
	if flagValue != "" {
		return flagValue, nil
	}
	if env := os.Getenv(PassphraseEnv); env != "" {
		return env, nil
	}
	fmt.Fprint(os.Stderr, "Passphrase: ")
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

func printUsage(w io.Writer) {
	fmt.Fprint(w, `Usage:
  sextant sync push --local <dir>            upload the encrypted bundle
  sextant sync push --webdav <url> [--user u] [--password p]
  sextant sync pull --local <dir>            download and apply a bundle
  sextant sync pull --webdav <url> ...

The passphrase comes from --passphrase or $SEXTANT_SYNC_PASSPHRASE, otherwise
it is prompted for. There is no unencrypted export.
`)
}
