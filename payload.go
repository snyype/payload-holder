package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"github.com/atotto/clipboard"
)

const windowsDefaultStore = `C:\payload`

// version is set at release build time via -ldflags "-X main.version=vX.Y.Z".
var version = "dev"

// in is the one buffered reader over stdin, shared by every plain prompt and the JSON paste.
var in = bufio.NewReader(os.Stdin)

// storeDir decides where payloads live.
func storeDir() string {
	if v := os.Getenv("PAYLOAD_STORE"); v != "" {
		return v
	}
	if runtime.GOOS == "windows" {
		return windowsDefaultStore
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "payload"
	}
	return filepath.Join(home, "payload")
}

// validKey rejects names that aren't safe as a file name.
func validKey(k string) bool {
	k = strings.TrimSpace(k)
	if k == "" || k == "." || k == ".." {
		return false
	}
	return !strings.ContainsAny(k, `/\:*?"<>|`)
}

func main() {
	initTerminal()

	dir := storeDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fail("cannot create store dir: %v", err)
	}

	cmd := ""
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}

	switch cmd {
	case "":
		retrieve(dir)
	case "store":
		store(dir)
	case "update":
		update(dir)
	case "drop":
		drop(dir, os.Args[2:])
	case "copy", "cp":
		copyCmd(dir, os.Args[2:])
	case "list":
		list(dir)
	case "table":
		tableCmd(dir, os.Args[2:])
	case "path":
		fmt.Println(dir)
	case "version", "--version", "-v":
		fmt.Println(version)
	case "-h", "--help", "help":
		usage()
	default:
		// Not a command: treat it as a key name and print that payload directly.
		if validKey(cmd) && exists(dir, cmd) {
			showPayload(cmd, readPayload(dir, cmd), false)
			return
		}
		fail("unknown command or key %q — run \"payload list\" to see saved keys, or \"payload help\"", cmd)
	}
}

func usage() {
	cmds := [][2]string{
		{"payload", "pick a saved key from a menu and show its JSON (key names when piped)"},
		{"payload <key>", "show that key's JSON directly (raw when piped)"},
		{"payload store", "save a new key: name it, then paste JSON"},
		{"payload update", "pick a key, then paste JSON to replace it"},
		{"payload copy [key]", "copy a key's JSON to the clipboard (menu when no key)"},
		{"payload drop", "tick one or more keys to delete"},
		{"payload drop <key>...", "delete the named keys"},
		{"payload list", "list saved keys (names only when piped)"},
		{"payload table [page]", "browse keys in a table, 10 per page"},
		{"payload path", "print the storage folder"},
		{"payload version", "print the installed version"},
	}
	fmt.Println(styleBadge.Render("payload") + "  " + styleMuted.Render("save & retrieve named JSON payloads"))
	fmt.Println()
	for _, c := range cmds {
		fmt.Printf("  %s %s\n", styleCode.Render(fmt.Sprintf("%-22s", c[0])), c[1])
	}
	fmt.Println()
	fmt.Println(styleMuted.Render("  Storage: one <key>.json per key in " + storeDir() + " (override with PAYLOAD_STORE)."))
}

// keys returns the sorted key names (file names without the .json suffix).
func keys(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".json") {
			continue
		}
		out = append(out, strings.TrimSuffix(name, ".json"))
	}
	sort.Strings(out)
	return out
}

func exists(dir, key string) bool {
	_, err := os.Stat(filepath.Join(dir, key+".json"))
	return err == nil
}

func readPayload(dir, key string) []byte {
	data, err := os.ReadFile(filepath.Join(dir, key+".json"))
	if err != nil {
		fail("read error: %v", err)
	}
	return data
}

// savedKeys returns the keys, or prints a hint and returns nil when there are none.
func savedKeys(dir string) []string {
	ks := keys(dir)
	if len(ks) == 0 {
		info("No saved payloads yet. Run:  payload store")
	}
	return ks
}

// readLine reads one line and strips a leading UTF-8 BOM (PowerShell 5.1 adds one when piping).
func readLine() string {
	line, _ := in.ReadString('\n')
	return strings.TrimSpace(strings.TrimPrefix(line, string(rune(0xFEFF))))
}

func prompt(label string) string {
	fmt.Print(styleAccent.Render("? ") + label + " ")
	return readLine()
}

func printMenu(ks []string) {
	for i, k := range ks {
		fmt.Printf("  %s %s\n", styleMuted.Render(fmt.Sprintf("%2d)", i+1)), k)
	}
}

// pickKey returns one chosen key: an arrow-key menu on a console, a numbered prompt otherwise.
func pickKey(title string, ks []string) string {
	if interactive {
		return pickKeyTUI(title, ks)
	}
	printMenu(ks)
	n, err := strconv.Atoi(prompt("Select a key by number:"))
	if err != nil || n < 1 || n > len(ks) {
		fail("invalid selection")
	}
	return ks[n-1]
}

// pickKeys returns every chosen key: a checkbox list on a console, otherwise a prompt that takes
// numbers and ranges separated by spaces or commas ("1 3 5-7"), or "all".
func pickKeys(title string, ks []string) []string {
	if interactive {
		return pickKeysTUI(title, ks)
	}
	printMenu(ks)
	answer := strings.ToLower(prompt("Select keys (e.g. 1 3 5-7, or all):"))
	if answer == "all" {
		return ks
	}
	picked := map[int]bool{}
	for _, f := range strings.FieldsFunc(answer, func(r rune) bool { return r == ' ' || r == ',' }) {
		lo, hi, isRange := strings.Cut(f, "-")
		a, errA := strconv.Atoi(lo)
		b := a
		var errB error
		if isRange {
			b, errB = strconv.Atoi(hi)
		}
		if errA != nil || errB != nil || a < 1 || b > len(ks) || a > b {
			fail("invalid selection %q", f)
		}
		for n := a; n <= b; n++ {
			picked[n] = true
		}
	}
	if len(picked) == 0 {
		fail("nothing selected")
	}
	var out []string
	for i, k := range ks {
		if picked[i+1] {
			out = append(out, k)
		}
	}
	return out
}

// confirm asks a yes/no question that defaults to No.
func confirm(title, description, yes string) bool {
	if interactive {
		return confirmTUI(title, description, yes)
	}
	ans := strings.ToLower(prompt(title + " [y/N]:"))
	return ans == "y" || ans == "yes"
}

func list(dir string) {
	ks := keys(dir)
	if !stdoutTTY {
		for _, k := range ks {
			fmt.Println(k)
		}
		return
	}
	if len(ks) == 0 {
		info("No saved payloads yet. Run:  payload store")
		return
	}
	width := 0
	for _, k := range ks {
		width = max(width, len(k))
	}
	fmt.Println(styleBadge.Render("payloads") + "  " + styleMuted.Render(fmt.Sprintf("%d saved in %s", len(ks), dir)))
	for _, k := range ks {
		meta := ""
		if st, err := os.Stat(filepath.Join(dir, k+".json")); err == nil {
			meta = fmt.Sprintf("%8s   %s", humanSize(int(st.Size())), st.ModTime().Format("2006-01-02 15:04"))
		}
		fmt.Printf("  %s %s  %s\n", styleOK.Render("•"), fmt.Sprintf("%-*s", width, k), styleMuted.Render(meta))
	}
}

func retrieve(dir string) {
	// Output going to a pipe or file (e.g. `payload | grep test`): print key names, no menu.
	if !stdoutTTY {
		for _, k := range keys(dir) {
			fmt.Println(k)
		}
		return
	}
	ks := savedKeys(dir)
	if len(ks) == 0 {
		return
	}
	key := pickKey("Which payload?", ks)
	showPayload(key, readPayload(dir, key), true)
}

func store(dir string) {
	var key string
	if interactive {
		key = askKeyTUI()
	} else {
		key = prompt("Key name:")
		if !validKey(key) {
			fail(`invalid key name (not empty, ".", "..", or any of / \ : * ? " < > |)`)
		}
	}

	if exists(dir, key) && !confirm(fmt.Sprintf("Key %q already exists. Overwrite it?", key), "The current JSON will be replaced.", "Overwrite") {
		info("Aborted.")
		return
	}
	readAndSave(key, filepath.Join(dir, key+".json"), "Saved", "")
}

// update lets the user pick an existing key and edit its JSON in place
// (on a console; otherwise it shows the current JSON and reads the replacement).
func update(dir string) {
	ks := savedKeys(dir)
	if len(ks) == 0 {
		return
	}
	key := pickKey("Which payload do you want to replace?", ks)
	current := readPayload(dir, key)
	if !interactive {
		showPayload(key, current, true)
		fmt.Println()
	}
	readAndSave(key, filepath.Join(dir, key+".json"), "Updated", string(current))
}

// readAndSave gets the JSON — from the editor on a console, else pasted until EOF — and writes it to target.
func readAndSave(key, target, verb, initial string) {
	var data []byte
	if interactive {
		data = []byte(editJSONTUI(key, initial))
	} else {
		eof := "Ctrl+D"
		if runtime.GOOS == "windows" {
			eof = "Ctrl+Z then Enter"
		}
		fmt.Println(styleAccent.Render("? ") + "Paste the JSON for " + styleTitle.Render(key) +
			styleMuted.Render(" — finish with "+eof))
		var err error
		data, err = io.ReadAll(in)
		if err != nil {
			fail("read error: %v", err)
		}
	}
	if strings.TrimSpace(string(data)) == "" {
		fail("no JSON entered — nothing saved")
	}

	if err := os.WriteFile(target, data, 0o644); err != nil {
		fail("write error: %v", err)
	}
	success("%s %s → %s", verb, styleTitle.Render(key), styleMuted.Render(target))
	if !json.Valid(data) {
		warn("Saved as-is, but it is not valid JSON.")
	}
}

// copyCmd puts the named key's JSON on the clipboard, or the key picked from a menu when none is named.
func copyCmd(dir string, names []string) {
	var key string
	switch len(names) {
	case 0:
		if !interactive {
			fail("usage: payload copy <key>")
		}
		ks := savedKeys(dir)
		if len(ks) == 0 {
			return
		}
		key = pickKey("Which payload do you want to copy?", ks)
	case 1:
		key = names[0]
		if !validKey(key) {
			fail("invalid key name %q", key)
		}
		if !exists(dir, key) {
			fail("no saved key %q — run \"payload list\" to see saved keys", key)
		}
	default:
		fail("copy takes one key, got %d", len(names))
	}

	data := readPayload(dir, key)
	if err := clipboard.WriteAll(strings.TrimSpace(string(data))); err != nil {
		fail("could not copy to clipboard: %v", err)
	}
	success("Copied %s to the clipboard %s", styleTitle.Render(key), styleMuted.Render("("+humanSize(len(data))+")"))
}

// drop deletes the named keys, or the keys picked from a list when none are named,
// after a confirmation.
func drop(dir string, names []string) {
	var targets []string
	if len(names) == 0 {
		ks := savedKeys(dir)
		if len(ks) == 0 {
			return
		}
		targets = pickKeys("Which payloads do you want to delete?", ks)
	} else {
		seen := map[string]bool{}
		for _, k := range names {
			if seen[k] {
				continue
			}
			seen[k] = true
			if !validKey(k) {
				fail("invalid key name %q", k)
			}
			if !exists(dir, k) {
				fail("no saved key %q — run \"payload list\" to see saved keys", k)
			}
			targets = append(targets, k)
		}
	}

	title := fmt.Sprintf("Delete %d payload(s)?", len(targets))
	if !confirm(title, strings.Join(targets, ", "), "Delete") {
		info("Aborted.")
		return
	}
	failed := false
	for _, k := range targets {
		if err := os.Remove(filepath.Join(dir, k+".json")); err != nil {
			fmt.Fprintln(os.Stderr, styleErr.Render("✗ ")+fmt.Sprintf("could not delete %q: %v", k, err))
			failed = true
			continue
		}
		success("Deleted %s", styleTitle.Render(k))
	}
	if failed {
		os.Exit(1)
	}
}
