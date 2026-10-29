# Changelog

All notable changes to Sextant are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project uses
[Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- **Multi-terminal workspace with a left rail.** The desktop window now follows
  the OmnySSH/SecureCRT layout: persistent navigation on the left, a live
  **Sessions** list, and one active screen on the right. Every host opened from
  a card or **Sessions → +** gets its own highlighted row and independent close
  button. Sessions keep their shell, scrollback and running programs while you
  switch screens or terminals, and closing one never touches the others.
- **Card dashboard with automatic metrics.** The desktop host list is now a
  responsive card grid showing reachability, CPU/RAM/disk severity bars, OS,
  uptime and load. Metrics and reachability refresh in the background on the
  configured interval using stored credentials and the SSH agent; hosts that
  need an interactive login or a new host key are marked *Limited* and can be
  refreshed individually.
- **Complete dark and light themes.** **Settings → Theme** offers Default
  (follow the desktop), Dark and Light. The choice covers the navigation rail,
  cards, dialogs and terminals, and changes immediately without reconnecting.
  Light mode uses a darker ANSI palette so coloured shell output stays readable
  on a white terminal, including 256-colour output.

### Fixed

- **Unix metrics now populate again.** The section markers were sent as
  `echo ###SEXTANT-…`, which a shell treats as a comment, so CPU, memory, disk
  and the other sections were silently discarded. The markers are now quoted.
- **Windows GUI no longer opens a `cmd` window.** The desktop binary is linked
  with `-H=windowsgui`, so closing a console window cannot terminate the app.
- Terminal sessions survive a theme or language change instead of being
  disconnected when the interface is rebuilt.

## [1.1.5] — 2026-10-21

Sextant is a Go implementation of the SSH dashboard: one engine, two clients.

### Added

- **Two clients, one engine.** `sextant` is a terminal client built on Bubble
  Tea; `sextant-gui` is a desktop client built on Fyne. They share the runtime
  in `internal/app` and the same JSON configuration directory, so a host added
  in one appears in the other.
- **Live dashboard.** CPU, memory, disk, uptime, distro and top processes per
  host, refreshed on a configurable interval. Windows hosts report the same
  metrics as Linux hosts, including a GPU section when the driver exposes one.
  Filter as you type, show only reachable hosts, or hide hosts you do not want
  to see.
- **Full PTY terminals** with a built-in VT emulator: 256-colour and
  true-colour output, scrollback with incremental search, a faithful cursor,
  and OSC 52 clipboard integration so remote programs can copy to the local
  clipboard.
- **SFTP browser.** Upload, download, rename, delete trees, create folders and
  edit a remote file in place — the terminal client hands it to `$EDITOR` and
  uploads the result; the desktop client opens an in-app editor. Disconnecting
  SFTP leaves the SSH session alone.
- **Snippets.** Save commands and send one to every visible host in parallel,
  with streamed output and a Stop button.
- **One-click SSH key setup.** Generate an Ed25519 key, install it, verify it
  over a fresh connection, then disable password login — with a rollback if any
  step fails.
- **Port forwarding.** `-L` rules and dynamic SOCKS5 proxies per host, with
  optional autostart, live status, and agent forwarding
  (`auth-agent@openssh.com`) over jump chains.
- **Encrypted configuration sync.** A single AES-256-GCM bundle (key derived
  with Argon2id) carrying settings, hosts, groups, snippets and secrets between
  machines through a local folder or WebDAV. There is deliberately no
  unencrypted export.
- **Portable mode.** Set `SEXTANT_PORTABLE=1`, or drop a `sextant.portable`
  file next to the binary, to keep the whole configuration directory beside the
  executable.

### Configuration

- Everything lives in one JSON directory: `settings.json`, `hosts.json`,
  `snippets.json`, `secrets.json`, `secrets.key`, `state.json` and
  `known_hosts`. Secrets are encrypted with AES-256-GCM; the key is a `0600`
  key file by default, or an Argon2id master password asked once per run.
- `~/.ssh/config` is read and never written. `Include` is honoured, wildcard
  `Host` blocks act as defaults for the literal blocks that match, and `Match`
  blocks are skipped. PuTTY sessions and PPK v2/v3 keys can be imported on
  Windows.
- Authentication order: `ssh-agent`, explicit identity files (with matching
  `-cert.pub` certificates), default keys, saved password, then
  keyboard-interactive. Passphrases and passwords stay in memory for the run.

### Internationalisation

- English and Simplified Chinese are compiled in, with no external dependency.
  A test fails if any key is missing from any language.

[Unreleased]: https://github.com/coddan/sextant/compare/v1.1.5...HEAD
[1.1.5]: https://github.com/coddan/sextant/releases/tag/v1.1.5
