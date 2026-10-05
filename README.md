# payload

**Homepage:** https://snyype.github.io/payload-holder/

A single Go binary to save & retrieve named JSON payloads from any terminal (cmd, PowerShell, Git Bash).
Each key is stored as its own file `<key>.json` in `C:\payload` (override with `PAYLOAD_STORE`; `~/payload` on non-Windows).

## Commands

```
payload          list saved keys, pick one, print its JSON
payload store    enter a key name, then paste JSON to save
payload update   pick an existing key, then paste new JSON to replace it
payload list     print key names only
payload path     print the storage folder
payload version  print the installed version
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

Re-running the installer checks the installed version (`payload version`): it does nothing if you
already have the latest release, and updates in place if a newer one exists.

Options: `PAYLOAD_VERSION=v1.0.0` to pin a release (also allows downgrading), `INSTALL_DIR=...` to
change the install folder, `FORCE=1` to reinstall the same version.

> **Windows Smart App Control:** if it is on, Windows blocks unsigned programs
> (`Permission denied` / "An Application Control policy has blocked this file"). Windows release
> binaries are code-signed (see [Code signing policy](#code-signing-policy)) so they run; local
> unsigned builds may still be blocked.

## Releasing

Push a version tag; the `release` workflow builds Windows/Linux/macOS binaries, has the Windows
binaries signed through SignPath, and attaches everything to a GitHub Release:

```sh
git tag v1.0.1 && git push origin v1.0.1
```

Each release-signing request must be approved in SignPath before the release is published.

### SignPath setup (one-time)

Signing is skipped until these are configured in the GitHub repo (**Settings → Secrets and variables → Actions**):

| Kind | Name | Value |
|---|---|---|
| Secret | `SIGNPATH_API_TOKEN` | API token of a SignPath CI user with submitter rights |
| Variable | `SIGNPATH_ORGANIZATION_ID` | SignPath organization ID |
| Variable | `SIGNPATH_PROJECT_SLUG` | SignPath project slug, e.g. `payload-holder` |
| Variable | `SIGNPATH_SIGNING_POLICY_SLUG` | `release-signing` (or `test-signing` while testing) |
| Variable | `SIGNPATH_ARTIFACT_CONFIGURATION_SLUG` | slug of the artifact configuration from [`.signpath/artifact-configuration.xml`](.signpath/artifact-configuration.xml) |

In SignPath, add the predefined **GitHub.com** trusted build system to the organization and link it to the project.

## Code signing policy

Free code signing provided by [SignPath.io](https://about.signpath.io), certificate by [SignPath Foundation](https://signpath.org).

Team roles:

- Committers and reviewers: [snyype](https://github.com/snyype)
- Approvers: [snyype](https://github.com/snyype)

Only binaries built by this repository's GitHub Actions `release` workflow from tagged source are signed,
and every signing request is manually approved.

**Privacy policy:** this program will not transfer any information to other networked systems unless
specifically requested by the user or the person installing or operating it. Saved payloads stay in
the local storage folder (`C:\payload` / `~/payload`).

## Build & install from source (Windows)

```powershell
go build -o payload.exe .
New-Item -ItemType Directory -Force -Path C:\payload | Out-Null
Copy-Item .\payload.exe C:\payload\
$old = [Environment]::GetEnvironmentVariable('Path','User')
if ($old -notlike '*C:\payload*') { [Environment]::SetEnvironmentVariable('Path', "$old;C:\payload", 'User') }
```

Open a new terminal afterwards so the PATH change applies.

## License

[MIT](LICENSE)
