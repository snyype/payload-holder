#!/bin/sh
# Install the payload CLI from GitHub Releases and add it to PATH.
#
#   curl -fsSL https://raw.githubusercontent.com/snyype/payload-holder/main/install.sh | sh
#
# Works in Git Bash (Windows), macOS and Linux.
# Env overrides:
#   PAYLOAD_VERSION   release tag to install (default: latest)
#   INSTALL_DIR       where to put the binary (default: C:\payload on Windows, ~/.local/bin elsewhere)
#   GITHUB_TOKEN      token with repo read access — needed while the repository is private
set -eu

REPO="snyype/payload-holder"
VERSION="${PAYLOAD_VERSION:-latest}"

err() { echo "install.sh: $*" >&2; exit 1; }

case "$(uname -s)" in
  Linux*) os=linux ;;
  Darwin*) os=darwin ;;
  MINGW* | MSYS* | CYGWIN*) os=windows ;;
  *) err "unsupported OS: $(uname -s)" ;;
esac

case "$(uname -m)" in
  x86_64 | amd64) arch=amd64 ;;
  arm64 | aarch64) arch=arm64 ;;
  *) err "unsupported architecture: $(uname -m)" ;;
esac

ext=""
bin="payload"
if [ "$os" = "windows" ]; then
  ext=".exe"
  bin="payload.exe"
  INSTALL_DIR="${INSTALL_DIR:-/c/payload}"
else
  INSTALL_DIR="${INSTALL_DIR:-$HOME/.local/bin}"
fi
asset="payload_${os}_${arch}${ext}"

command -v curl >/dev/null 2>&1 || err "curl is required"
mkdir -p "$INSTALL_DIR"
tmp="$(mktemp)"
trap 'rm -f "$tmp"' EXIT

echo "Downloading $asset ($VERSION)..."
if [ -n "${GITHUB_TOKEN:-}" ]; then
  # Private repo: resolve the asset id through the API, then download it as a binary.
  if [ "$VERSION" = "latest" ]; then
    rel_url="https://api.github.com/repos/$REPO/releases/latest"
  else
    rel_url="https://api.github.com/repos/$REPO/releases/tags/$VERSION"
  fi
  rel_json="$(curl -fsSL -H "Authorization: Bearer $GITHUB_TOKEN" "$rel_url")" ||
    err "could not read release $VERSION (check GITHUB_TOKEN and that a release exists)"
  # Asset objects list "url" (…/releases/assets/<id>) a few lines before "name".
  asset_api="$(printf '%s\n' "$rel_json" | grep -E '"(url|name)"' |
    grep -B1 "\"name\": *\"$asset\"" | grep '/releases/assets/' |
    sed -E 's/.*"url": *"([^"]+)".*/\1/' | head -n1)"
  [ -n "$asset_api" ] || err "asset $asset not found in release $VERSION"
  curl -fsSL -H "Authorization: Bearer $GITHUB_TOKEN" -H "Accept: application/octet-stream" \
    -o "$tmp" "$asset_api" || err "download failed"
else
  if [ "$VERSION" = "latest" ]; then
    url="https://github.com/$REPO/releases/latest/download/$asset"
  else
    url="https://github.com/$REPO/releases/download/$VERSION/$asset"
  fi
  curl -fsSL -o "$tmp" "$url" ||
    err "download failed: $url (if the repo is private, set GITHUB_TOKEN)"
fi

mv -f "$tmp" "$INSTALL_DIR/$bin"
chmod +x "$INSTALL_DIR/$bin"
trap - EXIT
echo "Installed $INSTALL_DIR/$bin"

# Add INSTALL_DIR to PATH.
if [ "$os" = "windows" ]; then
  win_dir="$(cygpath -w "$INSTALL_DIR")"
  powershell.exe -NoProfile -Command "
    \$d = '$win_dir'
    \$old = [Environment]::GetEnvironmentVariable('Path','User')
    if ((\$old -split ';') -notcontains \$d) {
      [Environment]::SetEnvironmentVariable('Path', ((\$old.TrimEnd(';') + ';' + \$d).TrimStart(';')), 'User')
      Write-Output \"Added \$d to user PATH\"
    } else { Write-Output \"\$d already on user PATH\" }
  "
  echo "Open a new terminal (cmd, PowerShell or Git Bash), then run: payload --help"
else
  case ":$PATH:" in
    *":$INSTALL_DIR:"*) echo "$INSTALL_DIR already on PATH" ;;
    *)
      line="export PATH=\"$INSTALL_DIR:\$PATH\""
      for rc in "$HOME/.bashrc" "$HOME/.zshrc" "$HOME/.profile"; do
        [ -f "$rc" ] || [ "$rc" = "$HOME/.profile" ] || continue
        grep -qsF "$line" "$rc" || { printf '\n%s\n' "$line" >> "$rc"; echo "Added PATH entry to $rc"; }
      done
      echo "Restart your shell (or run: $line), then run: payload --help"
      ;;
  esac
fi
