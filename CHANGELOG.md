# Changelog

Every release has a section here; the release build publishes it as the GitHub release notes and
`payload doctor` shows it under "What's new" before updating. `payload changelog` prints this file.

## v2.5.0 — 2026-11-04

- New **`payload ssh clients`**: ssh logins in client folders (e.g. GIBL → Acopening → onboarding.ssh).
  `n` adds a folder — top-level clients get an emoji or a **PNG/JPEG logo**, drawn in the terminal —
  `s` adds a login, enter connects, `p` copies its password, `e` edits, `d` deletes.
- **Passwords on the clipboard, one prompt at a time:** when you connect, payload copies each password
  the moment ssh asks — the jump host's (`-J`), then the server's — and you paste it. Nothing is typed
  for you, and the clipboard is cleared when the session ends.
- A login's passwords are bound to it in the env vault as one entry, `ssh_<id>` — with a jump host a
  group holding both (`ssh_<id>.server`, `ssh_<id>.jump`). Env values can now be such groups.
- **`payload ssh export` / `import`:** one zip with every client, logo and login (same IDs), passwords
  encrypted with a passphrase.
- Windows releases bundle **Microsoft's OpenSSH client** in `bin\ssh` (checksum-verified, not on PATH);
  `payload ssh path` / `payload ssh install`, and `payload doctor` shows it.
- `payload sync` carries ssh folders, logins and logos (passwords stay in each computer's env vault).

## v2.4.1 — 2026-11-04

- The Windows `payload.exe` now has the payload icon (shown in Explorer, the taskbar and file
  Properties).

## v2.4.0 — 2026-11-04

- Every computer now has a **device ID** (`pd-` + 16 hex), made from the operating system's machine ID,
  so it stays the same across reinstalls. `payload doctor` shows it.
- **Hold a client's text for approval:** in `payload clients`, press `s` for clipboard settings and
  toggle a client with space. Their text then shows in your `payload clipboard listen` window but is not
  copied to your clipboard, and their send reports it as held.
- The client is told when you flag them (and when you release them) — straight away if they're
  listening, otherwise the next time payload sees them.
- `payload clients` shows a THEIR TEXT column (copied / on hold). Clients on older versions show as
  "copied (old version)" until they update.

## v2.3.2 — 2026-11-03

- After you approve the login, `payload auth login` opens the page to install the payload GitHub App on
  `payload-sync` (or to add the repo, if the app is installed without it) and waits until payload can
  reach the repo — no need to find the page yourself or run anything again.

## v2.3.1 — 2026-11-03

- Fixed `payload auth login` in v2.3.0, which was built with the old OAuth App and could not reach the
  sync repo. This build uses the payload GitHub App. Run `payload auth login` again after updating.

## v2.3.0 — 2026-11-03

- `payload sync` can now reach **only one repo**: `<you>/payload-sync`. Login is now a GitHub App
  installed on just that repo (Contents read & write), instead of a token with access to all your
  repositories. Create the private `payload-sync` repo and install the app once — `payload auth login`
  prints both links.
- The login callback is now `http://127.0.0.1:47322/callback`.
- Tokens renew themselves (8-hour access token, refresh token stored encrypted too).
- `payload auth status` checks the repo is reachable. `payload sync repo` no longer changes the repo.
- Log in again after updating: `payload auth login`.

## v2.2.0 — 2026-11-03

- New `payload auth login`: log in with GitHub in your browser (local `127.0.0.1` callback, state and
  PKCE checked). The token is stored encrypted — Windows DPAPI, or AES-256-GCM on macOS/Linux.
  `payload auth status` and `payload auth logout` too.
- New `payload sync push` / `payload sync pull`: keep payloads, saved curl requests and settings the same
  on every computer through a private GitHub repo (`<you>/payload-sync`, created on the first push).
  Pull asks first and backs up what it replaces. The env vault and your clients name are never synced.
- `payload doctor` shows the GitHub login.

## v2.1.1 — 2026-11-02

- `payload clients` now updates itself every 3 seconds: computers that start `payload clipboard listen`
  while the table is open appear on their own, and ones that stop drop off. `r` still searches right away.

## v2.1.0 — 2026-11-02

- `payload doctor` shows **What's new**: the changes in every release between yours and the latest,
  before asking to update.
- New `payload changelog [version]` prints the release history (works offline).
- Release notes on GitHub now come from this changelog.

## v2.0.0 — 2026-11-02

- Version 2: shared clipboard, clients table, saved curl requests and the env vault together.
  No changes since v1.11.0.

## v1.11.0 — 2026-11-02

- New `payload clients`: a table of everyone running `payload clipboard listen`; pick one, paste text
  and press Ctrl+S to send it to just them.
- Asks your name the first time; `payload settings set-name <name>` changes it and
  `payload settings name` prints it. Saved as `"name"` in settings.json.
- New `clients_table` key bindings (send, refresh).

## v1.10.0 — 2026-11-02

- New shared clipboard: `payload clipboard listen` receives text from other computers on the network
  onto your clipboard; `payload clipboard <text>` sends to every listener (or piped input, or your
  current clipboard).

## v1.9.0 — 2026-11-02

- `payload curl <name> --pretty` prints a JSON response indented and coloured; headers from `-i` are
  kept and non-JSON responses print as-is.

## v1.8.1 — 2026-11-02

- Fixed `payload curl` failing with "The filename or extension is too long" for very large request
  bodies: long values are passed to curl through temporary config files.

## v1.8.0 — 2026-11-02

- Windows releases bundle the official curl.se build at `bin\curl\curl.exe`, used by `payload curl`.
- New `payload curl path` and `payload curl install`.

## v1.7.0 — 2026-11-02

- New `payload curl`: save curl commands by name and run them with extra flags (e.g. `-vvk`), with
  step-by-step output and `{{name}}` placeholders from the env vault.
- `payload curl set` repairs common paste damage (prompts, curly quotes, trailing commas, …).
- New `payload curl table` to run, edit, add, copy and delete saved requests.

## v1.6.0 — 2026-10-19

- New env vault: `payload env set/list/drop/table`; payloads use values as `{{name}}`.
- Masked env table with show/hide.
- New `payload ai` prints a guide for AI assistants.

## v1.5.3 — 2026-10-08

- New `payload fix` reinstalls the current version; `payload fix --verify` checks the installed binary.

## v1.5.2 — 2026-10-08

- Available updates show in the payload view footer.

## v1.5.1 — 2026-10-08

- Downloads are verified against the release's SHA256SUMS; `payload doctor` can update dev builds.

## v1.5.0 — 2026-10-08

- Daily check for new versions and the new `payload doctor`.

## v1.4.0 — 2026-10-08

- Key-binding settings in `customization/settings.json`.
- Copy and delete from the table; built-in clipboard on Linux.

## v1.3.0 — 2026-10-07

- New `payload copy` and an arrow-key JSON editor for `store` and `update`.

## v1.2.0 — 2026-10-05

- Terminal UI, `payload table`, `payload <key>` direct lookup and `payload drop`.

## v1.1.0 — 2026-10-05

- New `payload version`; the installer skips or updates based on the installed version.

## v1.0.0 — 2026-10-05

- First release: save and retrieve named JSON payloads, with prebuilt binaries and `install.sh`.
