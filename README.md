# payload

**Homepage:** https://snyype.github.io/payload-holder/

A single Go binary to save & retrieve named JSON payloads from any terminal (cmd, PowerShell, Git Bash).
Each key is stored as its own file `<key>.json` in `C:\payload` (override with `PAYLOAD_STORE`; `~/payload` on non-Windows).

## Commands

```
payload          list saved keys, pick one, print its JSON
payload <key>    print that key's JSON directly (no menu; pipe-friendly)
payload store    enter a key name, then paste JSON to save
payload update   pick an existing key, then paste new JSON to replace it
payload copy [key]   copy that key's JSON to the clipboard (menu when no key)
payload drop     pick one or more keys to delete (e.g. 1 3 5-7, or all)
payload drop <key> [<key>...]   delete the named keys
payload list     print key names only
payload table [page]   browse keys in a table, 10 per page (←/→ to page, enter to view, c to copy, d to delete)
payload env <name>          print an env vault value (raw, pipe-friendly)
payload env set <name> [value]   set a value (reads it from input when no value is given)
payload env list            print env value names only
payload env drop <name>...  delete env values
payload env table           browse env values in a masked table (v show/hide, V all)
payload curl <name> [flags]      run a saved curl request; extra flags (e.g. -vvk) go to curl, --pretty formats JSON
payload curl set <name> [cmd]    save a curl command (opens an editor to paste it when not given)
payload curl show <name>    print a saved curl command
payload curl list           print saved curl request names only
payload curl drop <name>... delete saved curl requests
payload curl table [page]   browse curl requests (enter run, e edit, a add, c copy, d delete)
payload curl path           print the curl that payload curl runs (bundled, or the one on PATH)
payload curl install        download the bundled curl into bin\curl (Windows)
payload ssh clients         ssh logins in client folders (n folder, s login, enter open/connect, p copy password)
payload ssh <folder/login>  connect; each password is copied to the clipboard the moment ssh asks for it
payload ssh list            print every login as folder/login
payload ssh export [file.zip] [--no-passwords]   all clients, logos and logins in a zip (passwords encrypted)
payload ssh import <file.zip> [-y]   add or update clients and logins from an export
payload ssh path | install  the ssh payload runs / download the bundled ssh into bin\ssh (Windows)
payload clients             pick a listening computer and send it text (enter, paste, Ctrl+S)
payload clipboard listen    receive text from the network onto your clipboard (Ctrl+C stops)
payload clipboard [text]    send text (or piped input, or your clipboard) to every listener on the network
payload auth login          log in with GitHub (opens the browser; token stored encrypted)
payload auth status|logout  show the login and check it works / delete it from this computer
payload sync push           make your private <you>/payload-sync repo match this computer
payload sync pull [-y]      make this computer match the sync repo (backs up first)
payload sync                show the sync setup
payload path     print the storage folder
payload settings print the key-bindings settings file path
payload settings set-name <name>   set the name others see in payload clients (payload settings name prints it)
payload setup    create the settings file if missing and refresh the AI README
payload ai       print the guide for AI assistants (markdown)
payload version  print the installed version
payload changelog [version]   print what changed in each release (built in, works offline)
payload doctor   check the install; show what's new and update to the latest release if there is one
payload fix      reinstall the version you're on (verified against its SHA256SUMS)
payload fix --verify   check the installed binary against its release's SHA256SUMS (changes nothing)
```

In a console (cmd, PowerShell, Windows Terminal), `store` and `update` open a JSON editor: arrow keys,
Home/End and PgUp/PgDn move the cursor, a status line shows whether the JSON is valid, **Ctrl+S** saves and
**Esc** cancels. `update` opens with the current JSON so you can edit it in place.
When input is piped (or in Git Bash/mintty), paste instead and finish with **Ctrl+Z then Enter** (Windows) or **Ctrl+D** (Unix).

## Env vault

Global values — a base URL, a token — that any payload can use as `{{name}}`:

```sh
payload env set localUrl http://localhost:8000
payload env set token            # prompts for the value, so it stays out of shell history
echo "$TOKEN" | payload env set token
```

```json
{"callback": "{{localUrl}}/hook", "auth": "{{ token }}"}
```

`payload <key>`, `payload copy` and the view screen fill placeholders in with the current values; the saved
file, the editor and the table preview keep the raw `{{name}}`, so editing never bakes a value in. A
placeholder whose name isn't set is left as written, with a warning on stderr. Values are inserted as-is
(the quotes come from your JSON). `payload env <name>` prints one value for scripts:
`curl "$(payload env localUrl)/orders" -d "$(payload create_order)"`.

`payload env table` shows the values in a red table with every value masked as `••••••••`; `v` shows or
hides the selected value, `V` all of them, `c` copies the real value, `d` deletes. Values are stored in
**plain text** in `vault/env.json` in the payload folder. Names follow the same rules as payload keys.

## Curl requests

Save whole curl commands under a name and run them again with one word:

```sh
payload curl set risk_score        # opens an editor: paste the curl command, Ctrl+S to save
payload curl risk_score            # run it
payload curl risk_score -vvk       # run it with extra curl flags (appended, so they win)
payload curl risk_score -s > out.json
payload curl risk_score -s --pretty      # print the JSON response indented and coloured
```

Paste the command exactly as you copied it — multi-line with `\` continuations, PowerShell's
`` ` `` continuations, or a browser's "Copy as cURL (cmd)" with `^` all work. `{{name}}` placeholders
are filled in from the env vault when the request runs, so put tokens there:

```sh
curl --request POST \
  --url '{{riskUrl}}/api/v1/cdd/risk/score?cddtype=individual' \
  --header 'Authorization: Bearer {{token}}' \
  --header 'Content-Type: application/json' \
  --data '{"categorytype": "individual", "results": ["NP", null]}'
```

**Fixes on save.** Before saving, payload repairs common copy-paste damage and tells you what it
changed: a leading `$` prompt, spaces after a line-continuation `\`, curly quotes, a missing `curl`,
a lower-case `-X post`, and trailing commas in a `--data` JSON body. It warns (but still saves) when a
`--data` body isn't valid JSON, a header has no `:`, or there's no URL, and refuses a command it can't
parse (an unclosed quote) or one that isn't curl.

**Every step is shown** on stderr — which request it loaded, which `{{placeholders}}` it filled, the
flags you added, the exact command, and how long curl took (or its exit code with the reason, such as
`6 couldn't resolve host`). The response goes to stdout, so `payload curl x > out.json` stays clean.
`payload` exits with curl's exit code.

**`--pretty`** is payload's own flag (curl never sees it): the response's JSON body is printed indented,
and coloured on a terminal. Headers from `-i` are kept as they are, a body that isn't JSON is printed
unchanged, and piping still gives plain JSON (`payload curl x -s --pretty > out.json`). Save `--pretty`
in a request to always format that one. The response is printed once curl finishes rather than as it
streams in.

**Big bodies work.** Windows can't start a program whose command line is over 32,767 characters, so
when a request is that big (an 80 KB `--data` body, say) payload moves the long values into temporary
curl config files passed with `-K` — curl reads them in place, so nothing changes about the request —
and deletes them when curl finishes. The step output shows a long value shortened, with its size.

`payload curl table` lists the requests with their method and URL in a blue table: **enter** runs
the selected one, **e** edits (sets) it, **a** adds a new one, **c** copies the command with
placeholders filled in, **d** deletes. Requests are stored in **plain text** in `vault/curl.json`.

**Bundled curl (Windows).** Every release ships the official [curl.se](https://curl.se/windows/)
Windows build, and the installer (or `payload doctor` / `payload fix`) puts it at
`C:\payload\bin\curl\curl.exe`, checked against the release's `SHA256SUMS` like the payload binary.
`payload curl` uses it first, so requests behave the same whatever curl your shell has (Git Bash,
System32 or none). It lives in its own folder, which is **not** on PATH, so a plain `curl` in your
terminal is unchanged. `payload curl path` prints the curl in use; `payload curl install` downloads it
again. Without it — and on macOS/Linux, which always have curl — `payload curl` runs the curl on PATH.

## Shared clipboard

Copy on one computer, paste on another. Everyone who wants to receive runs `payload clipboard listen` and
leaves it open; text that arrives goes straight onto their clipboard, with a line saying who sent it:

```
19:53:30 ✓ Copied text from DESKTOP-1R2LC2N (192.168.1.65, 24 B)  http://192.168.1.65:8000
```

To send, `payload clipboard <text>` goes to every listener on the network. With no text it sends piped
input (`payload create_order | payload clipboard`, up to 16 MB), or else whatever is on your clipboard.
Each listener is listed with ✓ or ✗ as it receives.

Listeners are found with a broadcast on the local network (UDP and TCP port **47321**), so there is
nothing to set up — but Windows Firewall asks the first time `listen` runs; allow it on **private**
networks. While listening, anyone on the network can put text on your clipboard, and text travels
unencrypted: use it on networks you trust and stop it (Ctrl+C) when you're done.

**Send to one person.** `payload clients` lists everyone listening, with their name and IP:

```
 clients   2 listening · you are snype
╭───┬───────────────────────┬──────────────╮
│ # │ NAME                  │ IP           │
├───┼───────────────────────┼──────────────┤
│ 1 │ LAPTOP-K0482585       │ 192.168.1.64 │
│ 2 │ snype (this computer) │ 192.168.1.65 │
╰───┴───────────────────────┴──────────────╯
```

↑/↓ to pick, **enter** opens a text area to type or paste into, **Ctrl+S** sends it to that computer
only (Esc cancels), then you're back at the table. The table updates itself every 3 seconds, so people who start or
stop listening appear and disappear while it's open; **r** searches right away. The first time you run it,
payload asks your name — that's what others see in their table and in their listen log. It is saved as
`"name"` in `settings.json`; change it with `payload settings set-name "snype"` (a running listener
picks it up straight away). Without a name, the computer name is used.

**Hold a client's text for approval.** Every computer has a **device ID** (like `pd-3f9a0c41d27be610`),
made from the operating system's own machine ID, so it stays the same however often payload is
reinstalled; `payload doctor` shows yours. In `payload clients`, press **s** for clipboard settings: a
list of every client payload knows (listening now or set earlier), with what happens to their text.
Press **space** (or enter, or h) on one to put it **on hold**: text they send still appears in your
`payload clipboard listen` window, in full, but is **not copied** to your clipboard, and their send shows
`⏸ held`. Toggling tells them — `⚑ snype flagged you risky — text you send them is on hold until they
turn it off` in their listen window, or, if they aren't listening, the next time payload sees them —
and toggling it off tells them it's released. Holds are saved by device ID under
`"clipboard_clients"` in `settings.json`. Clients on payload older than v2.4.0 show as
"copied (old version)" and can't be held until they update. A device ID is what the other computer
says it is: it stops casual sending, not a determined attacker on your network.

## SSH clients

Keep ssh logins in folders per client — `GIBL → Acopening → onboarding.ssh` — and connect by picking
one. `payload ssh clients` opens the table:

```
 ssh   clients
╭───┬────────┬──────┬──────────────╮
│ # │        │ NAME │ DETAILS      │
├───┼────────┼──────┼──────────────┤
│ 1 │ ▀▀▀▀▀▀ │ GIBL │ 2 subfolders │   ← a PNG/JPEG logo, drawn in the terminal
│   │ ▀▀▀▀▀▀ │      │              │
│ 2 │ 🏦     │ MBL  │ 1 subfolder  │   ← or an emoji
╰───┴────────┴──────┴──────────────╯
```

- **enter** opens a folder (backspace goes back) or connects to a login; **n** adds a folder — tick
  *Mark as parent* for a top-level client and pick its **icon**: an emoji, or **an image file** (PNG or
  JPEG; payload keeps a small copy in `vault/ssh-logos/` and draws it with coloured half blocks, plus a
  larger preview of the selected client under the table). Leave it unticked to put the folder inside
  another one.
- **s** adds an ssh login in the current folder: its name (shown as `onboarding.ssh`), the ssh command
  (`ssh root@10.0.0.5`), its **password**, and — when *Through a jump host* is ticked — the jump host
  (`admin@1.2.3.4`, used as `-J`) and **its password**. A pasted `-J` is picked up too.
- **Passwords are bound to the login.** They go into the env vault under the login's ID: one entry,
  `ssh_<id>`, holding both when there's a jump host (`{"server": "…", "jump": "…"}`, shown in
  `payload env table` as `ssh_<id>.server` and `ssh_<id>.jump`). Deleting the login removes them.
- **Connecting** (enter, or `payload ssh GIBL/Acopening/onboarding`) runs ssh in your terminal. At each
  password prompt — the jump host's first, then the server's — payload copies the matching password
  to the clipboard at that moment and shows the prompt; paste it (right-click or Ctrl+V). Nothing is
  typed for you. When the session ends, payload clears the password from the clipboard.
- **e** edits, **p** copies a login's password, **c** its command, **d** deletes (a folder with everything
  in it). The folders and logins are in `vault/ssh.json`; `payload sync` carries them and the logos.
- **Export / import.** `payload ssh export` writes a `.zip` with every folder, logo and login (same IDs,
  so importing on another computer updates rather than duplicates). Passwords are included only
  **encrypted** with a passphrase you choose (AES-256-GCM, key from PBKDF2-SHA256 with 600,000 rounds);
  `--no-passwords` leaves them out. `payload ssh import file.zip` shows what it adds (+) and updates (~),
  asks, and asks for the passphrase. For scripts: `-y`, and the passphrase in `PAYLOAD_SSH_PASSPHRASE`.
- **Bundled ssh (Windows).** Each release ships Microsoft's OpenSSH for Windows client
  (`ssh.exe` + `libcrypto.dll`, checked against the SHA-256 GitHub publishes) at `C:\payload\bin\ssh\`,
  installed and updated like the bundled curl, and not on PATH. Without it payload uses Windows' own
  ssh; on macOS/Linux the ssh on PATH.

## Sync with GitHub

Keep payloads, saved curl requests and settings the same on every computer through **one private
repo, `<you>/payload-sync`**, which is the only repository payload can reach.

One-time setup on GitHub:

1. Create a **private** repo named `payload-sync` (empty is fine).
2. Install the **payload** GitHub App on your account and choose **Only select repositories →
   payload-sync**. You don't need to look for it: right after you log in, `payload auth login` opens
   the install page (or, if the app is installed without that repo, its settings) in your browser and
   waits until payload can reach the repo.

```sh
payload auth login     # opens GitHub in your browser; approve, and you're back in the terminal
payload sync push      # upload this computer's payloads, curl requests and settings
payload sync pull      # on another computer (after payload auth login): download everything
```

- **Access is limited to that one repo.** payload logs in through a GitHub App, not a classic OAuth
  token: the app asks only for *Contents: read & write* on repositories it is installed on (plus your
  email), so its token can't see or change any other repository. The repo name is fixed.
- **Login.** `payload auth login` starts a one-off server on `http://127.0.0.1:47322/callback`, opens
  GitHub's login page, and finishes when GitHub redirects back there (protected by a random `state` and
  PKCE). The tokens are stored **encrypted** in `vault/auth.json`: with Windows DPAPI (only your
  Windows account on this computer can decrypt them), or on macOS/Linux with AES-256-GCM using a key
  kept outside the payload folder (`~/.config/payload/secret.key`). The access token lasts 8 hours and
  payload renews it by itself; after about 6 months unused you log in again. `payload auth status` shows
  who you are and checks GitHub accepts the token and the repo is reachable; `payload auth logout`
  deletes the tokens (and prints the GitHub page to revoke them there too).
- **push** makes the repo match this computer: payloads go to `payloads/<key>.json`, plus `curl.json`,
  `ssh.json` (with the client logos in `ssh-logos/`) and `settings.json`. Each push is a commit, so older versions stay in the repo's history.
- **pull** makes this computer match the repo: it lists what will be added (+), replaced (~) and deleted
  (-), asks first (`-y` skips the question, required when not on a terminal), and copies everything it
  replaces or deletes to `.cache/sync-backup/<time>/` before touching it.
- **Never synced:** the env vault (`vault/env.json` — tokens stay on each computer), the login itself,
  caches, and your `payload clients` name, which stays per computer.
- payload refuses to sync if the repo is public. `payload sync` shows the current setup.

## Install (prebuilt binary)

From Git Bash (Windows), macOS or Linux:

```sh
curl -fsSL https://raw.githubusercontent.com/snyype/payload-holder/main/install.sh | sh
```

It downloads the right binary from the latest GitHub Release, sets up the payload folder
(`C:\payload` on Windows, `~/payload` elsewhere) and adds its `bin` folder to PATH. Open a new terminal afterwards.

```
C:\payload\                    (~/payload on macOS/Linux)
├── <key>.json                 your saved payloads
├── README.md                  guide for AI assistants: what payload can do and how to drive it
├── bin\payload.exe            the binary (on PATH)
├── bin\curl\curl.exe          bundled curl for payload curl (Windows; not on PATH)
├── vault\
│   ├── env.json               env vault values (plain text)
│   └── curl.json              saved curl requests (plain text)
└── customization\
    └── settings.json          key-binding overrides
```

Installs from v1.3.0 and earlier put the binary in `C:\payload` or `~/.local/bin`; the installer moves it into `bin`.

While the repository is private, pass a GitHub token with read access to it:

```sh
curl -fsSL -H "Authorization: Bearer $GITHUB_TOKEN" \
  https://raw.githubusercontent.com/snyype/payload-holder/main/install.sh | GITHUB_TOKEN=$GITHUB_TOKEN sh
```

Re-running the installer checks the installed version (`payload version`): it does nothing if you
already have the latest release, and updates in place if a newer one exists.

**Checksums:** every release includes a `SHA256SUMS` file with the SHA-256 of each binary, generated by
the release workflow when it builds them. The installer (and `payload doctor`) hashes the downloaded
binary and compares it; on a mismatch it prints a warning and installs nothing, leaving your current
`payload` untouched. To check a manual download yourself, put `SHA256SUMS` next to it and run
`sha256sum -c SHA256SUMS --ignore-missing` (macOS: `shasum -a 256 -c SHA256SUMS --ignore-missing`).

Options: `PAYLOAD_VERSION=v1.0.0` to pin a release (also allows downgrading), `INSTALL_DIR=...` to
change the install folder, `FORCE=1` to reinstall the same version.

## Customization

Key bindings for the tables and the JSON editor live in `customization/settings.json` inside the payload
folder (`payload settings` prints the path). The installer creates it with the defaults and never
overwrites it. Each action takes a list of keys; settings **override** the defaults, so leaving an
action out — or deleting the whole file — falls back to the built-in keys. **Ctrl+C** always quits.

```json
{
  "update_check": true,
  "name": "snype",
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
    "clients_table": {
      "send": ["enter"],
      "refresh": ["r"],
      "settings": ["s"]
    },
    "client_settings": {
      "hold": ["space", "enter", "h"]
    },
    "ssh_table": {
      "open": ["enter"],
      "back": ["backspace", "left"],
      "new_folder": ["n"],
      "new_login": ["s"],
      "edit": ["e"],
      "copy_password": ["p"]
    },
    "editor": {
      "save": ["ctrl+s"],
      "cancel": ["esc"]
    }
  }
}
```

`payload env table` moves, copies and deletes with the `table` keys; `env_table` only adds the show/hide keys.

Key names: letters as typed (case matters), `space`, `enter`, `esc`, `tab`, `backspace`, `delete`,
`up`/`down`/`left`/`right`, `home`/`end`, `pgup`/`pgdown`, and modifiers such as `ctrl+s` or `alt+x`.
Help lines show your keys. A broken file or an unknown action is reported and ignored. Run
`payload setup` to recreate a deleted file.

## Updates

Once a day, payload checks GitHub for a newer release (one HEAD request to the public
`releases/latest` page; nothing about you or your payloads is sent). When there is one, every command
ends with a line like:

```
↑ New version v1.5.0 available (you have v1.4.0) — run: payload doctor
```

Without internet it says `Version check failed (no internet connection)` once and tries again the next
day. The notice goes to stderr and only appears on a terminal, so piped output is untouched.

`payload doctor` checks the install (version, binary and PATH, storage, settings, and on Linux the
clipboard) and, when a newer release exists, shows **What's new** — the changelog of every release
between yours and the latest — then offers to update in place: it downloads the release for
your OS, checks it against the release's `SHA256SUMS`, makes sure it runs, swaps it in and refreshes
the AI README. If an install seems broken,
`payload fix --verify` re-downloads the checksums for your version and checks the installed binary, and
`payload fix` reinstalls that same version (it also recreates a missing settings file). Turn the daily check off with
`"update_check": false` in `settings.json` or `PAYLOAD_NO_UPDATE_CHECK=1`.

## AI assistants

The installer also writes `README.md` into the payload folder: a guide for AI assistants (Claude,
Copilot, …) on what payload can do, where payloads live, and how to use it without the interactive menus.
`payload ai` prints the same guide, so an agent can read it with one command.

## Releasing

The release build needs the payload **GitHub App**'s client ID and secret as repository Actions
secrets `PAYLOAD_GITHUB_CLIENT_ID` and `PAYLOAD_GITHUB_CLIENT_SECRET`, and its URL name as the Actions
variable `PAYLOAD_GITHUB_APP_SLUG` (for the "install the app" link). The app's callback URL is
`http://127.0.0.1:47322/callback`; its permissions are *Contents: read and write* (repository) and
*Email addresses: read-only* (account). Local builds read the same names from environment variables.

Add a section for the new version at the top of [`CHANGELOG.md`](CHANGELOG.md), commit it, then push a
version tag; the `release` workflow builds Windows/Linux/macOS binaries and attaches them to a GitHub
Release whose notes are that section:

```md
## v1.0.1 — 2026-11-02

- What changed, one bullet per change.
```

```sh
git tag v1.0.1 && git push origin v1.0.1
```

The workflow fails before publishing if `CHANGELOG.md` has no `## <tag>` section. `payload doctor`
reads the same file (from the new tag) to show "What's new".

## If Windows blocks payload

payload's Windows binaries are **not code-signed**. Windows doesn't let unsigned apps run while some of
its protections are on, so you may see one of these. It happens because the binary is unsigned, not
because Windows found anything harmful in it:

- **"Windows protected your PC" (SmartScreen)**, usually after downloading the `.exe` in a browser:
  click **More info → Run anyway**.
- **`Permission denied` / "An Application Control policy has blocked this file" (Smart App Control)**:
  Smart App Control has no per-app allow list, so the only way to run payload is to turn it off:
  **Windows Security → App & browser control → Smart App Control settings → Off**. On some Windows 11
  versions it can't be switched back on afterwards without resetting Windows.

It is safe to allow payload: it is open source (MIT), every release binary is built by GitHub Actions
from this repository's tagged source, and its only network use is a once-a-day check for new releases on GitHub (which you can turn off) and the download when you choose to update. If you'd rather check for
yourself, [build it from source](#build--install-from-source-windows).

**Privacy policy:** this program will not transfer any information to other networked systems unless
specifically requested by the user or the person installing or operating it. Saved payloads stay in
the local storage folder (`C:\payload` / `~/payload`). The daily update check sends only a request
for the latest release tag to GitHub and can be turned off (see [Updates](#updates)).

## Build & install from source (Windows)

```powershell
go build -o payload.exe .
New-Item -ItemType Directory -Force -Path C:\payload\bin | Out-Null
Copy-Item .\payload.exe C:\payload\bin\
C:\payload\bin\payload.exe setup   # settings file + AI README
$old = [Environment]::GetEnvironmentVariable('Path','User')
if (($old -split ';') -notcontains 'C:\payload\bin') { [Environment]::SetEnvironmentVariable('Path', "$old;C:\payload\bin", 'User') }
```

Open a new terminal afterwards so the PATH change applies.

## License

[MIT](LICENSE)
