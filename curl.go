package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Saved curl requests: named curl commands kept in vault/curl.json. "payload curl <name> [flags]"
// runs one with the flags appended (so -vvk or -X PUT override what is stored), filling in
// {{name}} placeholders from the env vault first.

// curlSubcommands can't be used as names, since "payload curl <name>" would run the subcommand.
var curlSubcommands = map[string]bool{"set": true, "list": true, "drop": true, "table": true, "show": true, "path": true, "install": true}

func curlPath() string {
	return filepath.Join(storeDir(), "vault", "curl.json")
}

// validCurlName is validEnvName that also can't start with "-", so it never looks like a curl flag.
func validCurlName(n string) bool {
	return validEnvName(n) && !strings.HasPrefix(n, "-")
}

// loadCurls reads the saved requests; a missing file means none.
func loadCurls() (map[string]string, error) {
	vals := map[string]string{}
	data, err := os.ReadFile(curlPath())
	if os.IsNotExist(err) {
		return vals, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, &vals); err != nil {
		return nil, fmt.Errorf("%s: %v", curlPath(), err)
	}
	return vals, nil
}

func mustLoadCurls() map[string]string {
	vals, err := loadCurls()
	if err != nil {
		fail("cannot read saved curl requests: %v", err)
	}
	return vals
}

func saveCurls(vals map[string]string) error {
	if err := os.MkdirAll(filepath.Dir(curlPath()), 0o755); err != nil {
		return err
	}
	data, _ := json.MarshalIndent(vals, "", "  ")
	return os.WriteFile(curlPath(), append(data, '\n'), 0o600)
}

func curlUsage() {
	fail("usage: payload curl <name> [curl flags...] | set <name> [command] | show <name> | list | drop <name>... | table [page] | path | install")
}

func curlCmd(args []string) {
	if len(args) == 0 {
		curlList()
		return
	}
	switch args[0] {
	case "set":
		curlSet(args[1:])
	case "show":
		if len(args) != 2 {
			fail("usage: payload curl show <name>")
		}
		curlShow(args[1])
	case "list":
		curlList()
	case "path":
		curlPathCmd()
	case "install":
		curlInstallCmd()
	case "drop":
		curlDrop(args[1:])
	case "table":
		curlTableCmd(args[1:])
	default:
		if strings.HasPrefix(args[0], "-") {
			curlUsage()
		}
		curlRun(args[0], args[1:])
	}
}

// curlArgs splits a saved command into curl's arguments: shell-style words, without a leading
// "curl", each with {{name}} placeholders filled in from the env vault. It also returns the
// placeholder names it filled and the ones that aren't set (each once).
func curlArgs(command string) (args, filled, missing []string, err error) {
	words, err := splitCommand(command)
	if err != nil {
		return nil, nil, nil, err
	}
	if w := strings.ToLower(words[0]); w == "curl" || w == "curl.exe" {
		words = words[1:]
	}
	var vals map[string]string
	seen := map[string]bool{}
	for i, w := range words {
		words[i] = placeholder.ReplaceAllStringFunc(w, func(m string) string {
			if vals == nil {
				vals = mustLoadEnv()
			}
			name := placeholder.FindStringSubmatch(m)[1]
			v, ok := vals[name]
			if !seen[name] {
				seen[name] = true
				if ok {
					filled = append(filled, name)
				} else {
					missing = append(missing, name)
				}
			}
			if !ok {
				return m
			}
			return v
		})
	}
	return words, filled, missing, nil
}

func lookupCurl(name string) string {
	command, ok := mustLoadCurls()[name]
	if !ok {
		fail("no saved curl request %q — run \"payload curl list\" to see names, or \"payload curl set %s\"", name, name)
	}
	return command
}

// curlStep reports one step of a run on stderr, so the response on stdout can still be piped.
func curlStep(format string, a ...any) {
	fmt.Fprintln(os.Stderr, styleCurl.Render("● ")+fmt.Sprintf(format, a...))
}

// curlRun runs a saved request with extra flags appended, reporting each step, and exits with
// curl's exit status.
func curlRun(name string, extra []string) {
	command := lookupCurl(name)
	curlStep("Loaded %s from %s", styleTitle.Render(name), styleMuted.Render(curlPath()))
	args, filled, missing, err := curlArgs(command)
	if err != nil {
		fail("cannot parse saved curl request %q: %v", name, err)
	}
	curlStep("Parsed %d argument(s)", len(args))
	if len(filled) > 0 {
		curlStep("Filled in %s from the env vault", braceList(filled))
	}
	if len(missing) > 0 {
		settingsWarn("Not set, left as written: %s — run: payload env set <name> <value>", braceList(missing))
	}
	if len(extra) > 0 {
		curlStep("Added flags: %s", styleCode.Render(quoteWords(extra)))
		args = append(args, extra...)
	}
	bin, source, err := findCurl()
	if err != nil {
		fail("%v", err)
	}
	curlStep("Running %s %s", styleMuted.Render(bin), styleMuted.Render("("+source+")"))
	shown := []string{"curl"}
	for _, a := range args {
		shown = append(shown, shortArg(a))
	}
	for _, line := range strings.Split(formatCurl(shown), "\n") {
		fmt.Fprintln(os.Stderr, "  "+styleCode.Render(line))
	}
	if stderrTTY() {
		fmt.Fprintln(os.Stderr, styleRule.Render(strings.Repeat("─", 40)))
	}

	args, moved, cleanup, err := fitCommandLine(args)
	if err != nil {
		fail("%v", err)
	}
	if len(moved) > 0 {
		curlStep("Command line too long to start curl — passing %s via temp config files (-K), removed afterwards",
			strings.Join(moved, ", "))
	}

	started := time.Now()
	code := execCurl(bin, args)
	cleanup()
	took := time.Since(started).Round(time.Millisecond)
	if stdoutTTY {
		fmt.Println() // the response body often has no trailing newline
	}
	if code != 0 {
		msg := fmt.Sprintf("curl exited with code %d after %s", code, took)
		if why, ok := curlExitReasons[code]; ok {
			msg += " (" + why + ")"
		}
		fmt.Fprintln(os.Stderr, styleErr.Render("✗ ")+msg)
		os.Exit(code)
	}
	curlStep("%s in %s", styleOK.Render("Done"), took)
}

// curlExitReasons explains the curl exit codes people run into most.
var curlExitReasons = map[int]string{
	1:  "unsupported protocol",
	2:  "failed to initialize",
	3:  "malformed URL",
	5:  "couldn't resolve proxy",
	6:  "couldn't resolve host",
	7:  "couldn't connect to server",
	22: "HTTP error status, from --fail",
	23: "write error",
	26: "read error",
	28: "timed out",
	35: "SSL/TLS handshake failed",
	47: "too many redirects",
	52: "empty reply from server",
	56: "failure receiving network data",
	60: "SSL certificate problem — -k skips the check",
}

// shortArg keeps a long argument (a big --data body) from flooding the step output: the start of
// it on one line, then its size.
func shortArg(a string) string {
	if len(a) <= 300 {
		return a
	}
	return previewValue(a, 120) + " (" + humanSize(len(a)) + ")"
}

func braceList(names []string) string {
	return styleTitle.Render("{{" + strings.Join(names, "}}, {{") + "}}")
}

// quoteWords joins arguments into one line, quoting the ones a shell would split.
func quoteWords(words []string) string {
	q := make([]string, len(words))
	for i, w := range words {
		q[i] = shellQuote(w)
	}
	return strings.Join(q, " ")
}

// execCurl runs curl attached to this terminal and returns its exit status.
func execCurl(bin string, args []string) int {
	c := exec.Command(bin, args...)
	c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
	err := c.Run()
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	if err != nil {
		fail("could not run curl: %v", err)
	}
	return 0
}

// curlShow prints the saved command as stored, placeholders and all.
func curlShow(name string) {
	command := lookupCurl(name)
	if !stdoutTTY {
		fmt.Println(command)
		return
	}
	fmt.Println(styleCurlBadge.Render("curl") + "  " + styleTitle.Render(name))
	fmt.Println(command)
}

func curlSet(args []string) {
	if len(args) == 0 {
		fail("usage: payload curl set <name> [command]  (leave the command out to paste it)")
	}
	name := args[0]
	checkCurlName(name)
	var command string
	switch len(args) {
	case 1:
		var ok bool
		if command, ok = readCurlCommand(name, mustLoadCurls()[name]); !ok {
			fmt.Println(styleMuted.Render("Cancelled."))
			os.Exit(130)
		}
	case 2:
		command = args[1]
	default:
		// Already split by the shell: quote each word back so the saved command splits the same way.
		command = quoteWords(args[1:])
	}
	if err := saveCurlCommand(name, command); err != nil {
		fail("%v", err)
	}
}

// saveCurlCommand repairs a command (see fixCurlCommand), saves it and reports what it changed.
func saveCurlCommand(name, command string) error {
	fixed, fixes, warnings, err := fixCurlCommand(command)
	if err != nil {
		return fmt.Errorf("cannot save %s: %v", name, err)
	}
	existed, err := storeCurl(name, fixed)
	if err != nil {
		return fmt.Errorf("write error: %v", err)
	}
	for _, f := range fixes {
		fmt.Println(styleCurl.Render("● ") + "Fixed: " + f)
	}
	verb := "Saved"
	if existed {
		verb = "Updated"
	}
	success("%s %s %s", verb, styleTitle.Render(name), styleMuted.Render("→ "+curlPath()))
	for _, w := range warnings {
		warn("%s", w)
	}
	return nil
}

func checkCurlName(name string) {
	if err := curlNameError(name); err != nil {
		fail("%v", err)
	}
}

func curlNameError(name string) error {
	if !validCurlName(name) {
		return fmt.Errorf(`invalid name %q (not empty, ".", "..", not starting with "-", no spaces, and none of / \ : * ? " < > | { })`, name)
	}
	if curlSubcommands[name] {
		return fmt.Errorf("%q is a payload curl command, pick another name", name)
	}
	return nil
}

// storeCurl saves one request and reports whether it replaced an existing one.
func storeCurl(name, command string) (bool, error) {
	vals, err := loadCurls()
	if err != nil {
		return false, err
	}
	_, existed := vals[name]
	vals[name] = command
	return existed, saveCurls(vals)
}

// readCurlCommand opens the editor on a console, otherwise reads the pasted command until EOF.
// It reports false when the user cancels.
func readCurlCommand(name, initial string) (string, bool) {
	if interactive {
		return editTextTUI("curl · "+name, styleCurlBadge, initial,
			"Paste a curl command — multi-line with \\ continuations is fine, {{name}} uses env vault values",
			curlStatus)
	}
	eof := "Ctrl+D"
	if runtime.GOOS == "windows" {
		eof = "Ctrl+Z then Enter"
	}
	fmt.Fprintln(os.Stderr, styleAccent.Render("? ")+"Paste the curl command for "+styleTitle.Render(name)+
		styleMuted.Render(" — finish with "+eof))
	data, err := io.ReadAll(in)
	if err != nil {
		fail("read error: %v", err)
	}
	return strings.TrimPrefix(string(data), string(rune(0xFEFF))), true
}

// curlStatus is the editor's status line: whether the command parses, and what it will send.
func curlStatus(val string) string {
	fixed, fixes, warnings, err := fixCurlCommand(val)
	if err != nil {
		return styleWarn.Render("✗ " + err.Error())
	}
	words, _ := splitCommand(fixed)
	method, url := describeCurl(words)
	s := styleOK.Render("✓ "+method) + " " + previewValue(url, 50)
	if len(fixes) > 0 {
		s += styleMuted.Render(fmt.Sprintf(" · %d fix(es) on save", len(fixes)))
	}
	if len(warnings) > 0 {
		s += " " + styleWarn.Render("! "+warnings[0])
	}
	return s
}

func curlList() {
	names := envNames(mustLoadCurls())
	if !stdoutTTY {
		for _, n := range names {
			fmt.Println(n)
		}
		return
	}
	if len(names) == 0 {
		info("No saved curl requests yet. Run:  payload curl set <name> \"curl ...\"")
		return
	}
	fmt.Println(styleCurlBadge.Render("curl") + "  " + styleMuted.Render(fmt.Sprintf("%d saved in %s", len(names), curlPath())))
	for _, n := range names {
		fmt.Printf("  %s %s\n", styleCurl.Render("•"), n)
	}
}

func curlDrop(names []string) {
	if len(names) == 0 {
		fail("usage: payload curl drop <name>...")
	}
	vals := mustLoadCurls()
	var targets []string
	seen := map[string]bool{}
	for _, n := range names {
		if seen[n] {
			continue
		}
		seen[n] = true
		if _, ok := vals[n]; !ok {
			fail("no saved curl request %q — run \"payload curl list\" to see names", n)
		}
		targets = append(targets, n)
	}
	if !confirm(fmt.Sprintf("Delete %d curl request(s)?", len(targets)), strings.Join(targets, ", "), "Delete") {
		info("Aborted.")
		return
	}
	for _, n := range targets {
		delete(vals, n)
	}
	if err := saveCurls(vals); err != nil {
		fail("write error: %v", err)
	}
	for _, n := range targets {
		success("Deleted %s", styleTitle.Render(n))
	}
}

// --- command splitting ----------------------------------------------------------------------

// splitCommand splits a command line into words the way a shell would. It understands POSIX
// quoting ('...', "...", backslash escapes, backslash-newline continuations) and PowerShell's
// backtick-newline continuation. A command copied for cmd.exe (with ^ line continuations, as
// browsers' "Copy as cURL (cmd)" produces) is read the way cmd.exe and curl.exe would.
func splitCommand(s string) ([]string, error) {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	if strings.Contains(s, "^\n") {
		return splitCmdExe(s)
	}
	var (
		words   []string
		cur     strings.Builder
		inWord  bool
		quote   rune // 0, '\'' or '"'
		escaped bool
	)
	for _, r := range s {
		switch {
		case escaped:
			escaped = false
			if r == '\n' {
				continue // line continuation
			}
			if quote == '"' && !strings.ContainsRune("\"\\$`", r) {
				cur.WriteRune('\\') // inside "...", backslash only escapes " \ $ `
			}
			cur.WriteRune(r)
			inWord = true
		case quote == '\'':
			if r == '\'' {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case r == '\\':
			escaped = true
		case quote == '"':
			if r == '"' {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case r == '\'' || r == '"':
			quote = r
			inWord = true
		case r == '`':
			escaped = true // PowerShell continuation; `x outside quotes is rare in a curl command
		case r == ' ' || r == '\t' || r == '\n':
			if inWord {
				words = append(words, cur.String())
				cur.Reset()
				inWord = false
			}
		default:
			cur.WriteRune(r)
			inWord = true
		}
	}
	if quote != 0 {
		return nil, fmt.Errorf("unterminated %c quote", quote)
	}
	return finishWords(words, cur.String(), inWord)
}

// splitCmdExe reads a cmd.exe command line: first cmd's ^ escapes are removed, then the words are
// split the way a Windows program parses its arguments ("..." quoting, \" for a literal quote).
func splitCmdExe(s string) ([]string, error) {
	var plain []rune
	caret := false
	for _, r := range s {
		switch {
		case caret:
			caret = false
			if r != '\n' {
				plain = append(plain, r)
			}
		case r == '^':
			caret = true
		default:
			plain = append(plain, r)
		}
	}
	var (
		words  []string
		cur    strings.Builder
		inWord bool
		quoted bool
	)
	for i := 0; i < len(plain); i++ {
		r := plain[i]
		switch {
		case r == '\\':
			n := 0
			for i < len(plain) && plain[i] == '\\' {
				n++
				i++
			}
			if i < len(plain) && plain[i] == '"' {
				// 2n backslashes + " → n backslashes and a quote toggle; 2n+1 → n and a literal "
				cur.WriteString(strings.Repeat(`\`, n/2))
				if n%2 == 1 {
					cur.WriteRune('"')
				} else {
					quoted = !quoted
				}
			} else {
				cur.WriteString(strings.Repeat(`\`, n))
				i--
			}
			inWord = true
		case r == '"':
			quoted = !quoted
			inWord = true
		case !quoted && (r == ' ' || r == '\t' || r == '\n'):
			if inWord {
				words = append(words, cur.String())
				cur.Reset()
				inWord = false
			}
		default:
			cur.WriteRune(r)
			inWord = true
		}
	}
	if quoted {
		return nil, fmt.Errorf(`unterminated " quote`)
	}
	return finishWords(words, cur.String(), inWord)
}

func finishWords(words []string, last string, inWord bool) ([]string, error) {
	if inWord {
		words = append(words, last)
	}
	if len(words) == 0 {
		return nil, fmt.Errorf("empty command")
	}
	return words, nil
}

// shellQuote quotes a word so splitCommand gives it back unchanged.
func shellQuote(w string) string {
	if w != "" && !strings.ContainsAny(w, " \t\r\n'\"\\`$^&|;<>()*?!#~{}[]") {
		return w
	}
	return "'" + strings.ReplaceAll(w, "'", `'\''`) + "'"
}
