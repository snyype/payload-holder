package main

import (
	"bufio"
	"fmt"
	"os"
	"runtime"
	"strings"

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

	if name != "" {
		login := firstNonEmpty(os.Getenv("PAYLOAD_SSH_LOGIN"), "this login")
		what := "Password for " + login
		if name == os.Getenv("PAYLOAD_SSH_JUMP_ENV") && name != os.Getenv("PAYLOAD_SSH_PW_ENV") {
			what = "Jump host password for " + login
		}
		env, _ := loadEnv()
		if v, ok := env[name]; ok && v != "" {
			if err := copyText(v); err == nil {
				fmt.Fprintf(out, "* %s copied to the clipboard - paste it (right-click or Ctrl+V)\n", what)
			} else {
				fmt.Fprintf(out, "! could not copy {{%s}}: %v\n", name, err)
			}
		} else {
			fmt.Fprintf(out, "! %s isn't saved (add it with e in payload ssh clients) - type it\n", what)
		}
	}
	fmt.Fprint(out, prompt)

	var answer string
	if secret {
		b, err := term.ReadPassword(in.Fd())
		fmt.Fprintln(out)
		if err != nil {
			os.Exit(1)
		}
		answer = string(b)
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
