# Contributing to Sextant

Thank you for your interest in contributing! This document describes the
development workflow, coding conventions, and review process.

---

## Table of contents

1. [Setting up the development environment](#1-setting-up-the-development-environment)
2. [Running the project](#2-running-the-project)
3. [Running tests](#3-running-tests)
4. [Code conventions](#4-code-conventions)
5. [Commit style](#5-commit-style)
6. [Opening a pull request](#6-opening-a-pull-request)
7. [Reporting bugs](#7-reporting-bugs)

---

## 1. Setting up the development environment

**Prerequisites:**

| Tool | Minimum version | Notes |
|------|-----------------|-------|
| Go | 1.25 | the whole project, both clients |
| Git | any recent | |
| C toolchain | — | desktop client only (`sextant-gui`, Fyne needs CGO) |
| OpenGL / X11 headers | — | desktop client only |

**Clone and build:**

```bash
git clone https://github.com/coddan/sextant.git
cd sextant
go build ./...
```

The first build downloads the module cache and may take a few minutes.
Subsequent builds are incremental.

**Repository layout:**

| Path | Contents |
|------|----------|
| `cmd/sextant` | Terminal client entry point (binary `sextant`, pure Go) |
| `cmd/sextant-gui` | Desktop client entry point (binary `sextant-gui`, Fyne) |
| `internal/app` | Shared runtime: store, dialer, forwards, bookkeeping |
| `internal/sshclient` | SSH, SFTP, forwarding, metrics, key setup, snippets |
| `internal/sshconfig` | `~/.ssh/config` parser (globs, `Include`, wildcard defaults) |
| `internal/config` | JSON store, encrypted secrets, portable paths |
| `internal/model` | Host, group, snippet, metrics and notification types |
| `internal/tui` | Bubble Tea terminal client |
| `internal/gui` | Fyne desktop client |
| `internal/vterm` | VT emulator (scrollback, search, OSC 52) |
| `internal/i18n` | Typed translation keys, `en` + `zh-CN` |
| `internal/sync` + `internal/synccmd` | Encrypted config bundle (local + WebDAV) |
| `internal/putty` | PuTTY session and PPK import |

The terminal client builds with `CGO_ENABLED=0`; the desktop client requires
CGO. CI runs both: a plain build/vet/test job with the GUI dependencies
installed, and a CGO-off job that builds and tests everything except
`internal/gui`.

**Recommended tools:**

```bash
go install golang.org/x/tools/gopls@latest   # editor support
```

---

## 2. Running the project

```bash
# Terminal client
go run ./cmd/sextant

# Desktop client (needs CGO and the GL/X11 headers)
go run ./cmd/sextant-gui

# Build the release binaries
go build -o sextant ./cmd/sextant
go build -o sextant-gui ./cmd/sextant-gui
```

Both clients read and write the same configuration directory, so a host added
in one shows up in the other. Set `SEXTANT_PORTABLE=1` (or drop a
`sextant.portable` file next to the binary) to keep that directory beside the
executable during development.

---

## 3. Running tests

```bash
go test ./...           # everything
go test ./internal/...  # the engine, without the GUI
go test -run TestFoo ./internal/sshconfig
go test -race ./...     # before a PR that touches concurrency
```

Checks that CI enforces:

```bash
gofmt -l .              # must print nothing
go vet ./...
go mod tidy             # must leave go.mod/go.sum unchanged
```

`internal/i18n` has a test that fails if any language misses a key, so add the
key to every language map when you introduce one.

---

## 4. Code conventions

These conventions are enforced in code review and by CI.

### Architecture

- **The engine stays UI-free.** Code under `internal/` that is not
  `internal/tui` or `internal/gui` must not import either frontend.
- **Both frontends share one runtime.** Add behaviour to `internal/app` and
  consume it from both clients rather than duplicating logic.
- **Never block the UI on network I/O.** SSH work runs in goroutines and
  reports back through the Bubble Tea `Update` loop (terminal client) or
  `fyne.Do` (desktop client).
- **`vterm.Terminal` has no internal mutex.** The terminal client serialises
  writes through its `Update` loop; the desktop client serialises them on the
  UI goroutine. Keep it that way instead of adding a lock.

### Error handling

- Wrap errors with `fmt.Errorf("...: %w", err)` and return them; do not panic
  on user input or network failures.
- Every SSH error (timeout, auth failure, host key mismatch) must be shown to
  the user through the status bar, a notification, or a dialog.

### UI

- Destructive operations (delete host, delete file) require a confirmation.
- Every screen or panel handles its own input; keep the dispatch in one place.

### Cross-platform

- Use `os.UserHomeDir` and the helpers in `internal/config/paths.go` for user
  paths — never hardcode `~`.
- Platform-specific code goes in `_unix.go` / `_windows.go` files behind one
  interface.
- Parse remote command output with `strings.Split` on the line separator so
  both `\n` and `\r\n` work.

### Dependencies

Before adding a module, check whether the feature can be implemented in a few
dozen lines of standard-library Go. Every dependency increases build time and
binary size.

---

## 5. Commit style

Sextant uses [Conventional Commits](https://www.conventionalcommits.org/).

```
<type>(<optional scope>): <short summary>

[optional body]

[optional footer]
```

**Types:**

| Type       | When to use |
|------------|-------------|
| `feat`     | New user-visible feature |
| `fix`      | Bug fix |
| `docs`     | Documentation only |
| `refactor` | Code change with no user-visible effect |
| `test`     | Adding or fixing tests |
| `chore`    | Build system, CI, dependency bumps |
| `perf`     | Performance improvement |

**Examples:**

```
feat(themes): add gruvbox colour scheme

fix(ssh): respect connection timeout when host is unreachable

docs: update README with installation instructions

chore: bump bubbletea to v1.3.10
```

---

## 6. Opening a pull request

1. Fork the repository and create a branch:
   ```bash
   git checkout -b feat/my-feature
   ```
2. Make your changes following the conventions above.
3. Run the full test suite and the CI checks:
   ```bash
   gofmt -l . && go vet ./... && go test ./... && go mod tidy
   ```
4. Push and open a PR against the `main` branch.
5. Fill in the PR template (problem, solution, test plan).

**PR checklist:**

- [ ] All tests pass (`go test ./...`)
- [ ] `go vet ./...` and `gofmt -l .` are clean
- [ ] `go mod tidy` leaves `go.mod`/`go.sum` unchanged
- [ ] Relevant tests added (parsers, new features)
- [ ] CHANGELOG entry added
- [ ] README updated if the change is user-visible

---

## 7. Reporting bugs

Please open a GitHub Issue with:

- Sextant version (`sextant version`, or Settings in the desktop client)
- OS and terminal emulator
- Steps to reproduce
- Expected behaviour vs. actual behaviour
- `go test ./...` output if a test fails

For security issues, please **do not** open a public issue. Email the
maintainers directly.
