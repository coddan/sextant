<div align="center">

# Sextant

### Every server you manage, in one window. Dashboard, terminal, SFTP, snippets.

[![Downloads](https://img.shields.io/github/downloads/coddan/sextant/total?label=total%20installs&color=2ea44f)](https://github.com/coddan/sextant/releases)
[![Latest release](https://img.shields.io/github/v/release/coddan/sextant?label=latest)](https://github.com/coddan/sextant/releases/latest)
[![License](https://img.shields.io/badge/license-Apache--2.0-blue.svg)](LICENSE)
[![Build](https://img.shields.io/github/actions/workflow/status/coddan/sextant/ci.yml?branch=main)](https://github.com/coddan/sextant/actions)

**[Install](#install)** •
**[Features](#features)** •
**[SSH keys](#ssh-key-setup)** •
**[Configuration](#configuration)** •
**[The terminal client](#the-terminal-client)** •
**[Building](#building-from-source)**

</div>

---

Sextant is written in Go. Two clients share one engine:

| Binary | Client | Toolchain |
|---|---|---|
| `sextant-gui` | Desktop app (Fyne) | CGO + OpenGL/X11 |
| `sextant` | Terminal app (Bubble Tea) | pure Go, `CGO_ENABLED=0` |

Both read and write the same JSON configuration, so a host added in the
terminal shows up in the desktop app.

## Install

```bash
curl -fsSL https://raw.githubusercontent.com/coddan/sextant/main/install.sh | sh
```

The default is the desktop app; `sh -s -- --tui` installs `sextant`,
`sh -s -- --both` installs both. Prefer clicking or a package manager?

| Platform | Command |
|---|---|
| Windows | `winget install coddan.Sextant` |
| Any (Go 1.25+) | `go install github.com/coddan/sextant/cmd/sextant@latest` (terminal) |
| Linux, macOS | downloads from [Releases](https://github.com/coddan/sextant/releases/latest) |

ARM64 Linux and Termux have no desktop build (Fyne needs CGO and a native
toolchain), so the installer falls back to the terminal client there.

## Features

### Live dashboard
Every host in one list with CPU, RAM and disk, uptime, distro and top
processes. Filter as you type, show only the hosts that answer (`o`), hide the
ones you do not want to see. Windows hosts report the same metrics as Linux
ones, including a GPU section when the driver exposes one.

### Real terminals
A full PTY per host with a built-in VT emulator: 256-colour and true-colour
output, scrollback with search (`Ctrl+F`), and a faithful cursor. Remote
programs that copy through OSC 52 land in your local clipboard, so copying from
an agent like Claude Code or Codex works.

### SFTP
Browse remote directories, upload, download, rename, delete trees, create
folders and edit a file in place — the terminal client hands it to `$EDITOR`
and uploads the changes, the desktop client opens an in-app editor.
Disconnecting SFTP leaves the SSH session alone.

### Snippets
Save the commands you run every week and send one to every host currently
visible, with the hosts in parallel and the output streamed live. A Stop button
ends a long-running `journalctl -f` / `tail -f` cleanly.

### Key setup in one click
Pick a host that still uses a password, hit **Set up SSH key**: Sextant
generates an Ed25519 key, appends it to `authorized_keys`, proves the new key
works on a fresh connection and only then disables password login. `sshd_config`
is backed up first and restored if any step fails. Your private key never
leaves your machine.

Keys with a passphrase work too. A key already loaded in `ssh-agent` is used
without asking, `IdentityFiles` with matching `-cert.pub` certificates are
offered, and the host **Password** field is the login password, not a key
passphrase.

### Port forwarding
`-L` rules and dynamic SOCKS5 proxies per host, started automatically when the
host says so, with live status. Forwarding over a jump chain works, and
`auth-agent@openssh.com` forwarding is supported so the remote side can use
your local agent.

### Config you already have
Your `~/.ssh/config` is read, never written. `Include` is honoured, wildcard
blocks act as defaults for the literal `Host` blocks that match. PuTTY
sessions can be imported on Windows: PPK v2/v3 keys are converted to OpenSSH
format on the way in.

### Languages and notifications
English and Simplified Chinese ship in the binary, with no external i18n
dependency; every key is checked to exist in every language at build time.
Notifications dismiss themselves after a configurable timeout, and errors can
be made sticky.

## SSH key setup

The flow is the same in both clients and lives in
[`internal/sshclient/keysetup.go`](internal/sshclient/keysetup.go). Read it
before you point it at production — that is the point of shipping it open
source.

## Configuration

Everything is JSON, in a single directory:

```
~/.config/sextant/                       (Linux)
~/Library/Application Support/sextant/   (macOS)
%APPDATA%\sextant\                       (Windows)
  settings.json   interface, terminal, notifications
  hosts.json      hosts and groups
  snippets.json   saved commands
  secrets.json    encrypted credentials
  secrets.key     key file (mode 0600)
  state.json      session state
  known_hosts     host keys learned on first use
```

Secrets are encrypted with AES-256-GCM. By default the key lives in
`secrets.key` (mode `0600`); you can switch to an Argon2id master password that
is asked once per run and never stored. Hosts only ever reference
secret IDs.

Set `SEXTANT_PORTABLE=1`, or drop a file named `sextant.portable` next to the
binary, to keep the whole directory beside the executable instead.

Details: [doc/configuration.md](doc/configuration.md) and
[doc/sync.md](doc/sync.md).

## The terminal client

```bash
go install github.com/coddan/sextant/cmd/sextant@latest
sextant
```

Press `a` to add a host, `/` to filter, `m` to sample metrics, `s` for
snippets, `f` for SFTP, `K` for key setup, `?` for help.

## Building from source

Go 1.25 or newer.

```bash
# terminal client — no C toolchain needed
go build -o sextant ./cmd/sextant

# desktop client — Fyne needs CGO and the GL/X11 headers
sudo apt install gcc libgl1-mesa-dev xorg-dev   # Debian/Ubuntu
go build -o sextant-gui ./cmd/sextant-gui
```

If the linker cannot find `libXxf86vm` on a system where it exists under a
versioned name, point CGO at a directory containing the unversioned symlink:

```bash
ln -s /lib64/libXxf86vm.so.1 /some/dir/libXxf86vm.so
CGO_LDFLAGS="-L/some/dir" go build ./cmd/sextant-gui
```

Tests and checks:

```bash
go test ./...
go vet ./...
gofmt -l .
```

### Wayland

The desktop client uses GLFW. If the window does not appear under Wayland,
run it against X11:

```bash
WAYLAND_DISPLAY= sextant-gui
```

See [doc/gui.md](doc/gui.md) for more.

## Out of scope

Native iOS and Android apps are not planned. Configuration sync **is**
implemented — an encrypted bundle that moves settings, hosts, groups, snippets
and secrets between machines through a local folder or WebDAV. See
[doc/sync.md](doc/sync.md).

## Repository layout

```
cmd/sextant          terminal client entry point
cmd/sextant-gui      desktop client entry point
internal/app         shared runtime (store, dialer, forwards, bookkeeping)
internal/sshclient   SSH, SFTP, forwarding, metrics, key setup, snippets
internal/vterm       VT emulator (scrollback, search, OSC 52)
internal/config      JSON store, encrypted secrets, portable paths
internal/tui         Bubble Tea terminal client
internal/gui         Fyne desktop client
internal/i18n        typed translation keys, en + zh-CN
internal/sync        encrypted config bundle (local + WebDAV)
internal/putty       PuTTY session and PPK import
```

## License

Apache 2.0. See [LICENSE](LICENSE).

<div align="center">

### ⭐ Star the repo if Sextant saved you a terminal tab

[Report a bug](https://github.com/coddan/sextant/issues) •
[Request a feature](https://github.com/coddan/sextant/issues) •
[Discussions](https://github.com/coddan/sextant/discussions)

</div>
