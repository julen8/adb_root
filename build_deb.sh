#!/usr/bin/env bash
set -euo pipefail

APP_NAME="adb-root"
PACKAGE_NAME="adb-root"
VERSION="${VERSION:-1.0.0}"
REVISION="${REVISION:-1}"
ARCH="arm64"
MAINTAINER="${MAINTAINER:-adb-root maintainer <root@localhost>}"
DESCRIPTION="Web UI for running adb root helper script"

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DIST_DIR="${ROOT_DIR}/dist"
BUILD_DIR="${DIST_DIR}/${PACKAGE_NAME}_${VERSION}-${REVISION}_${ARCH}"
DEB_PATH="${DIST_DIR}/${PACKAGE_NAME}_${VERSION}-${REVISION}_${ARCH}.deb"

BIN_PATH="${BUILD_DIR}/usr/bin/${APP_NAME}"
SCRIPT_PATH="${BUILD_DIR}/usr/lib/${APP_NAME}/adb_root.sh"
SERVICE_PATH="${BUILD_DIR}/lib/systemd/system/${APP_NAME}.service"

require_command() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "missing required command: $1" >&2
    exit 1
  fi
}

require_command go
require_command dpkg-deb

if [[ ! -f "${ROOT_DIR}/adb_root.sh" ]]; then
  echo "missing script: ${ROOT_DIR}/adb_root.sh" >&2
  exit 1
fi

rm -rf "${BUILD_DIR}" "${DEB_PATH}"
mkdir -p \
  "$(dirname "${BIN_PATH}")" \
  "$(dirname "${SCRIPT_PATH}")" \
  "$(dirname "${SERVICE_PATH}")" \
  "${BUILD_DIR}/DEBIAN"

echo "building ${APP_NAME} for linux/${ARCH}..."
(
  cd "${ROOT_DIR}"
  CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags "-s -w" -o "${BIN_PATH}" .
)

install -m 0755 "${ROOT_DIR}/adb_root.sh" "${SCRIPT_PATH}"

cat >"${SERVICE_PATH}" <<EOF
[Unit]
Description=ADB Root Web Service
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=root
ExecStart=/usr/bin/${APP_NAME} -addr :8080 -script /usr/lib/${APP_NAME}/adb_root.sh
Restart=on-failure
RestartSec=3

[Install]
WantedBy=multi-user.target
EOF

cat >"${BUILD_DIR}/DEBIAN/control" <<EOF
Package: ${PACKAGE_NAME}
Version: ${VERSION}-${REVISION}
Section: utils
Priority: optional
Architecture: ${ARCH}
Maintainer: ${MAINTAINER}
Description: ${DESCRIPTION}
 Provides a systemd-managed web service on port 8080.
EOF

cat >"${BUILD_DIR}/DEBIAN/postinst" <<EOF
#!/bin/sh
set -e

if command -v systemctl >/dev/null 2>&1; then
  systemctl daemon-reload || true
  systemctl enable ${APP_NAME}.service || true
  systemctl restart ${APP_NAME}.service || true
fi

exit 0
EOF

cat >"${BUILD_DIR}/DEBIAN/prerm" <<EOF
#!/bin/sh
set -e

if [ "\$1" = "remove" ] || [ "\$1" = "deconfigure" ]; then
  if command -v systemctl >/dev/null 2>&1; then
    systemctl stop ${APP_NAME}.service || true
    systemctl disable ${APP_NAME}.service || true
  fi
fi

exit 0
EOF

cat >"${BUILD_DIR}/DEBIAN/postrm" <<EOF
#!/bin/sh
set -e

if command -v systemctl >/dev/null 2>&1; then
  systemctl daemon-reload || true
fi

exit 0
EOF

chmod 0755 \
  "${BUILD_DIR}/DEBIAN/postinst" \
  "${BUILD_DIR}/DEBIAN/prerm" \
  "${BUILD_DIR}/DEBIAN/postrm"

find "${BUILD_DIR}" -type d -exec chmod 0755 {} +
chmod 0755 "${BIN_PATH}" "${SCRIPT_PATH}"
chmod 0644 "${SERVICE_PATH}" "${BUILD_DIR}/DEBIAN/control"

mkdir -p "${DIST_DIR}"
dpkg-deb --build --root-owner-group "${BUILD_DIR}" "${DEB_PATH}"

echo "built: ${DEB_PATH}"
echo "install on Debian arm64 with: sudo apt install ./${DEB_PATH#${ROOT_DIR}/}"
