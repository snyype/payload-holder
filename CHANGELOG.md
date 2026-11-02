# Changelog

Every release has a section here; the release build publishes it as the GitHub release notes and
`payload doctor` shows it under "What's new" before updating. `payload changelog` prints this file.

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
