#!/usr/bin/env bash
#
# xpu-smi-tui uninstaller script
#
set -euo pipefail

APP_NAME="xpu-smi-tui"

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

echo -e "${CYAN}${BOLD}"
echo "  🗑 XPU-SMI TUI UNINSTALLER"
echo -e "${RESET}"

removed=0

# Check and remove binaries
for bin_dir in "/usr/local/bin" "${HOME}/.local/bin"; do
    for bin in "${APP_NAME}" xpu-exporter; do
        if [ -f "${bin_dir}/${bin}" ]; then
            if [ ! -w "${bin_dir}/${bin}" ] && [ "$(id -u)" -ne 0 ]; then
                warn "Cannot remove ${bin_dir}/${bin}: Permission denied (run with sudo to remove system install)"
            else
                rm -f "${bin_dir}/${bin}"
                success "Removed binary: ${bin_dir}/${bin}"
                removed=$((removed + 1))
            fi
        fi
    done
done

# Check and remove shell completions
comp_files=(
    "/usr/share/bash-completion/completions/${APP_NAME}"
    "${HOME}/.local/share/bash-completion/completions/${APP_NAME}"
    "/usr/local/share/zsh/site-functions/_${APP_NAME}"
    "${HOME}/.local/share/zsh/site-functions/_${APP_NAME}"
    "/usr/share/fish/vendor_completions.d/${APP_NAME}.fish"
    "${HOME}/.config/fish/completions/${APP_NAME}.fish"
)

for comp in "${comp_files[@]}"; do
    if [ -f "${comp}" ]; then
        if [ ! -w "${comp}" ] && [ "$(id -u)" -ne 0 ]; then
            warn "Cannot remove ${comp}: Permission denied"
        else
            rm -f "${comp}"
            success "Removed completion: ${comp}"
            removed=$((removed + 1))
        fi
    fi
done

if [ "$removed" -eq 0 ]; then
    warn "No installed ${APP_NAME} files found."
else
    echo ""
    success "${APP_NAME} uninstalled successfully."
fi
