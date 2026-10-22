# Encrypted configuration sync

Configuration sync moves your setup between machines. It is one file:

```
sextant-sync.bundle
```

Settings, hosts, groups, snippets **and** decrypted secrets are packed into a
single bundle, sealed with **AES-256-GCM**, with the key derived from your
passphrase using **Argon2id**. The bundle is opaque: whoever stores it cannot
read it. There is deliberately **no unencrypted export**.

## Command line

Both clients expose the same subcommand.

```bash
# Push this machine's configuration into a synced folder.
sextant sync push --local ~/Dropbox/sextant

# On the other machine: download and apply it.
sextant sync pull --local ~/Dropbox/sextant
```

WebDAV replaces `--local`:

```bash
sextant sync push --webdav https://cloud.example.com/remote.php/dav/files/me/sextant \
  --user me --password 'app-password'
sextant sync pull --webdav https://cloud.example.com/remote.php/dav/files/me/sextant \
  --user me --password 'app-password'
```

The passphrase is read from `--passphrase`, then `$SEXTANT_SYNC_PASSPHRASE`,
then prompted for on the terminal. For scripted use, prefer the environment
variable so the passphrase does not show up in the process list:

```bash
SEXTANT_SYNC_PASSPHRASE=... sextant sync push --local /mnt/shared/sextant
```

## What a pull does

`pull` **replaces** the local configuration with the bundle: settings, hosts,
groups, snippets and the entire credential set. Hosts that only exist locally
are gone afterwards. Take a copy of the directory first if that matters:

```bash
cp -a ~/.config/sextant ~/.config/sextant.bak
```

A wrong passphrase fails cleanly — nothing is written:

```
sextant: sync: could not decrypt the bundle: wrong passphrase or damaged file
```

The bundle carries a version and a magic header, so a truncated or foreign file
is rejected before the passphrase is even checked.

## Providers

| Provider | Use |
|---|---|
| Local directory | Any folder a file-sync tool already handles: Dropbox, Syncthing, iCloud Drive, a network share, a Git annex |
| WebDAV | Nextcloud, ownCloud, Synology, Fastmail files, a self-hosted server |

Both are a single file, so conflicts are resolved by whoever wins the upload —
this is a "last push wins" sync, not a merge engine. Push when you finish
configuring a machine, pull on the next one.

## Key file mode

Sync exports secrets, so the store must be unlocked. With the default key-file
mode that is always the case. If you enabled a master password, unlock the
store first (open the app and enter the password, or use key-file mode for the
machine that pushes).
