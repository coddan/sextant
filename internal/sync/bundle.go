package sync

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/coddan/sextant/internal/config"
	"github.com/coddan/sextant/internal/model"
)

// ErrLocked is returned when a snapshot is requested while the secret store is
// locked, because exporting without credentials would silently drop them.
var ErrLocked = errors.New("sync: unlock the secret store before exporting")

// Snapshot builds a bundle from the live store, including decrypted secrets.
func Snapshot(store *config.Store) (Bundle, error) {
	if store == nil {
		return Bundle{}, errors.New("sync: no store")
	}
	secrets := map[string]string{}
	if store.Secrets != nil {
		if store.Secrets.Locked() {
			return Bundle{}, ErrLocked
		}
		for _, id := range store.Secrets.IDs() {
			if v, ok := store.Secrets.Get(id); ok {
				secrets[id] = v
			}
		}
	}
	return Bundle{
		Version:    BundleVersion,
		ExportedAt: time.Now().UTC(),
		App:        "sextant",
		Settings:   store.Settings,
		Hosts:      append([]model.Host(nil), store.Hosts...),
		Groups:     append([]model.Group(nil), store.Groups...),
		Snippets:   append([]model.Snippet(nil), store.Snippets...),
		Secrets:    secrets,
	}, nil
}

// Apply replaces the store's configuration with the bundle and saves it. The
// caller is expected to have confirmed with the user, since this overwrites the
// current setup.
func Apply(bundle Bundle, store *config.Store) error {
	if store == nil {
		return errors.New("sync: no store")
	}
	store.Settings = bundle.Settings
	store.Settings.Normalize()
	store.Hosts = bundle.Hosts
	store.Groups = bundle.Groups
	store.Snippets = bundle.Snippets

	if store.Secrets != nil {
		if store.Secrets.Locked() {
			return ErrLocked
		}
		// Replace the credential set entirely; anything not in the bundle
		// belongs to hosts that no longer exist.
		for _, id := range store.Secrets.IDs() {
			store.Secrets.Delete(id)
		}
		for id, value := range bundle.Secrets {
			store.Secrets.Set(id, value)
		}
	}
	if err := store.Save(); err != nil {
		return fmt.Errorf("sync: save imported configuration: %w", err)
	}
	return nil
}

// Push exports the store and uploads the sealed bundle to the provider.
func Push(ctx context.Context, provider Provider, store *config.Store, passphrase string) error {
	bundle, err := Snapshot(store)
	if err != nil {
		return err
	}
	data, err := Seal(bundle, passphrase)
	if err != nil {
		return err
	}
	return provider.Push(ctx, data)
}

// Pull downloads the bundle from the provider, decrypts it and applies it.
func Pull(ctx context.Context, provider Provider, store *config.Store, passphrase string) error {
	data, err := provider.Fetch(ctx)
	if err != nil {
		return err
	}
	if data == nil {
		return fmt.Errorf("sync: no bundle found at %s", provider.Describe())
	}
	bundle, err := Open(data, passphrase)
	if err != nil {
		return err
	}
	return Apply(bundle, store)
}
