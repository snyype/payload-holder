package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
)

const windowsDefaultStore = `C:\payload`

// version is set at release build time via -ldflags "-X main.version=vX.Y.Z".
var version = "dev"

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
	dir := storeDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "cannot create store dir:", err)
		os.Exit(1)
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
	case "list":
		for _, k := range keys(dir) {
			fmt.Println(k)
		}
	case "path":
		fmt.Println(dir)
	case "version", "--version", "-v":
		fmt.Println(version)
	case "-h", "--help", "help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", cmd)
		usage()
		os.Exit(1)
	}
}

func usage() {
	fmt.Println(`payload — save & retrieve named JSON payloads

  payload          list saved keys, pick one, print its JSON
  payload store    enter a key name, then paste JSON to save
  payload update   pick an existing key, then paste new JSON to replace it
  payload list     print key names only
  payload path     print the storage folder
  payload version  print the installed version

Storage: one <key>.json file per key. Default C:\payload (override with PAYLOAD_STORE).`)
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

// readLine reads one line and strips a leading UTF-8 BOM (PowerShell 5.1 adds one when piping).
func readLine(r *bufio.Reader) string {
	line, _ := r.ReadString('\n')
	return strings.TrimSpace(strings.TrimPrefix(line, string(rune(0xFEFF))))
}

// selectKey shows the numbered key menu and returns the chosen key ("" if none are saved).
func selectKey(dir string, reader *bufio.Reader) string {
	ks := keys(dir)
	if len(ks) == 0 {
		fmt.Println("No saved payloads yet. Run:  payload store")
		return ""
	}
	for i, k := range ks {
		fmt.Printf("%d) %s\n", i+1, k)
	}
	fmt.Print("Select a key by number: ")

	n, err := strconv.Atoi(readLine(reader))
	if err != nil || n < 1 || n > len(ks) {
		fmt.Fprintln(os.Stderr, "invalid selection")
		os.Exit(1)
	}
	return ks[n-1]
}

// printPayload prints a key's JSON under a header line.
func printPayload(dir, key string) {
	data, err := os.ReadFile(filepath.Join(dir, key+".json"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "read error:", err)
		os.Exit(1)
	}
	fmt.Printf("----- %s -----\n", key)
	os.Stdout.Write(data)
	if len(data) > 0 && data[len(data)-1] != '\n' {
		fmt.Println()
	}
}

// readAndSave reads pasted JSON until EOF and writes it to target.
func readAndSave(reader *bufio.Reader, key, target string) {
	fmt.Println("Paste JSON payload, then press Ctrl+Z then Enter (Windows) or Ctrl+D (Unix) to finish:")
	data, err := io.ReadAll(reader)
	if err != nil {
		fmt.Fprintln(os.Stderr, "read error:", err)
		os.Exit(1)
	}
	if strings.TrimSpace(string(data)) == "" {
		fmt.Fprintln(os.Stderr, "no JSON entered — nothing saved")
		os.Exit(1)
	}

	if err := os.WriteFile(target, data, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "write error:", err)
		os.Exit(1)
	}
	fmt.Printf("Saved %q -> %s\n", key, target)
}

func retrieve(dir string) {
	if key := selectKey(dir, bufio.NewReader(os.Stdin)); key != "" {
		printPayload(dir, key)
	}
}

// update lets the user pick an existing key, shows its current JSON, then replaces it.
func update(dir string) {
	reader := bufio.NewReader(os.Stdin)
	key := selectKey(dir, reader)
	if key == "" {
		return
	}
	printPayload(dir, key)
	readAndSave(reader, key, filepath.Join(dir, key+".json"))
}

func store(dir string) {
	reader := bufio.NewReader(os.Stdin)

	fmt.Print("Enter key name: ")
	key := readLine(reader)
	if !validKey(key) {
		fmt.Fprintln(os.Stderr, `invalid key name (no empty, '.', '..', or any of / \ : * ? " < > |)`)
		os.Exit(1)
	}

	target := filepath.Join(dir, key+".json")
	if _, err := os.Stat(target); err == nil {
		fmt.Printf("Key %q exists. Overwrite? [y/N]: ", key)
		ans := strings.ToLower(readLine(reader))
		if ans != "y" && ans != "yes" {
			fmt.Println("Aborted.")
			return
		}
	}

	readAndSave(reader, key, target)
}
