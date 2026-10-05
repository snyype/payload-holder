# payload

A single Go binary to save & retrieve named JSON payloads from any terminal (cmd, PowerShell, Git Bash).
Each key is stored as its own file `<key>.json` in `C:\payload` (override with `PAYLOAD_STORE`; `~/payload` on non-Windows).

## Commands

```
payload          list saved keys, pick one, print its JSON
payload store    enter a key name, then paste JSON to save
payload update   pick an existing key, then paste new JSON to replace it
payload list     print key names only
payload path     print the storage folder
```

Finish pasting with **Ctrl+Z then Enter** (Windows) or **Ctrl+D** (Unix).

## Install (prebuilt binary)

From Git Bash (Windows), macOS or Linux:

```sh
curl -fsSL https://raw.githubusercontent.com/snyype/payload-holder/main/install.sh | sh
```

It downloads the right binary from the latest GitHub Release, installs it (`C:\payload` on Windows,
`~/.local/bin` elsewhere) and adds that folder to PATH. Open a new terminal afterwards.

While the repository is private, pass a GitHub token with read access to it:

```sh
curl -fsSL -H "Authorization: Bearer $GITHUB_TOKEN" \
  https://raw.githubusercontent.com/snyype/payload-holder/main/install.sh | GITHUB_TOKEN=$GITHUB_TOKEN sh
```

Options: `PAYLOAD_VERSION=v1.0.0` to pin a release, `INSTALL_DIR=...` to change the install folder.

## Releasing

Push a version tag; the `release` workflow builds Windows/Linux/macOS binaries and attaches them to a GitHub Release:

```sh
git tag v1.0.1 && git push origin v1.0.1
```

## Build & install from source (Windows)

```powershell
go build -o payload.exe .
New-Item -ItemType Directory -Force -Path C:\payload | Out-Null
Copy-Item .\payload.exe C:\payload\
$old = [Environment]::GetEnvironmentVariable('Path','User')
if ($old -notlike '*C:\payload*') { [Environment]::SetEnvironmentVariable('Path', "$old;C:\payload", 'User') }
```

Open a new terminal afterwards so the PATH change applies.
