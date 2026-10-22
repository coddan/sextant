# Configuration

Sextant keeps everything in JSON files inside one directory. There is no
database, no daemon and no account.

| Platform | Directory |
|---|---|
| Linux | `~/.config/sextant/` |
| macOS | `~/Library/Application Support/sextant/` |
| Windows | `%APPDATA%\sextant\` |

The same directory is used by the terminal client (`sextant`) and the desktop
client (`sextant-gui`), so a host added in one appears in the other.

## Files

| File | Contents |
|---|---|
| `settings.json` | Interface, terminal, notification, security and sync settings |
| `hosts.json` | Saved hosts and groups |
| `snippets.json` | Saved commands |
| `secrets.json` | Encrypted credentials, keyed by secret ID |
| `secrets.key` | Random key file for `secrets.json` (mode `0600`) |
| `state.json` | Session state (last tab, window geometry) |
| `known_hosts` | Host keys learned on first use, in OpenSSH format |

`hosts.json` never contains a password: the host references a secret ID, and
the value lives encrypted in `secrets.json`.

## Portable mode

To keep the whole directory beside the executable — on a USB stick, in a
portable app folder, or on a machine where you cannot write to `$HOME`:

```bash
SEXTANT_PORTABLE=1 sextant
```

Or drop a marker file next to the binary, which is friendlier when you cannot
set environment variables on the launcher:

```
sextant.portable      # or simply: portable
```

The data then lives in a `sextant-data` directory next to the executable.

## Secrets

Credentials are encrypted with **AES-256-GCM**. Two unlock modes:

* **Key file (default).** A random 32-byte key is generated on first use and
  stored in `secrets.key` with mode `0600`. Nothing to type, but anyone who can
  read your home directory can read it.
* **Master password.** Turn it on in settings. The key is derived
  from your password with **Argon2id** and is never written to disk; you type it
  once per run (minimum 8 characters).

Switching modes re-encrypts the store. Host passwords, key passphrases and the
WebDAV password for sync all live here. Passphrases and passwords are held in
memory for the duration of a run and are never written back in plaintext.

## Hosts and authentication

Authentication is tried in a fixed order:

1. `ssh-agent` (including a named pipe on Windows)
2. explicit `IdentityFiles`, with a matching `-cert.pub` certificate
3. the default keys (`~/.ssh/id_ed25519`, `id_ecdsa`, `id_rsa`)
4. the saved password
5. keyboard-interactive

Set `identities_only` to stop after step 2, like `ssh -o IdentitiesOnly=yes`.
This matters when your agent holds many keys and the server refuses too many
offers.

Keys with a passphrase are asked for once per run; the passphrase stays in
memory. Existing `Host` blocks from `~/.ssh/config` can be imported from the
dashboard; `Include` is followed, and literal `Host` blocks override wildcard
defaults. PuTTY sessions can be imported on Windows, converting PPK v2/v3
keys to OpenSSH format.

## settings.json

```jsonc
{
  "version": 1,
  "general": {
    "refresh_interval_seconds": 30,
    "default_shell": "",
    "max_concurrent_connections": 8,
    "confirm_close_tab": true,
    "import_ssh_config": true
  },
  "ui": {
    "theme": "default",          // default | dark | light
    "language": "en",            // en | zh-CN
    "hide_offline": false,
    "sort_by": "name"
  },
  "terminal": {
    "font_family": "",           // any installed font
    "font_size": 14,
    "scrollback_lines": 10000,
    "sudo_autofill": false       // fill sudo's prompt from the host password
  },
  "notifications": {
    "timeout_seconds": 5,
    "error_timeout_seconds": 12,
    "sticky_errors": false       // 0 timeout = never auto-dismiss
  },
  "security": { "master_password": false },
  "sync": { "enabled": false, "url": "", "auto_push": false }
}
```

Unknown keys are ignored and missing keys fall back to the defaults, so an
older file keeps working after an upgrade.

## Custom port forwards

Per host:

* `local_forwards` — `-L` rules, `[bind:]port:host:port`
* `dynamic_forward` — a SOCKS5 port, `[bind:]port`
* `tunnel_autostart` — start them when the host connects

## Sync

An encrypted bundle of settings, hosts, groups, snippets and secrets can move
between machines through a local folder or WebDAV. See
[doc/sync.md](sync.md).
