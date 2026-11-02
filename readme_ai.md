# payload — guide for AI assistants

`payload` {{VERSION}} is a command-line tool that saves and retrieves named JSON payloads (API request
bodies, test fixtures, config snippets). Each payload has a **key** (a name) and is stored as one plain
file. Use it to look up, save, update, copy or delete payloads for the user.

## Folder layout

```
{{ROOT}}/
├── <key>.json                 one file per saved payload (the file name is the key)
├── README.md                  this guide (rewritten by `payload setup`)
├── bin/                       the payload binary (on PATH)
│   └── curl/curl.exe          bundled curl that `payload curl` runs (Windows only; not on PATH)
├── vault/
│   ├── env.json               env vault: {"name": "value", ...} (plain text)
│   └── curl.json              saved curl requests: {"name": "curl ...", ...} (plain text)
└── customization/
    └── settings.json          key-binding overrides (optional)
```

The folder is `C:\payload` on Windows and `~/payload` elsewhere; the `PAYLOAD_STORE` environment
variable overrides it. `payload path` prints the folder in use.

You may read and write `<key>.json` files directly — that is exactly what the commands do. Key names
cannot be empty, `.` or `..`, or contain any of `/ \ : * ? " < > |`.

## Commands

| Command | What it does | Safe without a terminal? |
|---|---|---|
| `payload list` | Print every key, one per line | yes |
| `payload <key>` | Print that key's JSON (raw, unformatted when piped) | yes |
| `payload copy <key>` (`cp`) | Put that key's JSON on the system clipboard | yes |
| `payload store` | Save a new key (prompts for the name, then reads JSON) | yes, via stdin — see below |
| `payload update` | Replace an existing key's JSON | no — needs a menu; write the file instead |
| `payload drop <key>...` | Delete the named keys after a y/N confirmation | yes, pipe `y` |
| `payload table [page]` | Browse keys in a table (10 per page) | prints one page when piped |
| `payload env <name>` | Print an env vault value (raw, no trailing newline when piped) | yes |
| `payload env set <name> [value]` | Set a value; reads it from stdin when no value is given | yes |
| `payload env list` | Print env value names, one per line (never values) | yes |
| `payload env drop <name>...` | Delete env values after a y/N confirmation | yes, pipe `y` |
| `payload env table` | Browse env values, masked (prints one masked page when piped) | prints one page when piped |
| `payload curl <name> [flags...]` | Run a saved curl request; extra flags (e.g. `-vvk`, `-s`) are appended; `--pretty` (payload's own, not passed to curl) indents a JSON response. Steps on stderr, response on stdout, exits with curl's code | yes |
| `payload curl set <name> [command]` | Save a curl command (repairs common paste damage first); reads it from stdin when not given | yes, via stdin |
| `payload curl show <name>` | Print a saved command as stored, `{{placeholders}}` and all | yes |
| `payload curl list` | Print saved curl request names, one per line | yes |
| `payload curl drop <name>...` | Delete saved curl requests after a y/N confirmation | yes, pipe `y` |
| `payload curl table [page]` | Browse requests: enter run, e edit, a add, c copy, d delete | prints one page when piped |
| `payload curl path` | Print the curl `payload curl` runs: the bundled `bin/curl/curl.exe` if present, else the one on PATH | yes |
| `payload curl install` | Download the bundled curl for this release, verified against SHA256SUMS (Windows only) | yes |
| `payload clipboard listen` | Receive text from other computers on the network onto this clipboard; runs until Ctrl+C | no — long-running; don't start it for the user |
| `payload clipboard [text]` | Send text (or stdin, or the current clipboard when neither) to every `listen`er on the local network; exits 1 when none received it | yes |
| `payload path` | Print the storage folder | yes |
| `payload settings` | Print the settings file path | yes |
| `payload setup` | Create `customization/settings.json` if missing and rewrite this README | yes |
| `payload ai` | Print this guide (markdown) | yes |
| `payload version` | Print the version | yes |
| `payload fix` | Reinstall the current version from its release (checksum-verified; recreates a missing settings file) | yes |
| `payload fix --verify` | Check the installed binary against its release's SHA256SUMS; exit 1 on mismatch | yes |
| `payload doctor` | Check the install; offers to update when a newer release exists (answers y/N) | yes; pipe `y` to update |
| `payload` (no arguments) | Interactive picker; prints key names when piped | piped only |

Commands without a key (`payload`, `payload update`, `payload drop`, `payload copy`) open interactive
menus on a real terminal. When you run commands from a script or tool, always pass the key.

### Non-interactive recipes

```sh
payload list                                   # what is saved
payload create_order                           # read one payload (raw JSON on stdout)
payload create_order > body.json               # save it to a file
curl -X POST -d "$(payload create_order)" ...  # use it as a request body

# store a new key: first line = key name, the rest = JSON until end of input
{ echo create_order; cat body.json; } | payload store

# overwrite an existing key: answer the overwrite prompt with y
{ echo create_order; echo y; cat body.json; } | payload store

echo y | payload drop create_order             # delete (confirmation answered)
```

Saving accepts any text; it warns, but still saves, when the content is not valid JSON.

## Env vault and placeholders

A payload may contain `{{name}}` (spaces inside the braces allowed). `payload <key>` and `payload copy <key>`
replace each one with the value from `vault/env.json`; the file on disk keeps the placeholder. A name that is
not set is left as written and reported on stderr. To change a URL or token for every payload, set it once:

```sh
payload env set localUrl http://localhost:8000
printf '%s' "$TOKEN" | payload env set token   # value from stdin, kept out of shell history
payload env localUrl                           # -> http://localhost:8000
```

When you edit a payload file directly, keep the `{{name}}` placeholders rather than writing values in.
Values are stored in plain text; do not print secrets back to the user unless they ask.

## Saved curl requests

`vault/curl.json` maps a name to a full curl command (multi-line with `\` continuations is fine).
`payload curl <name>` splits it like a shell, fills `{{name}}` placeholders from the env vault, appends
any extra arguments and runs `curl`. Progress lines (`● Loaded …`, `● Filled in …`, the exact command,
`● Done in …`) go to stderr; only curl's own output goes to stdout. Request bodies of any size work:
when the command line would exceed Windows' 32,767-character limit, long option values are passed to
curl through temporary `-K` config files (deleted afterwards), and the step output shortens them.

```sh
# save: the command from stdin (keeps quoting intact — prefer this over passing it as an argument)
cat <<'EOF' | payload curl set risk_score
curl --request POST \
  --url '{{riskUrl}}/api/v1/cdd/risk/score?cddtype=individual' \
  --header 'Authorization: Bearer {{token}}' \
  --header 'Content-Type: application/json' \
  --data '{"categorytype": "individual"}'
EOF

payload curl risk_score -s                 # run it, response body only
payload curl risk_score -vvk               # verbose, skip TLS verification
payload curl risk_score -s --pretty        # JSON response indented (non-JSON printed as-is)
payload curl show risk_score               # what is saved
```

On save, payload fixes a leading `$` prompt, spaces after a continuation `\`, curly quotes, a missing
`curl`, a lower-case method and trailing commas in a JSON `--data` body, printing `● Fixed: …` for each;
it warns about invalid JSON bodies, headers without `:` and a missing URL. Keep tokens and base URLs as
`{{name}}` placeholders in the env vault rather than writing them into the saved command. Running a
request makes a real network call — confirm with the user before running one that changes data.

On Windows, `payload curl` runs its own bundled curl (`bin/curl/curl.exe`, the official curl.se build
shipped with each release), so behaviour doesn't depend on which curl the shell has. It is installed by
the installer, `payload setup`, `payload doctor` and `payload fix`; `payload curl install` fetches it on
demand. It is deliberately not on PATH — to call it directly use `"$(payload curl path)"`.

## Clipboard

`payload copy <key>` uses the OS clipboard: Win32 on Windows, `pbcopy` on macOS, and on Linux
`wl-copy`, `xclip` or `xsel` when installed. Without those, payload keeps the text available itself in a
small background process, as `xclip` does, until something else is copied. That needs a graphical
session (it does not work over plain SSH).

Shared clipboard: `payload clipboard <text>` finds listeners with a UDP broadcast on port 47321 and
sends over TCP on the same port, unencrypted, to every computer on the network running
`payload clipboard listen`. It sends to other people's machines — confirm before sending anything,
and never send secrets or tokens.

## Updates

Once a day payload asks GitHub for the latest release tag. If it is newer, commands print
`New version vX.Y.Z available (you have …) — run: payload doctor` on stderr (terminals only; never in
piped output). Offline, it prints `Version check failed (no internet connection)` once a day.
To update for the user: `echo y | payload doctor` (it verifies the download against the release's
`SHA256SUMS` and refuses a mismatch). To disable the check: `"update_check": false` in
`customization/settings.json` or `PAYLOAD_NO_UPDATE_CHECK=1`.

## Customization

`customization/settings.json` overrides key bindings for the interactive table and JSON editor.
Each action takes a list of keys; an action left out (or the whole file deleted) uses the defaults
below. Ctrl+C always quits.

```json
{
  "update_check": true,
  "keybinds": {
    "table": {
      "up": ["up", "k"],
      "down": ["down", "j"],
      "next_page": ["right", "l", "pgdown", "n", "space"],
      "prev_page": ["left", "h", "pgup", "p"],
      "first": ["home", "g"],
      "last": ["end", "G"],
      "view": ["enter"],
      "copy": ["c"],
      "delete": ["d", "delete"],
      "confirm_delete": ["y", "Y"],
      "quit": ["q", "esc"]
    },
    "env_table": {
      "reveal": ["v"],
      "reveal_all": ["V"]
    },
    "curl_table": {
      "run": ["enter"],
      "edit": ["e"],
      "add": ["a"]
    },
    "editor": {
      "save": ["ctrl+s"],
      "cancel": ["esc"]
    }
  }
}
```

Key names: letters and symbols as typed (case matters), `space`, `enter`, `esc`, `tab`, `backspace`,
`delete`, `up`, `down`, `left`, `right`, `home`, `end`, `pgup`, `pgdown`, and modifiers such as
`ctrl+s` or `alt+x`. To change a binding for the user, edit only the actions they ask about; the
change takes effect the next time payload starts. Run `payload setup` to recreate a deleted file.

Source and issues: https://github.com/snyype/payload-holder
