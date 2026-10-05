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

## Build & install (Windows)

```powershell
go build -o payload.exe .
New-Item -ItemType Directory -Force -Path C:\payload | Out-Null
Copy-Item .\payload.exe C:\payload\
$old = [Environment]::GetEnvironmentVariable('Path','User')
if ($old -notlike '*C:\payload*') { [Environment]::SetEnvironmentVariable('Path', "$old;C:\payload", 'User') }
```

Open a new terminal afterwards so the PATH change applies.
