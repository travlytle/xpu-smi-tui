#!/usr/bin/env bash
#
# Build Debian/Ubuntu package (.deb) for xpu-smi-tui
#
set -euo pipefail

APP_NAME="xpu-smi-tui"
VERSION="$(cat "$(dirname "${BASH_SOURCE[0]}")/../VERSION")"
ARCH="amd64"
DEB_NAME="${APP_NAME}_${VERSION}_${ARCH}.deb"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BUILD_ROOT="${SCRIPT_DIR}/build/deb"

echo "==> Building Debian package ${DEB_NAME}..."

# Clean previous build
rm -rf "${BUILD_ROOT}"
mkdir -p "${BUILD_ROOT}/DEBIAN"
mkdir -p "${BUILD_ROOT}/usr/bin"
mkdir -p "${BUILD_ROOT}/usr/share/bash-completion/completions"
mkdir -p "${BUILD_ROOT}/usr/share/zsh/vendor-completions"
mkdir -p "${BUILD_ROOT}/usr/share/fish/vendor_completions.d"
mkdir -p "${BUILD_ROOT}/usr/share/doc/${APP_NAME}"

# Compile binary
echo "==> Compiling optimized binary..."
(cd "${SCRIPT_DIR}" && go build -ldflags="-s -w -X main.AppVersion=${VERSION}" -o "${BUILD_ROOT}/usr/bin/${APP_NAME}" .)
chmod 755 "${BUILD_ROOT}/usr/bin/${APP_NAME}"

# Install completions
if [ -f "${SCRIPT_DIR}/completions/xpu-smi-tui.bash" ]; then
    cp "${SCRIPT_DIR}/completions/xpu-smi-tui.bash" "${BUILD_ROOT}/usr/share/bash-completion/completions/${APP_NAME}"
fi
if [ -f "${SCRIPT_DIR}/completions/_xpu-smi-tui" ]; then
    cp "${SCRIPT_DIR}/completions/_xpu-smi-tui" "${BUILD_ROOT}/usr/share/zsh/vendor-completions/_${APP_NAME}"
fi
if [ -f "${SCRIPT_DIR}/completions/xpu-smi-tui.fish" ]; then
    cp "${SCRIPT_DIR}/completions/xpu-smi-tui.fish" "${BUILD_ROOT}/usr/share/fish/vendor_completions.d/${APP_NAME}.fish"
fi

# Docs
if [ -f "${SCRIPT_DIR}/README.md" ]; then
    cp "${SCRIPT_DIR}/README.md" "${BUILD_ROOT}/usr/share/doc/${APP_NAME}/"
fi

# The debconf question for CAP_PERFMON (see packaging/debian/templates).
# config/postinst/postrm must be executable.
install -m 644 "${SCRIPT_DIR}/packaging/debian/templates" "${BUILD_ROOT}/DEBIAN/templates"
for script in config postinst postrm; do
    install -m 755 "${SCRIPT_DIR}/packaging/debian/${script}" "${BUILD_ROOT}/DEBIAN/${script}"
done

# Create DEBIAN/control file
cat <<EOF > "${BUILD_ROOT}/DEBIAN/control"
Package: ${APP_NAME}
Version: ${VERSION}
Section: utils
Priority: optional
Architecture: ${ARCH}
Maintainer: Travis Lytle <travlytle@gmail.com>
Homepage: https://github.com/travlytle/xpu-smi-tui
Depends: debconf (>= 0.5) | debconf-2.0, procps
Recommends: libcap2-bin, libze1, libze-intel-gpu1
Description: Terminal Dashboard for Intel GPUs
 A modern terminal user interface (TUI) for monitoring Intel Arc and Data
 Center GPUs powered by Charm (Bubble Tea & Lip Gloss). Provides live gauges,
 btop-style multi-height Braille time-series charts, and process monitoring.
 .
 Telemetry is read in-process from Level Zero Sysman; xpu-smi is not needed.
 At install it asks whether to grant the binary CAP_PERFMON, which gives exact
 VRAM on Arc Pro A-series cards and accurate PCIe throughput without root.
EOF

# Calculate installed size
INSTALLED_SIZE=$(du -sk "${BUILD_ROOT}/usr" | cut -f1)
echo "Installed-Size: ${INSTALLED_SIZE}" >> "${BUILD_ROOT}/DEBIAN/control"

# Build package
dpkg-deb --build --root-owner-group "${BUILD_ROOT}" "${SCRIPT_DIR}/${DEB_NAME}"

echo "✓ Successfully generated ${SCRIPT_DIR}/${DEB_NAME}"
dpkg-deb --info "${SCRIPT_DIR}/${DEB_NAME}"
