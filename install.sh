#!/bin/sh
# Sextant installation script
# Usage: curl -fsSL https://raw.githubusercontent.com/coddan/sextant/main/install.sh | sh
#
# Installs either the desktop client (sextant-gui, Fyne) or the terminal client
# (sextant), or both, from the latest GitHub release.

set -e

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

REPO="coddan/sextant"

print_info()    { printf "${BLUE}[INFO]${NC} %s\n" "$1"; }
print_success() { printf "${GREEN}[SUCCESS]${NC} %s\n" "$1"; }
print_error()   { printf "${RED}[ERROR]${NC} %s\n" "$1"; }
print_warning() { printf "${YELLOW}[WARNING]${NC} %s\n" "$1"; }

# Detect OS and architecture and build the release target triple.
detect_platform() {
    OS="$(uname -s)"
    ARCH="$(uname -m)"
    IS_TERMUX=0
    GUI_AVAILABLE=1

    if [ -n "${TERMUX_VERSION:-}" ] || \
       { [ -n "${PREFIX:-}" ] && [ -d "${PREFIX}/bin" ] && \
         [ "${PREFIX#*/com.termux/}" != "${PREFIX}" ]; }; then
        IS_TERMUX=1
        PLATFORM="unknown-linux-gnu"
        INSTALL_DIR="$PREFIX/bin"
        GUI_AVAILABLE=0
        print_info "Termux detected"
    else
        case "$OS" in
            Linux*)  PLATFORM="unknown-linux-gnu"; INSTALL_DIR="/usr/local/bin" ;;
            Darwin*) PLATFORM="apple-darwin";      INSTALL_DIR="/usr/local/bin" ;;
            MINGW*|MSYS*|CYGWIN*)
                PLATFORM="pc-windows-gnu"
                INSTALL_DIR="$HOME/bin"
                EXT=".exe"
                print_warning "Windows detected. Manual PATH configuration may be required."
                ;;
            *) print_error "Unsupported OS: $OS"; exit 1 ;;
        esac
    fi

    case "$ARCH" in
        x86_64|amd64) ARCH="x86_64" ;;
        aarch64|arm64) ARCH="aarch64" ;;
        *) print_error "Unsupported architecture: $ARCH"; exit 1 ;;
    esac

    # No desktop build is released for ARM64 Linux: Fyne needs CGO and a native
    # toolchain, and cross-compiling it produces a binary that segfaults. The
    # terminal client covers those machines.
    if [ "$PLATFORM" = "unknown-linux-gnu" ] && [ "$ARCH" = "aarch64" ]; then
        GUI_AVAILABLE=0
    fi

    TARGET="${ARCH}-${PLATFORM}"
    if [ "$PLATFORM" = "pc-windows-gnu" ]; then
        ARCHIVE_EXT="zip"
    else
        ARCHIVE_EXT="tar.gz"
    fi
    print_info "Detected platform: $TARGET"
}

get_latest_release() {
    print_info "Fetching latest release version..."
    TAG_RE='s/.*"tag_name":[[:space:]]*"([^"]+)".*/\1/'
    if command -v curl >/dev/null 2>&1; then
        VERSION=$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" | \
                  tr ',' '\n' | grep '"tag_name":' | sed -E "$TAG_RE")
    elif command -v wget >/dev/null 2>&1; then
        VERSION=$(wget -qO- "https://api.github.com/repos/$REPO/releases/latest" | \
                  tr ',' '\n' | grep '"tag_name":' | sed -E "$TAG_RE")
    else
        print_error "Neither curl nor wget found. Please install one of them."
        exit 1
    fi
    if [ -z "$VERSION" ]; then
        print_error "Failed to fetch latest release version"
        exit 1
    fi
    print_info "Latest version: $VERSION"
}

# fetch <url> <dest>
fetch() {
    if command -v curl >/dev/null 2>&1; then
        curl -fsSL "$1" -o "$2"
    else
        wget -qO "$2" "$1"
    fi
}

# verify_checksum <archive-name>; skipped when no tool is present.
verify_checksum() {
    if command -v sha256sum >/dev/null 2>&1; then
        SHA_CHECK="sha256sum -c -"
    elif command -v shasum >/dev/null 2>&1; then
        SHA_CHECK="shasum -a 256 -c -"
    else
        print_warning "No sha256 tool found — skipping verification"
        return 0
    fi
    if fetch "https://github.com/$REPO/releases/download/$VERSION/SHA256SUMS" "$TMP_DIR/SHA256SUMS" 2>/dev/null; then
        (cd "$TMP_DIR" && grep "$1" SHA256SUMS | $SHA_CHECK) || {
            print_error "Checksum verification failed for $1"
            exit 1
        }
        print_success "Checksum verified"
    else
        print_warning "Could not download SHA256SUMS — skipping verification"
    fi
}

# download_binary <name>; extracts the binary into $TMP_DIR/$name$EXT
download_binary() {
    _name="$1"
    _archive="${_name}-${TARGET}.${ARCHIVE_EXT}"
    _url="https://github.com/$REPO/releases/download/$VERSION/${_archive}"
    print_info "Downloading $_archive..."
    if ! fetch "$_url" "$TMP_DIR/$_archive"; then
        print_error "Download failed: $_archive"
        return 1
    fi
    verify_checksum "$_archive"
    _extract="$TMP_DIR/extract"
    rm -rf "$_extract"
    mkdir -p "$_extract"
    if [ "$ARCHIVE_EXT" = "zip" ]; then
        unzip -q "$TMP_DIR/$_archive" -d "$_extract"
    else
        tar -xzf "$TMP_DIR/$_archive" -C "$_extract"
    fi
    _found="$(find "$_extract" -name "${_name}${EXT}" -type f | head -n 1)"
    [ -n "$_found" ] || { print_error "Binary $_name not found in archive"; return 1; }
    # Archives normally contain the binary at their root. Move it out of the
    # extraction directory rather than onto itself, which `mv` rejects.
    mv "$_found" "$TMP_DIR/$_name$EXT"
}

# install_binary <name>
install_binary() {
    _name="$1"
    if [ -z "${INSTALL_DIR:-}" ]; then
        INSTALL_DIR="$HOME/.local/bin"
    fi
    [ -d "$INSTALL_DIR" ] || mkdir -p "$INSTALL_DIR"
    if [ -w "$INSTALL_DIR" ] || [ "$IS_TERMUX" = "1" ]; then
        mv "$TMP_DIR/$_name$EXT" "$INSTALL_DIR/$_name$EXT"
        chmod +x "$INSTALL_DIR/$_name$EXT"
    else
        print_warning "Installing to $INSTALL_DIR requires sudo"
        sudo mv "$TMP_DIR/$_name$EXT" "$INSTALL_DIR/$_name$EXT"
        sudo chmod +x "$INSTALL_DIR/$_name$EXT"
    fi
    print_success "Installed $INSTALL_DIR/$_name$EXT"
}

# install_man_page <name>
install_man_page() {
    _name="$1"
    [ "$PLATFORM" = "pc-windows-gnu" ] && return 0
    print_info "Installing man page for $_name..."
    _man_dir="/usr/local/share/man/man1"
    [ "$IS_TERMUX" = "1" ] && _man_dir="$PREFIX/share/man/man1"
    if ! fetch "https://raw.githubusercontent.com/$REPO/$VERSION/doc/${_name}.1" "$TMP_DIR/${_name}.1" 2>/dev/null; then
        print_info "Man page installation skipped (download failed)"
        return 0
    fi
    if mkdir -p "$_man_dir" 2>/dev/null && cp "$TMP_DIR/${_name}.1" "$_man_dir/${_name}.1" 2>/dev/null; then
        print_success "Man page installed. Run 'man $_name'."
    elif [ "$IS_TERMUX" != "1" ] && sudo mkdir -p "$_man_dir" 2>/dev/null && sudo cp "$TMP_DIR/${_name}.1" "$_man_dir/${_name}.1" 2>/dev/null; then
        print_success "Man page installed. Run 'man $_name'."
    else
        print_info "Man page installation skipped (optional)"
    fi
}

verify_installation() {
    _name="$1"
    if command -v "$_name" >/dev/null 2>&1; then
        print_success "$_name $("$_name" version 2>/dev/null || "$_name" --version 2>/dev/null || echo installed)"
    else
        print_warning "$_name installed but not found in PATH"
        print_info "Add to your PATH: export PATH=\"$INSTALL_DIR:\$PATH\""
    fi
}

usage() {
    cat <<EOF
Sextant installer

Usage: install.sh [--gui | --tui | --both]

  --gui    Install the desktop app 'sextant-gui' (default)
  --tui    Install the terminal app 'sextant'
  --both   Install both

Environment: SEXTANT_INSTALL=gui|tui|both has the same effect.
Piped runs (curl | sh) default to --gui; pick another with:
  curl -fsSL .../install.sh | sh -s -- --tui

No desktop build is released for ARM64 Linux or Termux; the installer falls
back to the terminal client there. On Windows the desktop app is available
through winget: winget install coddan.Sextant
EOF
}

select_components() {
    COMPONENTS="${SEXTANT_INSTALL:-}"
    while [ $# -gt 0 ]; do
        case "$1" in
            --tui) COMPONENTS="tui" ;;
            --gui) COMPONENTS="gui" ;;
            --both|--all) COMPONENTS="both" ;;
            -h|--help) usage; exit 0 ;;
            *) print_warning "Ignoring unknown option: $1" ;;
        esac
        shift
    done

    if [ -z "$COMPONENTS" ]; then
        if [ -t 0 ]; then
            printf "Install which component? [1] GUI (desktop)  [2] TUI (sextant)  [3] Both  (default 1): "
            read -r _choice
            case "$_choice" in
                2) COMPONENTS="tui" ;;
                3) COMPONENTS="both" ;;
                *) COMPONENTS="gui" ;;
            esac
        else
            COMPONENTS="gui"
        fi
    fi
}

install_tui() {
    download_binary sextant && install_binary sextant && install_man_page sextant && verify_installation sextant
}

install_gui() {
    download_binary sextant-gui && install_binary sextant-gui && install_man_page sextant-gui && verify_installation sextant-gui
}

main() {
    select_components "$@"

    echo ""
    echo "╔═══════════════════════════════════════╗"
    echo "║   Sextant Installation Script         ║"
    echo "║   SSH Dashboard & Server Manager      ║"
    echo "╚═══════════════════════════════════════╝"
    echo ""

    detect_platform
    get_latest_release

    TMP_DIR="$(mktemp -d)"
    trap 'rm -rf "$TMP_DIR"' EXIT

    case "$COMPONENTS" in
        gui)
            if [ "$GUI_AVAILABLE" != "1" ]; then
                print_info "No desktop build for this platform; installing the terminal app."
                install_tui
            else
                install_gui
            fi
            ;;
        both)
            if [ "$GUI_AVAILABLE" != "1" ]; then
                print_info "No desktop build for this platform; installing the terminal app only."
                install_tui
            else
                install_gui
                install_tui
            fi
            ;;
        *)
            install_tui
            ;;
    esac

    echo ""
    print_success "Installation complete!"
    echo ""
    case "$PLATFORM" in
        apple-darwin)    CONFIG_DIR="~/Library/Application Support/sextant/" ;;
        pc-windows-gnu)  CONFIG_DIR="%APPDATA%\\sextant\\" ;;
        *)               CONFIG_DIR="~/.config/sextant/" ;;
    esac
    print_info "Next steps:"
    print_info "  1. Run 'sextant' (terminal) or launch 'sextant-gui' (desktop)"
    print_info "  2. Configuration lives in $CONFIG_DIR"
    print_info "  3. 'man sextant' / 'man sextant-gui' document the clients (Linux/macOS)"
    print_info "  • On Windows: winget install coddan.Sextant"
    echo ""
}

main "$@"
