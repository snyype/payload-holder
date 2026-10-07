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
| `payload path` | Print the storage folder | yes |
| `payload settings` | Print the settings file path | yes |
| `payload setup` | Create `customization/settings.json` if missing and rewrite this README | yes |
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

## Clipboard

`payload copy <key>` uses the OS clipboard: Win32 on Windows, `pbcopy` on macOS, and on Linux
`wl-copy`, `xclip` or `xsel` when installed. Without those, payload keeps the text available itself in a
small background process, as `xclip` does, until something else is copied. That needs a graphical
session (it does not work over plain SSH).

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
