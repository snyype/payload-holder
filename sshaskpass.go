package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/x/term"
)

// Password prompts during "payload ssh". ssh runs payload as its SSH_ASKPASS helper for every prompt,
// so payload knows the moment each one appears: for a password prompt it copies the matching
// password (the jump host's or the server's) from the env vault to the clipboard, then shows the
// prompt in the terminal and passes on what you paste. Nothing is typed for you.

const askpassEnv = "PAYLOAD_ASKPASS" // set to 1 for the ssh child process; main() checks it first

// askpassChoose picks the env vault name for a prompt: the jump password when the prompt names a jump
// host (and not the server), else the server password. "" for prompts that aren't for a password.
func askpassChoose(prompt, targetHost string, jumpHosts []string, pwEnv, jumpEnv string) string {
	p := strings.ToLower(prompt)
	if !strings.Contains(p, "password") {
		return ""
	}
	if jumpEnv != "" && jumpEnv != pwEnv && (targetHost == "" || !strings.Contains(p, strings.ToLower(targetHost))) {
		for _, h := range jumpHosts {
			if h != "" && strings.Contains(p, strings.ToLower(h)) {
				return jumpEnv
			}
		}
	}
	return pwEnv
}

// hostOf strips the user and port from user@host:port (ssh destinations and -J entries).
func hostOf(dest string) string {
	dest = strings.TrimPrefix(dest, "ssh://")
	if i := strings.LastIndex(dest, "@"); i >= 0 {
		dest = dest[i+1:]
	}
	if strings.HasPrefix(dest, "[") { // [ipv6]:port
		if i := strings.Index(dest, "]"); i > 0 {
			return dest[1:i]
		}
	}
	if i := strings.LastIndex(dest, ":"); i >= 0 && strings.Count(dest, ":") == 1 {
		dest = dest[:i]
	}
	return dest
}

// sshAskpass answers one prompt from ssh: it writes the answer to stdout, which ssh reads.
func sshAskpass(prompt string) {
	in, out, err := openConsole()
	if err != nil {
		fmt.Fprintf(os.Stderr, "payload: cannot open the terminal to ask %q: %v\n", strings.TrimSpace(prompt), err)
		os.Exit(1)
	}
	defer in.Close()
	defer out.Close()

	jumps := strings.Split(os.Getenv("PAYLOAD_SSH_JUMPS"), ",")
	name := askpassChoose(prompt, os.Getenv("PAYLOAD_SSH_TARGET"), jumps,
		os.Getenv("PAYLOAD_SSH_PW_ENV"), os.Getenv("PAYLOAD_SSH_JUMP_ENV"))
	secret := strings.Contains(strings.ToLower(prompt), "password") || strings.Contains(strings.ToLower(prompt), "passphrase")
	paste := "" // the saved password Ctrl+V inserts at the prompt

	if name != "" {
		login := firstNonEmpty(os.Getenv("PAYLOAD_SSH_LOGIN"), "this login")
		what := "Password for " + login
		if name == os.Getenv("PAYLOAD_SSH_JUMP_ENV") && name != os.Getenv("PAYLOAD_SSH_PW_ENV") {
			what = "Jump host password for " + login
		}
		env, err := loadEnv()
		if err == errLocked {
			fmt.Fprintf(out, "! payload is locked, so the %s wasn't copied - type it\n", strings.ToLower(what[:1])+what[1:])
		} else if v, ok := env[name]; ok && v != "" {
			paste = v
			if err := copyText(v); err == nil {
				fmt.Fprintf(out, "* %s copied to the clipboard - paste it (%s)\n", what, pasteKeys())
			} else {
				fmt.Fprintf(out, "! could not copy {{%s}} to the clipboard (%v) - press Ctrl+V at the prompt to paste it anyway\n", name, err)
			}
		} else {
			fmt.Fprintf(out, "! %s isn't saved (add it with e in payload ssh table) - type it\n", what)
		}
	}
	fmt.Fprint(out, prompt)

	var answer string
	if secret {
		b, err := readSecret(in, out, paste)
		fmt.Fprintln(out)
		if err != nil {
			os.Exit(1)
		}
		answer = b
	} else {
		line, err := bufio.NewReader(in).ReadString('\n')
		if err != nil && line == "" {
			os.Exit(1)
		}
		answer = strings.TrimRight(line, "\r\n")
	}
	fmt.Println(answer)
}

// openConsole opens the terminal directly (ssh owns payload's stdin and stdout here).
func openConsole() (in, out *os.File, err error) {
	if runtime.GOOS == "windows" {
		if in, err = os.OpenFile("CONIN$", os.O_RDWR, 0); err != nil {
			return nil, nil, err
		}
		if out, err = os.OpenFile("CONOUT$", os.O_RDWR, 0); err != nil {
			in.Close()
			return nil, nil, err
		}
		return in, out, nil
	}
	if in, err = os.OpenFile("/dev/tty", os.O_RDWR, 0); err != nil {
		return nil, nil, err
	}
	if out, err = os.OpenFile("/dev/tty", os.O_WRONLY, 0); err != nil {
		in.Close()
		return nil, nil, err
	}
	return in, out, nil
}

// pasteKeys names the keys that paste in this system's terminals.
func pasteKeys() string {
	switch runtime.GOOS {
	case "windows":
		return "right-click or Ctrl+V"
	case "darwin":
		return "Cmd+V or Ctrl+V"
	}
	return "Ctrl+V or Ctrl+Shift+V"
}

// readSecret reads a password from the terminal in raw mode, echoing * for each character so you
// can see a paste arrive. Ctrl+V inserts paste (the saved password payload just copied), so it works
// even where Ctrl+V isn't the terminal's paste key or the clipboard can't be reached. Falls back to
// a plain hidden read when the terminal can't be put in raw mode.
func readSecret(in, out *os.File, paste string) (string, error) {
	state, err := term.MakeRaw(in.Fd())
	if err != nil {
		b, err := term.ReadPassword(in.Fd())
		return string(b), err
	}
	defer term.Restore(in.Fd(), state)
	return readMasked(in, out, paste)
}

var errAborted = errors.New("cancelled")

// readMasked is readSecret's line editor over raw terminal input: Enter ends the line, Backspace
// deletes a character, Ctrl+U clears, Ctrl+C (or Ctrl+D on an empty line) cancels, Ctrl+V inserts
// paste, and escape sequences (arrow keys, bracketed-paste markers) are skipped.
func readMasked(r io.Reader, w io.Writer, paste string) (string, error) {
	br := bufio.NewReader(r)
	var buf []rune
	add := func(s string) {
		for _, c := range s {
			if c < 0x20 || c == utf8.RuneError {
				continue
			}
			buf = append(buf, c)
			fmt.Fprint(w, "*")
		}
	}
	for {
		c, _, err := br.ReadRune()
		if err != nil {
			if err == io.EOF && len(buf) > 0 {
				return string(buf), nil
			}
			return "", err
		}
		switch c {
		case '\r', '\n':
			return string(buf), nil
		case 0x03: // Ctrl+C
			return "", errAborted
		case 0x04: // Ctrl+D
			if len(buf) == 0 {
				return "", io.EOF
			}
		case 0x7f, 0x08: // Backspace
			if len(buf) > 0 {
				buf = buf[:len(buf)-1]
				fmt.Fprint(w, "\b \b")
			}
		case 0x15: // Ctrl+U
			fmt.Fprint(w, strings.Repeat("\b \b", len(buf)))
			buf = buf[:0]
		case 0x16: // Ctrl+V
			add(paste)
		case 0x1b: // escape sequence: ESC [ params final-byte, or ESC O x
			next, _, err := br.ReadRune()
			if err != nil {
				continue
			}
			switch next {
			case '[':
				for {
					b, _, err := br.ReadRune()
					if err != nil || (b >= 0x40 && b <= 0x7e) {
						break
					}
				}
			case 'O':
				_, _, _ = br.ReadRune()
			}
		default:
			add(string(c))
		}
	}
}
