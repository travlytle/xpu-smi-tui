#!/usr/bin/env bash
#
# xpu-smi-tui universal installer script
#
set -euo pipefail

APP_NAME="xpu-smi-tui"
VERSION="$(cat "$(dirname "${BASH_SOURCE[0]}")/VERSION")"

# Colors for terminal output
BOLD="\033[1m"
CYAN="\033[36m"
GREEN="\033[32m"
YELLOW="\033[33m"
RED="\033[31m"
RESET="\033[0m"

info() {
    echo -e "${CYAN}==>${RESET} ${BOLD}$1${RESET}"
}

success() {
    echo -e "${GREEN}✓${RESET} ${BOLD}$1${RESET}"
}

warn() {
    echo -e "${YELLOW}⚠${RESET} $1"
}

error() {
    echo -e "${RED}✕ ERROR:${RESET} $1" >&2
}

# Determine default install prefix (system-wide if root, else user-local)
INSTALL_MODE="auto"
TARGET_BIN=""
COMPLETION_DIR=""
# yes / no -- whether to grant the binary CAP_PERFMON (system installs).
PERFMON="yes"

show_help() {
    echo "Usage: ./install.sh [options]"
    echo ""
    echo "Options:"
    echo "  --user         Install to user directory (~/.local/bin, no root required)"
    echo "  --system       Install system-wide (/usr/local/bin, requires sudo)"
    echo "  --no-perfmon   With --system: do not grant the binary CAP_PERFMON (it is what lets"
    echo "                 it read exact A-series VRAM and PCIe throughput without root)"
    echo "  --uninstall    Uninstall xpu-smi-tui and shell completions"
    echo "  -h, --help     Show this help message"
    echo ""
}

# Parse options
while [[ $# -gt 0 ]]; do
    case "$1" in
        --user)
            INSTALL_MODE="user"
            shift
            ;;
        --system)
            INSTALL_MODE="system"
            shift
            ;;
        --no-perfmon)
            PERFMON="no"
            shift
            ;;
        --uninstall)
            if [ -f "$(dirname "$0")/uninstall.sh" ]; then
                exec "$(dirname "$0")/uninstall.sh"
            else
                error "uninstall.sh not found."
                exit 1
            fi
            ;;
        -h|--help)
            show_help
            exit 0
            ;;
        *)
            warn "Unknown option: $1"
            show_help
            exit 1
            ;;
    esac
done

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

echo -e "${CYAN}${BOLD}"
echo "  ⚡ XPU-SMI TUI INSTALLER (v${VERSION})"
echo -e "${RESET}"

# Determine installation paths
if [ "$INSTALL_MODE" = "system" ] || ([ "$INSTALL_MODE" = "auto" ] && [ "$(id -u)" -eq 0 ]); then
    if [ "$(id -u)" -ne 0 ]; then
        error "System-wide install requires root privileges. Please run with sudo: sudo ./install.sh --system"
        exit 1
    fi
    TARGET_BIN="/usr/local/bin"
    BASH_COMP_DIR="/usr/share/bash-completion/completions"
    ZSH_COMP_DIR="/usr/local/share/zsh/site-functions"
    FISH_COMP_DIR="/usr/share/fish/vendor_completions.d"
    info "Installing in SYSTEM-WIDE mode (/usr/local/bin)"
else
    TARGET_BIN="${HOME}/.local/bin"
    BASH_COMP_DIR="${HOME}/.local/share/bash-completion/completions"
    ZSH_COMP_DIR="${HOME}/.local/share/zsh/site-functions"
    FISH_COMP_DIR="${HOME}/.config/fish/completions"
    info "Installing in USER mode (~/.local/bin, no root needed)"
fi

# Ensure target directory exists
mkdir -p "${TARGET_BIN}"

# Build binary if necessary
BINARY_SOURCE="${SCRIPT_DIR}/${APP_NAME}"
if [ ! -f "${BINARY_SOURCE}" ] || [ -f "${SCRIPT_DIR}/main.go" ]; then
    if command -v go >/dev/null 2>&1; then
        info "Compiling ${APP_NAME} binary with Go..."
        (cd "${SCRIPT_DIR}" && go build -ldflags="-s -w -X main.AppVersion=${VERSION}" -o "${APP_NAME}" .)
        BINARY_SOURCE="${SCRIPT_DIR}/${APP_NAME}"
    elif [ ! -f "${BINARY_SOURCE}" ]; then
        error "No pre-compiled binary found and 'go' is not in PATH."
        echo "Please install Go or download a pre-built release binary."
        exit 1
    fi
fi

# Install binary
info "Installing binary to ${TARGET_BIN}/${APP_NAME}..."
install -m 755 "${BINARY_SOURCE}" "${TARGET_BIN}/${APP_NAME}"
success "Binary installed to ${TARGET_BIN}/${APP_NAME}"

if command -v go >/dev/null 2>&1 && [ -d "${SCRIPT_DIR}/cmd/xpu-exporter" ]; then
    (cd "${SCRIPT_DIR}" && go build -ldflags="-s -w" -o xpu-exporter ./cmd/xpu-exporter)
fi
if [ -f "${SCRIPT_DIR}/xpu-exporter" ]; then
    install -m 755 "${SCRIPT_DIR}/xpu-exporter" "${TARGET_BIN}/xpu-exporter"
    success "Exporter installed to ${TARGET_BIN}/xpu-exporter"
fi

# Install Shell Completions
if [ -d "${SCRIPT_DIR}/completions" ]; then
    info "Installing shell completions..."

    # Bash completion
    if [ -f "${SCRIPT_DIR}/completions/xpu-smi-tui.bash" ]; then
        mkdir -p "${BASH_COMP_DIR}"
        cp "${SCRIPT_DIR}/completions/xpu-smi-tui.bash" "${BASH_COMP_DIR}/${APP_NAME}"
        success "Bash completion installed to ${BASH_COMP_DIR}/${APP_NAME}"
    fi

    # Zsh completion
    if [ -f "${SCRIPT_DIR}/completions/_xpu-smi-tui" ]; then
        mkdir -p "${ZSH_COMP_DIR}"
        cp "${SCRIPT_DIR}/completions/_xpu-smi-tui" "${ZSH_COMP_DIR}/_${APP_NAME}"
        success "Zsh completion installed to ${ZSH_COMP_DIR}/_${APP_NAME}"
    fi

    # Fish completion
    if [ -f "${SCRIPT_DIR}/completions/xpu-smi-tui.fish" ]; then
        mkdir -p "${FISH_COMP_DIR}"
        cp "${SCRIPT_DIR}/completions/xpu-smi-tui.fish" "${FISH_COMP_DIR}/${APP_NAME}.fish"
        success "Fish completion installed to ${FISH_COMP_DIR}/${APP_NAME}.fish"
    fi
fi

# CAP_PERFMON on the binary: lets it read the exact VRAM in use on Arc Pro
# A-series cards and PCIe throughput from the CPU's counters, unprivileged. It
# is a property of the file, so re-running this after replacing the binary
# (which is what this script just did) is what keeps it.
if [ "${PERFMON}" = "yes" ]; then
    if [ "$(id -u)" -ne 0 ]; then
        echo "Tip: for exact A-series VRAM and PCIe throughput without root, run once:"
        echo "     sudo setcap cap_perfmon+ep ${TARGET_BIN}/${APP_NAME}   (lost when the binary is replaced)"
    elif command -v setcap >/dev/null 2>&1; then
        setcap cap_perfmon+ep "${TARGET_BIN}/${APP_NAME}" \
            && success "Granted CAP_PERFMON to ${TARGET_BIN}/${APP_NAME} (undo: sudo setcap -r ${TARGET_BIN}/${APP_NAME})" \
            || warn "Could not set CAP_PERFMON on ${TARGET_BIN}/${APP_NAME}"
    else
        warn "setcap not found (install libcap2-bin): CAP_PERFMON not granted"
    fi
fi

# Check if TARGET_BIN is in user's PATH
case ":$PATH:" in
    *":${TARGET_BIN}:"*) ;;
    *)
        echo ""
        warn "${TARGET_BIN} is not in your current PATH!"
        echo "To use '${APP_NAME}' from any terminal, add this to your shell profile (~/.bashrc or ~/.zshrc):"
        echo -e "  ${BOLD}export PATH=\"${TARGET_BIN}:\$PATH\"${RESET}"
        echo ""
        ;;
esac

echo ""
success "Installation complete!"
echo -e "Run ${CYAN}${BOLD}${APP_NAME}${RESET} to start monitoring Intel GPUs."
echo -e "Run ${CYAN}${BOLD}${APP_NAME} --help${RESET} for options or ${CYAN}${BOLD}${APP_NAME} --demo${RESET} for simulation mode."
echo ""
