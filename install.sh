#!/bin/sh
# Install or update the payload CLI from GitHub Releases and add it to PATH.
#
#   curl -fsSL https://raw.githubusercontent.com/snyype/payload-holder/main/install.sh | sh
#
# Works in Git Bash (Windows), macOS and Linux. Re-running it updates an older install and
# does nothing if the installed version is already current.
# Env overrides:
#   PAYLOAD_VERSION   release tag to install (default: latest)
#   INSTALL_DIR       where to put the binary (default: C:\payload on Windows, ~/.local/bin elsewhere)
#   GITHUB_TOKEN      token with repo read access — needed while the repository is private
#   FORCE=1           reinstall even if the same version is already installed
set -eu

REPO="snyype/payload-holder"

err() { echo "install.sh: $*" >&2; exit 1; }

# ver_cmp A B prints -1, 0 or 1 comparing dotted versions (leading "v" ignored).
ver_cmp() {
  a="${1#v}"; b="${2#v}"
  while [ -n "$a" ] || [ -n "$b" ]; do
    x="${a%%.*}"; y="${b%%.*}"
    x="${x%%[!0-9]*}"; y="${y%%[!0-9]*}"
    x="${x:-0}"; y="${y:-0}"
    [ "$x" -gt "$y" ] && { echo 1; return; }
    [ "$x" -lt "$y" ] && { echo -1; return; }
    case "$a" in *.*) a="${a#*.}" ;; *) a="" ;; esac
    case "$b" in *.*) b="${b#*.}" ;; *) b="" ;; esac
  done
  echo 0
}

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
target="$INSTALL_DIR/$bin"

command -v curl >/dev/null 2>&1 || err "curl is required"

# Resolve the release tag we are installing.
rel_json=""
if [ -n "${GITHUB_TOKEN:-}" ]; then
  if [ -n "${PAYLOAD_VERSION:-}" ]; then
    rel_url="https://api.github.com/repos/$REPO/releases/tags/$PAYLOAD_VERSION"
  else
    rel_url="https://api.github.com/repos/$REPO/releases/latest"
  fi
  rel_json="$(curl -fsSL -H "Authorization: Bearer $GITHUB_TOKEN" "$rel_url")" ||
    err "could not read release ${PAYLOAD_VERSION:-latest} (check GITHUB_TOKEN and that a release exists)"
  tag="$(printf '%s\n' "$rel_json" | grep -m1 '"tag_name"' | sed -E 's/.*"tag_name": *"([^"]+)".*/\1/')"
elif [ -n "${PAYLOAD_VERSION:-}" ]; then
  tag="$PAYLOAD_VERSION"
else
  # github.com/<repo>/releases/latest redirects to .../releases/tag/<tag>.
  latest_url="$(curl -fsSLI -o /dev/null -w '%{url_effective}' "https://github.com/$REPO/releases/latest")" ||
    err "could not find the latest release (if the repo is private, set GITHUB_TOKEN)"
  tag="${latest_url##*/}"
fi
[ -n "$tag" ] && [ "$tag" != "latest" ] || err "could not determine the release version"

# Compare with what is already installed (an old build without "version", or one the OS refuses
# to run, reports nothing and is simply replaced).
current=""
if [ -f "$target" ]; then
  current="$("$target" version 2>/dev/null)" || current=""
  case "$current" in v[0-9]*) ;; *) current="" ;; esac
fi

if [ -n "$current" ] && [ "${FORCE:-}" != "1" ]; then
  cmp="$(ver_cmp "$current" "$tag")"
  if [ "$cmp" = "0" ]; then
    echo "payload $current is already installed and up to date."
    skip=1
  elif [ "$cmp" = "1" ] && [ -z "${PAYLOAD_VERSION:-}" ]; then
    echo "payload $current is installed, which is newer than the latest release $tag — leaving it."
    skip=1
  else
    echo "Updating payload $current -> $tag"
  fi
elif [ -f "$target" ]; then
  echo "Replacing existing $target with $tag"
else
  echo "Installing payload $tag"
fi

if [ -z "${skip:-}" ]; then
  mkdir -p "$INSTALL_DIR"
  tmp="$(mktemp)"
  trap 'rm -f "$tmp"' EXIT

  echo "Downloading $asset..."
  if [ -n "$rel_json" ]; then
    # Private repo: find the asset's API url (listed just before its "name") and fetch it as a binary.
    asset_api="$(printf '%s\n' "$rel_json" | grep -E '"(url|name)"' |
      grep -B1 "\"name\": *\"$asset\"" | grep '/releases/assets/' |
      sed -E 's/.*"url": *"([^"]+)".*/\1/' | head -n1)"
    [ -n "$asset_api" ] || err "asset $asset not found in release $tag"
    curl -fsSL -H "Authorization: Bearer $GITHUB_TOKEN" -H "Accept: application/octet-stream" \
      -o "$tmp" "$asset_api" || err "download failed"
  else
    url="https://github.com/$REPO/releases/download/$tag/$asset"
    curl -fsSL -o "$tmp" "$url" ||
      err "download failed: $url (if the repo is private, set GITHUB_TOKEN)"
  fi

  mv -f "$tmp" "$target"
  chmod +x "$target"
  trap - EXIT
  echo "Installed $target ($tag)"

  if ! "$target" version >/dev/null 2>&1; then
    echo "warning: $target was installed but will not run." >&2
    if [ "$os" = "windows" ]; then
      echo "         Windows Smart App Control blocks unsigned programs; see the README note." >&2
    fi
  fi
fi

# Make sure INSTALL_DIR is on PATH.
if [ "$os" = "windows" ]; then
  win_dir="$(cygpath -w "$INSTALL_DIR")"
  powershell.exe -NoProfile -Command "
    \$d = '$win_dir'
    \$old = [Environment]::GetEnvironmentVariable('Path','User')
    if ((\$old -split ';') -notcontains \$d) {
      [Environment]::SetEnvironmentVariable('Path', ((\$old.TrimEnd(';') + ';' + \$d).TrimStart(';')), 'User')
      Write-Output \"Added \$d to user PATH - open a new terminal, then run: payload --help\"
    }
  "
else
  case ":$PATH:" in
    *":$INSTALL_DIR:"*) ;;
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
