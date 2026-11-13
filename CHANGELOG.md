# Changelog

All notable changes to Sextant are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project uses
[Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.2.5] — 2026-11-13

### Added

- **Mouse clicks and drags reach full-screen programs.** When a program enables
  mouse reporting, presses, drags and motion are forwarded to it, as in PuTTY
  and xterm. Hold `Shift` to select text locally instead, or use `Shift` with
  the wheel to scroll Sextant's own scrollback.

### Fixed

- Wheel events sent to full-screen programs used absolute scrollback line
  numbers as screen rows, so they pointed outside the viewport.
- The scrollbar stayed interactive on the alternate screen, where there is no
  scrollback to scroll.

## [0.2.4] — 2026-11-13

### Fixed

- **Scrolling in full-screen programs.** The mouse wheel now reaches programs
  that enable mouse reporting (vim, less) and falls back to cursor keys on the
  alternate screen; `PageUp`/`PageDown` are forwarded rather than consumed by
  local scrollback.

## [0.2.3] — 2026-11-10

### Added

- **Terminal scrollbar.** A draggable scrollbar appears when there is scrollback.

### Fixed

- **Mouse wheel scrolling** no longer snaps back to the bottom whenever output
  arrives; typing or pasting returns to the live screen.

## [0.2.2] — 2026-11-07

### Added

- **Mouse copy and paste.** Like PuTTY and SecureCRT, releasing a drag selection
  copies it to the clipboard and right-click pastes.

## [0.2.1] — 2026-11-02

### Fixed

- **Terminal copy and paste.** Fyne consumed every modified key before the
  terminal saw it, so `Ctrl+C`, `Ctrl+Shift+C/V` and other Ctrl combinations
  never reached the terminal. The terminal now handles shortcuts itself, supports
  drag selection with highlighting, and `Tab`/`Shift+Tab` reach the shell.

## [0.2.0] — 2026-11-01

### Added

- **Multi-terminal workspace with a left rail.** The desktop window now follows
  the OmnySSH/SecureCRT layout: persistent navigation on the left, a live
  **Sessions** list, and one active screen on the right. Every host opened from
  a card or **Sessions → +** gets its own highlighted row and independent close
  button. Sessions keep their shell, scrollback and running programs while you
  switch screens or terminals, and closing one never touches the others.
- **Split terminal panes.** The terminal **Layout** selector offers Single,
  **Split vertical** (side by side) and **Split horizontal** (stacked). Click
  either pane to move keyboard focus without moving the panes; the focused pane
  is highlighted in the Sessions list. Closing a pane keeps the remaining
  session visible and fills the other side with the next open session when one
  is available.
- **Card or list dashboard.** A persisted **Grid / List** selector makes large
  inventories manageable. Grid keeps the metric cards; List uses a virtualised
  row view with status, address, group, live CPU/RAM/disk summary and quick
  connect, SFTP, edit and delete actions.
- **Card dashboard with automatic metrics.** The desktop host list is a
  responsive card grid showing reachability, CPU/RAM/disk severity bars, OS,
  uptime and load. Metrics and reachability refresh in the background on the
  configured interval using stored credentials and the SSH agent; hosts that
  need an interactive login or a new host key are marked *Limited* and can be
  refreshed individually.
- **Metrics are optional.** **Settings → Collect host metrics** turns the
  background SSH metric pass off while keeping cheap reachability probes. When
  disabled, cards and list rows hide metric bars and a per-host **Off**
  monitoring mode skips probes and collection entirely.
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
- **Installer no longer aborts with `mv: ... are the same file`.** Release
  archives contain the binary at their root, so extraction now happens in a
  subdirectory before the binary is moved into the temporary install location.
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

[Unreleased]: https://github.com/coddan/sextant/compare/v0.2.5...HEAD
[0.2.5]: https://github.com/coddan/sextant/compare/v0.2.4...v0.2.5
[0.2.4]: https://github.com/coddan/sextant/compare/v0.2.3...v0.2.4
[0.2.3]: https://github.com/coddan/sextant/compare/v0.2.2...v0.2.3
[0.2.2]: https://github.com/coddan/sextant/compare/v0.2.1...v0.2.2
[0.2.1]: https://github.com/coddan/sextant/compare/v0.2.0...v0.2.1
[0.2.0]: https://github.com/coddan/sextant/compare/v0.1.1...v0.2.0
[0.1.1]: https://github.com/coddan/sextant/releases/tag/v0.1.1
[1.1.5]: https://github.com/coddan/sextant/releases/tag/v1.1.5
