#!/bin/sh
set -e

REPO="rdalbuquerque/pr-filter"
BINARY="pr-filter-tui"
INSTALL_DIR="${INSTALL_DIR:-/usr/local/bin}"

# Detect OS
OS="$(uname -s)"
case "$OS" in
  Linux*)  OS=linux ;;
  Darwin*) OS=darwin ;;
  MINGW*|MSYS*|CYGWIN*) OS=windows ;;
  *) echo "Unsupported OS: $OS" >&2; exit 1 ;;
esac

# Detect architecture
ARCH="$(uname -m)"
case "$ARCH" in
  x86_64|amd64) ARCH=amd64 ;;
  aarch64|arm64) ARCH=arm64 ;;
  *) echo "Unsupported architecture: $ARCH" >&2; exit 1 ;;
esac

# Get latest version
if [ -z "$VERSION" ]; then
  VERSION="$(curl -sL "https://api.github.com/repos/${REPO}/releases/latest" | grep '"tag_name"' | sed -E 's/.*"([^"]+)".*/\1/')"
fi

if [ -z "$VERSION" ]; then
  echo "Error: could not determine latest version" >&2
  exit 1
fi

VERSION_NUM="${VERSION#v}"

# Build download URL
if [ "$OS" = "windows" ]; then
  EXT="zip"
else
  EXT="tar.gz"
fi
FILENAME="${BINARY}_${VERSION_NUM}_${OS}_${ARCH}.${EXT}"
URL="https://github.com/${REPO}/releases/download/${VERSION}/${FILENAME}"

echo "Installing ${BINARY} ${VERSION} (${OS}/${ARCH})..."

# Download and extract
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

curl -sL "$URL" -o "${TMP}/${FILENAME}"

if [ "$EXT" = "zip" ]; then
  unzip -q "${TMP}/${FILENAME}" -d "$TMP"
else
  tar -xzf "${TMP}/${FILENAME}" -C "$TMP"
fi

# Install
if [ -w "$INSTALL_DIR" ]; then
  mkdir -p "$INSTALL_DIR"
  cp "${TMP}/${BINARY}" "${INSTALL_DIR}/${BINARY}"
  chmod +x "${INSTALL_DIR}/${BINARY}"
else
  echo "Need sudo to install to ${INSTALL_DIR}"
  sudo mkdir -p "$INSTALL_DIR"
  sudo cp "${TMP}/${BINARY}" "${INSTALL_DIR}/${BINARY}"
  sudo chmod +x "${INSTALL_DIR}/${BINARY}"
fi

echo "Installed ${BINARY} to ${INSTALL_DIR}/${BINARY}"
