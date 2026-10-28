# Desktop client notes

The desktop client (`sextant-gui`) is [Fyne](https://fyne.io) 2.8. It shares its
engine with the terminal client, including the VT emulator, so the terminal you
see is the same emulation running in `sextant`.

## Requirements

Fyne needs **CGO**, OpenGL and X11 headers:

```bash
# Debian / Ubuntu
sudo apt install gcc libgl1-mesa-dev xorg-dev
# Fedora
sudo dnf install gcc libXcursor-devel libXrandr-devel libXinerama-devel \
  libXi-devel libXxf86vm-devel mesa-libGL-devel
# Arch
sudo pacman -S base-devel libxcursor libxrandr libxinerama libxi libxxf86vm mesa
```

## Wayland

The window uses GLFW, which talks to X11. On a Wayland session the XWayland
bridge normally handles that. If the window never appears — commonly on
compositors that do not start XWayland — run against X11 explicitly:

```bash
WAYLAND_DISPLAY= sextant-gui
```

The `version` and `sync` subcommands do not touch the display and work in a
headless session:

```bash
sextant-gui version
sextant-gui sync pull --local ~/Dropbox/sextant
```

## Missing `libXxf86vm`

Some distributions ship only the versioned library
(`libXxf86vm.so.1`) with no development symlink, and the linker then fails with
`cannot find -lXxf86vm`. Install the `-devel` package above, or point CGO at a
directory that contains the symlink:

```bash
mkdir -p ~/.local/lib
ln -sf /lib64/libXxf86vm.so.1 ~/.local/lib/libXxf86vm.so
CGO_LDFLAGS="-L$HOME/.local/lib" go build ./cmd/sextant-gui
```

## Dashboard metrics

The **Hosts** tab is a responsive card grid with one card per host showing
reachability, CPU, memory and disk bars, plus OS, uptime and load when a sample
is available. Background collection runs on the refresh interval from
**Settings → General** and uses stored passwords, stored key passphrases and the
local SSH agent. It never opens credential dialogs, so refreshing a large host
list cannot leave a stack of password prompts on screen.

A card marked **Limited** is reachable but its metrics could not be collected
without interaction, usually because the host needs a password or its host key
has not been trusted yet. Use the **Metrics** button on that card to authenticate
once; later background samples can then reuse the stored credentials. After an
interactive-auth failure, background retries back off for ten minutes; the
**Metrics** button always tries immediately. Hosts configured for TCP monitoring
show reachability only.

## Multiple terminals

Every connection opened from the dashboard gets its own tab under **Terminal**.
Sessions stay alive when you switch back to the dashboard or open another host,
and each tab has its own **Close** button. Ending one session leaves the others
running.

## Windows

Build with the GUI subsystem to avoid a console window:

```powershell
go build -ldflags "-H=windowsgui" -o sextant-gui.exe ./cmd/sextant-gui
```

Without `-H=windowsgui`, Windows attaches a `cmd` window to the process and
closing that window terminates the app. The published Windows builds already
set this flag.

## Terminal fonts

The terminal renders with whatever monospace font the theme resolves. Set
**Font family** in Settings to any installed font — a Nerd Font gives you
Powerline and icon glyphs. The value may be a family name (resolved through
`fc-match`, then a scan of the usual font directories) or a direct path to a
`.ttf`/`.otf` file. Only the monospace slot is overridden; the rest of the
interface keeps the selected theme. **Font size** is applied to new sessions.

## Clipboard

Text copied by a remote program through **OSC 52** is written to the system
clipboard, so agents like Claude Code and Codex, which copy through the
terminal, work without mouse selection. `Ctrl+Shift+C` copies the whole buffer
and `Ctrl+Shift+V` pastes.
