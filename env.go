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

// The vault maps a name to a value, or to a group of named values — e.g. an ssh login with a jump
// host keeps both passwords under one name:
//
//	{"localUrl": "http://localhost:8000", "gibl_onboarding": {"server": "…", "jump": "…"}}
//
// Everything reads it flat: a group's values are "<name>.<key>" (gibl_onboarding.jump), so
// {{gibl_onboarding.jump}}, payload env gibl_onboarding.jump and the env table work as usual.

// loadEnv reads the vault, flattened; a missing file is an empty vault.
func loadEnv() (map[string]string, error) {
	vals, _, err := loadEnvGroups()
	return vals, err
}

// loadEnvGroups is loadEnv plus the set of names that are groups.
func loadEnvGroups() (map[string]string, map[string]bool, error) {
	vals, groups := map[string]string{}, map[string]bool{}
	data, err := os.ReadFile(envPath())
	if os.IsNotExist(err) {
		return vals, groups, nil
	}
	if err != nil {
		return nil, nil, err
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, nil, fmt.Errorf("%s: %v", envPath(), err)
	}
	for name, r := range raw {
		var v string
		if json.Unmarshal(r, &v) == nil {
			vals[name] = v
			continue
		}
		var g map[string]string
		if err := json.Unmarshal(r, &g); err != nil {
			return nil, nil, fmt.Errorf("%s: %q must be a string or an object of strings", envPath(), name)
		}
		groups[name] = true
		for k, v := range g {
			vals[name+"."+k] = v
		}
	}
	return vals, groups, nil
}

// mustLoadEnv is loadEnv for commands that can't go on without the vault.
func mustLoadEnv() map[string]string {
	vals, err := loadEnv()
	if err != nil {
		fail("cannot read env vault: %v", err)
	}
	return vals
}

// saveEnv writes the flat values back: "<group>.<key>" goes inside its group when the group already
// exists in the vault or is named in newGroups; everything else is a plain value. A plain value
// with the same name as a group is dropped in favour of the group.
func saveEnv(vals map[string]string, newGroups ...string) error {
	_, groups, err := loadEnvGroups()
	if err != nil {
		groups = map[string]bool{}
	}
	for _, g := range newGroups {
		groups[g] = true
	}
	out := map[string]any{}
	nested := map[string]map[string]string{}
	for name, v := range vals {
		if g, key, ok := strings.Cut(name, "."); ok && groups[g] && key != "" {
			if nested[g] == nil {
				nested[g] = map[string]string{}
			}
			nested[g][key] = v
			continue
		}
		out[name] = v
	}
	for g, m := range nested {
		out[g] = m // a group without values disappears
	}
	if err := os.MkdirAll(filepath.Dir(envPath()), 0o755); err != nil {
		return err
	}
	data, _ := json.MarshalIndent(out, "", "  ") // map keys come out sorted
	return os.WriteFile(envPath(), append(data, '\n'), 0o600)
}

// envGroupKeys lists the flat names inside group name ("" if name isn't a group).
func envGroupKeys(vals map[string]string, name string) []string {
	var keys []string
	for k := range vals {
		if strings.HasPrefix(k, name+".") {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	return keys
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
	vals := mustLoadEnv()
	v, ok := vals[name]
	if keys := envGroupKeys(vals, name); !ok && len(keys) > 0 {
		fail("%q holds %d values — ask for one: %s", name, len(keys), strings.Join(keys, ", "))
	}
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
		if _, ok := vals[n]; ok {
			targets = append(targets, n)
			continue
		}
		if keys := envGroupKeys(vals, n); len(keys) > 0 {
			targets = append(targets, keys...) // the whole group, e.g. both passwords of an ssh login
			continue
		}
		fail("%q is not set — run \"payload env list\" to see names", n)
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
