package main

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// Key bindings: built-in defaults, overridden per action by customization/settings.json.
// A missing file, a missing action or an empty list keeps the default, so deleting the file
// restores the stock keys. Ctrl+C always quits regardless of settings.

type bindings map[string]map[string][]string // section -> action -> keys

// aiReadme is the README.md that "payload setup" writes into the payload folder for AI assistants.
//
//go:embed readme_ai.md
var aiReadme string

func defaultBindings() bindings {
	return bindings{
		"table": {
			"up":             {"up", "k"},
			"down":           {"down", "j"},
			"next_page":      {"right", "l", "pgdown", "n", "space"},
			"prev_page":      {"left", "h", "pgup", "p"},
			"first":          {"home", "g"},
			"last":           {"end", "G"},
			"view":           {"enter"},
			"copy":           {"c"},
			"delete":         {"d", "delete"},
			"confirm_delete": {"y", "Y"},
			"quit":           {"q", "esc"},
		},
		"editor": {
			"save":   {"ctrl+s"},
			"cancel": {"esc"},
		},
	}
}

// binds is the active bindings, loaded once at startup.
var binds = defaultBindings()

// updateCheck turns the daily "new version available" check on or off (settings: "update_check").
var updateCheck = true

type settingsFile struct {
	Help        string   `json:"_help,omitempty"`
	UpdateCheck *bool    `json:"update_check,omitempty"`
	Keybinds    bindings `json:"keybinds"`
}

func settingsPath() string {
	return filepath.Join(storeDir(), "customization", "settings.json")
}

// loadSettings applies the overrides in settings.json, if there is one.
func loadSettings() {
	data, err := os.ReadFile(settingsPath())
	if err != nil {
		return // no file: defaults
	}
	var f settingsFile
	if err := json.Unmarshal(data, &f); err != nil {
		settingsWarn("Ignoring %s: %v", settingsPath(), err)
		return
	}
	if f.UpdateCheck != nil {
		updateCheck = *f.UpdateCheck
	}
	for section, actions := range f.Keybinds {
		for action, ks := range actions {
			if _, known := binds[section][action]; !known {
				settingsWarn("Ignoring unknown key binding %s.%s in %s", section, action, settingsPath())
				continue
			}
			if len(ks) > 0 {
				binds[section][action] = ks
			}
		}
	}
}

// writeDefaultSettings creates settings.json with the default bindings, unless it already exists.
func writeDefaultSettings() (created bool, err error) {
	path := settingsPath()
	if _, err := os.Stat(path); err == nil {
		return false, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	on := true
	data, _ := json.MarshalIndent(settingsFile{
		Help: "Key bindings for payload. Each action takes a list of keys, e.g. \"ctrl+s\", \"enter\", \"esc\", " +
			"\"space\", \"up\", \"pgdown\", \"x\". Remove an action (or this whole file) to use its default keys. " +
			"Ctrl+C always quits. update_check: false stops the daily check for new versions.",
		UpdateCheck: &on,
		Keybinds:    defaultBindings(),
	}, "", "  ")
	return true, os.WriteFile(path, append(data, '\n'), 0o644)
}

// pressed reports whether the pressed key is bound to section.action.
func pressed(k tea.KeyMsg, section, action string) bool {
	got := k.String()
	for _, b := range binds[section][action] {
		if b == "space" {
			b = " "
		}
		if b == got {
			return true
		}
	}
	return false
}

// keyLabel is the first key bound to section.action, as shown in help lines.
func keyLabel(section, action string) string {
	ks := binds[section][action]
	if len(ks) == 0 {
		return "?"
	}
	if pretty, ok := map[string]string{"up": "↑", "down": "↓", "left": "←", "right": "→"}[ks[0]]; ok {
		return pretty
	}
	return ks[0]
}

// setup creates the settings file (if missing) and refreshes the README.md guide for AI assistants.
func setup() {
	dir := storeDir()
	created, err := writeDefaultSettings()
	if err != nil {
		fail("could not write %s: %v", settingsPath(), err)
	}
	if created {
		success("Created %s", settingsPath())
	} else {
		info("Kept your settings: %s", settingsPath())
	}
	readme := filepath.Join(dir, "README.md")
	text := strings.NewReplacer("{{VERSION}}", version, "{{ROOT}}", dir).Replace(aiReadme)
	if err := os.WriteFile(readme, []byte(text), 0o644); err != nil {
		fail("could not write %s: %v", readme, err)
	}
	success("Wrote %s", readme)
}

// settingsCmd prints where the settings file lives and whether it is in use.
func settingsCmd() {
	path := settingsPath()
	if _, err := os.Stat(path); err != nil {
		fmt.Println(path + styleMuted.Render("  (not present — default keys; run \"payload setup\" to create it)"))
		return
	}
	fmt.Println(path)
}

// settingsWarn reports a settings problem on stderr, so piped output (e.g. payload <key> > body.json) stays clean.
func settingsWarn(format string, a ...any) {
	fmt.Fprintln(os.Stderr, styleWarn.Render("! ")+fmt.Sprintf(format, a...))
}
