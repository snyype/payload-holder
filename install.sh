#!/bin/sh
# Install or update the payload CLI from GitHub Releases and add it to PATH.
#
# Layout (the payload folder is C:\payload on Windows, ~/payload elsewhere, or $PAYLOAD_STORE):
#   <folder>/bin/payload                    the binary (added to PATH)
#   <folder>/customization/settings.json    key-binding overrides (created once, never overwritten)
#   <folder>/README.md                      guide for AI assistants (refreshed on every install)
#   <folder>/<key>.json                     your saved payloads
#
#   curl -fsSL https://raw.githubusercontent.com/snyype/payload-holder/main/install.sh | sh
#
# Works in Git Bash (Windows), macOS and Linux. Re-running it updates an older install and
# does nothing if the installed version is already current.
# Env overrides:
#   PAYLOAD_VERSION   release tag to install (default: latest)
#   INSTALL_DIR       where to put the binary (default: <payload folder>/bin)
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
old_target=""
if [ "$os" = "windows" ]; then
  ext=".exe"
  bin="payload.exe"
  root="/c/payload"
  [ -n "${PAYLOAD_STORE:-}" ] && root="$(cygpath -u "$PAYLOAD_STORE")"
  [ -z "${PAYLOAD_STORE:-}${INSTALL_DIR:-}" ] && old_target="/c/payload/payload.exe"
else
  root="${PAYLOAD_STORE:-$HOME/payload}"
  [ -z "${PAYLOAD_STORE:-}${INSTALL_DIR:-}" ] && old_target="$HOME/.local/bin/payload"
fi
INSTALL_DIR="${INSTALL_DIR:-$root/bin}"
asset="payload_${os}_${arch}${ext}"
target="$INSTALL_DIR/$bin"

# Older versions installed the binary straight into C:\payload (Windows) or ~/.local/bin; move it
# into the new bin folder so there is only one payload on PATH. (Renaming works on Windows even
# while it is running.)
if [ -n "$old_target" ] && [ ! -f "$target" ] && [ -f "$old_target" ] && [ "$old_target" != "$target" ]; then
  mkdir -p "$INSTALL_DIR"
  mv -f "$old_target" "$target" && echo "Moved $old_target -> $target"
fi

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

# fetch NAME OUT downloads the release asset NAME of $tag into OUT; it fails quietly if it is missing.
fetch() {
  if [ -n "$rel_json" ]; then
    # Private repo: find the asset's API url (listed just before its "name") and fetch it as a binary.
    asset_api="$(printf '%s\n' "$rel_json" | grep -E '"(url|name)"' |
      grep -B1 "\"name\": *\"$1\"" | grep '/releases/assets/' |
      sed -E 's/.*"url": *"([^"]+)".*/\1/' | head -n1)"
    [ -n "$asset_api" ] || return 1
    curl -fsSL -H "Authorization: Bearer $GITHUB_TOKEN" -H "Accept: application/octet-stream" \
      -o "$2" "$asset_api"
  else
    curl -fsSL -o "$2" "https://github.com/$REPO/releases/download/$tag/$1"
  fi
}

# sha256 FILE prints the file's SHA-256, or nothing if no hashing tool is available.
sha256() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | awk '{print $1}'
  fi
}

if [ -z "${skip:-}" ]; then
  mkdir -p "$INSTALL_DIR"
  tmp="$(mktemp)"
  sums="$(mktemp)"
  trap 'rm -f "$tmp" "$sums"' EXIT

  echo "Downloading $asset..."
  fetch "$asset" "$tmp" ||
    err "download of $asset ($tag) failed (if the repo is private, set GITHUB_TOKEN)"

  # Compare against the SHA256SUMS that the release workflow published with the binaries.
  if fetch SHA256SUMS "$sums" 2>/dev/null; then
    expected="$(awk -v f="$asset" '$2 == f || $2 == "*" f { print $1; exit }' "$sums")"
    actual="$(sha256 "$tmp")"
    if [ -z "$actual" ]; then
      echo "note: no sha256sum/shasum found — skipping checksum verification"
    elif [ "$expected" != "$actual" ]; then
      cat >&2 <<EOF

  WARNING: checksum mismatch for $asset ($tag)
    expected (GitHub release SHA256SUMS): ${expected:-<not listed>}
    downloaded file:                      $actual

  The download is corrupted or is not the binary built by this repository's release workflow.
  Nothing was installed; your current payload (if any) is unchanged. Try again, and if it keeps
  happening, report it at https://github.com/$REPO/issues

EOF
      exit 1
    else
      echo "Checksum verified (sha256 $actual)"
    fi
  else
    echo "note: release $tag has no SHA256SUMS — skipping checksum verification"
  fi

  mv -f "$tmp" "$target"
  chmod +x "$target"
  rm -f "$sums"
  trap - EXIT
  echo "Installed $target ($tag)"
  if [ -n "$old_target" ] && [ -f "$old_target" ] && [ "$old_target" != "$target" ]; then
    rm -f "$old_target" 2>/dev/null || mv -f "$old_target" "$old_target.old" 2>/dev/null ||
      echo "warning: could not remove the old $old_target — delete it so it does not shadow $target" >&2
  fi

  if ! "$target" version >/dev/null 2>&1; then
    echo "warning: $target was installed but will not run." >&2
    if [ "$os" = "windows" ]; then
      echo "         Windows blocks unsigned apps while Smart App Control is on; see \"If Windows blocks payload\" in the README." >&2
    fi
  fi
fi

# Settings file (only if missing) and the README.md guide for AI assistants.
"$target" setup || echo "warning: \"payload setup\" failed; run it yourself later" >&2

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
