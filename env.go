package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/charmbracelet/huh"
)

// The env vault: global named values (a base URL, a token) kept in plain text in vault/env.json.
// Payloads refer to them as {{name}}; printing or copying a payload fills them in, while the file
// on disk keeps the placeholder.

// placeholder matches {{name}} with optional spaces inside the braces.
var placeholder = regexp.MustCompile(`\{\{\s*([^{}\s]+)\s*\}\}`)

// envSubcommands can't be used as names, since "payload env <name>" would run the subcommand.
var envSubcommands = map[string]bool{"set": true, "list": true, "drop": true, "table": true}

func envPath() string {
	return filepath.Join(storeDir(), "vault", "env.json")
}

// validEnvName is validKey plus no spaces or braces, so the name fits inside {{ }}.
func validEnvName(n string) bool {
	return validKey(n) && !strings.ContainsAny(n, "{} \t\r\n")
}

// loadEnv reads the vault; a missing file is an empty vault.
func loadEnv() (map[string]string, error) {
	vals := map[string]string{}
	data, err := os.ReadFile(envPath())
	if os.IsNotExist(err) {
		return vals, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, &vals); err != nil {
		return nil, fmt.Errorf("%s: %v", envPath(), err)
	}
	return vals, nil
}

// mustLoadEnv is loadEnv for commands that can't go on without the vault.
func mustLoadEnv() map[string]string {
	vals, err := loadEnv()
	if err != nil {
		fail("cannot read env vault: %v", err)
	}
	return vals
}

func saveEnv(vals map[string]string) error {
	if err := os.MkdirAll(filepath.Dir(envPath()), 0o755); err != nil {
		return err
	}
	data, _ := json.MarshalIndent(vals, "", "  ") // map keys come out sorted
	return os.WriteFile(envPath(), append(data, '\n'), 0o600)
}

func envNames(vals map[string]string) []string {
	names := make([]string, 0, len(vals))
	for n := range vals {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// expandEnv fills {{name}} placeholders from the vault and returns the names that aren't set
// (each once); their placeholders are left as written.
func expandEnv(data []byte) ([]byte, []string) {
	if !placeholder.Match(data) {
		return data, nil
	}
	vals, err := loadEnv()
	if err != nil {
		settingsWarn("Placeholders not filled in: cannot read env vault: %v", err)
		return data, nil
	}
	var missing []string
	seen := map[string]bool{}
	out := placeholder.ReplaceAllFunc(data, func(m []byte) []byte {
		name := string(placeholder.FindSubmatch(m)[1])
		if v, ok := vals[name]; ok {
			return []byte(v)
		}
		if !seen[name] {
			seen[name] = true
			missing = append(missing, name)
		}
		return m
	})
	return out, missing
}

// payloadOut is a key's JSON with placeholders filled in; unset names are reported on stderr.
func payloadOut(dir, key string) []byte {
	data, missing := expandEnv(readPayload(dir, key))
	warnMissing(missing)
	return data
}

func warnMissing(missing []string) {
	for _, n := range missing {
		settingsWarn("{{%s}} is not set — run: payload env set %s", n, n)
	}
}

func envUsage() {
	fail("usage: payload env <name> | set <name> [value] | list | drop <name>... | table [page]")
}

func envCmd(args []string) {
	if len(args) == 0 {
		envList()
		return
	}
	switch args[0] {
	case "set":
		envSet(args[1:])
	case "list":
		envList()
	case "drop":
		envDrop(args[1:])
	case "table":
		envTableCmd(args[1:])
	default:
		if len(args) > 1 {
			envUsage()
		}
		envGet(args[0])
	}
}

func envGet(name string) {
	v, ok := mustLoadEnv()[name]
	if !ok {
		fail("%q is not set — run \"payload env list\" to see names, or \"payload env set %s <value>\"", name, name)
	}
	if stdoutTTY {
		fmt.Println(v)
		return
	}
	fmt.Print(v) // no newline, so $(payload env name) and pipes get the exact value
}

func envSet(args []string) {
	if len(args) == 0 || len(args) > 2 {
		fail("usage: payload env set <name> [value]  (quote a value that has spaces)")
	}
	name := args[0]
	if !validEnvName(name) {
		fail(`invalid name %q (not empty, ".", "..", no spaces, and none of / \ : * ? " < > | { })`, name)
	}
	if envSubcommands[name] {
		fail("%q is a payload env command, pick another name", name)
	}
	var value string
	if len(args) == 2 {
		value = args[1]
	} else {
		value = readEnvValue(name)
	}
	if value == "" {
		fail("empty value — nothing saved")
	}

	vals := mustLoadEnv()
	_, existed := vals[name]
	vals[name] = value
	if err := saveEnv(vals); err != nil {
		fail("write error: %v", err)
	}
	verb := "Set"
	if existed {
		verb = "Updated"
	}
	success("%s %s %s", verb, styleTitle.Render(name), styleMuted.Render("→ "+envPath()))
}

// readEnvValue asks for a value with hidden input on a console, otherwise reads all of stdin.
func readEnvValue(name string) string {
	if interactive {
		var v string
		runForm(huh.NewInput().
			Title("Value for " + name).
			Description("Hidden while you type · stored in plain text in " + envPath()).
			EchoMode(huh.EchoModePassword).
			Value(&v))
		return strings.TrimSpace(v)
	}
	data, err := io.ReadAll(in)
	if err != nil {
		fail("read error: %v", err)
	}
	return strings.TrimSpace(strings.TrimPrefix(string(data), string(rune(0xFEFF))))
}

func envList() {
	names := envNames(mustLoadEnv())
	if !stdoutTTY {
		for _, n := range names {
			fmt.Println(n)
		}
		return
	}
	if len(names) == 0 {
		info("No env values yet. Run:  payload env set <name> <value>")
		return
	}
	fmt.Println(styleEnvBadge.Render("env") + "  " + styleMuted.Render(fmt.Sprintf("%d set in %s", len(names), envPath())))
	for _, n := range names {
		fmt.Printf("  %s %s\n", styleErr.Render("•"), n)
	}
}

func envDrop(names []string) {
	if len(names) == 0 {
		fail("usage: payload env drop <name>...")
	}
	vals := mustLoadEnv()
	var targets []string
	seen := map[string]bool{}
	for _, n := range names {
		if seen[n] {
			continue
		}
		seen[n] = true
		if _, ok := vals[n]; !ok {
			fail("%q is not set — run \"payload env list\" to see names", n)
		}
		targets = append(targets, n)
	}
	if !confirm(fmt.Sprintf("Delete %d env value(s)?", len(targets)), strings.Join(targets, ", "), "Delete") {
		info("Aborted.")
		return
	}
	for _, n := range targets {
		delete(vals, n)
	}
	if err := saveEnv(vals); err != nil {
		fail("write error: %v", err)
	}
	for _, n := range targets {
		success("Deleted %s", styleTitle.Render(n))
	}
}
