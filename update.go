package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/mattn/go-isatty"
)

const checkInterval = 24 * time.Hour

// repoURL is the GitHub repository releases are fetched from (a variable so tests can point it elsewhere).
var repoURL = "https://github.com/snyype/payload-holder"

// updateCache remembers the last release check, so GitHub is asked at most once a day.
type updateCache struct {
	Checked time.Time `json:"checked"`
	Latest  string    `json:"latest"`
}

func updateCachePath() string {
	return filepath.Join(storeDir(), ".cache", "update-check.json")
}

func readUpdateCache() updateCache {
	var c updateCache
	if data, err := os.ReadFile(updateCachePath()); err == nil {
		_ = json.Unmarshal(data, &c)
	}
	return c
}

func writeUpdateCache(c updateCache) {
	path := updateCachePath()
	if os.MkdirAll(filepath.Dir(path), 0o755) != nil {
		return
	}
	data, _ := json.Marshal(c)
	_ = os.WriteFile(path, data, 0o644)
}

// latestRelease asks GitHub for the newest release tag. github.com/<repo>/releases/latest redirects to
// .../releases/tag/<tag>, so one HEAD request without following the redirect is enough (and is not
// subject to the API rate limit). Nothing is sent besides the request itself.
func latestRelease(timeout time.Duration) (string, error) {
	client := &http.Client{
		Timeout:       timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	req, err := http.NewRequest(http.MethodHead, repoURL+"/releases/latest", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "payload/"+version)
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	resp.Body.Close()
	loc := resp.Header.Get("Location")
	if i := strings.LastIndex(loc, "/tag/"); i >= 0 {
		return loc[i+len("/tag/"):], nil
	}
	return "", fmt.Errorf("unexpected reply from GitHub (%s)", resp.Status)
}

// checkFailure describes a failed release check: "no internet connection" when GitHub could not be
// reached at all (DNS, connect or timeout errors), otherwise the error itself.
func checkFailure(err error) string {
	var netErr net.Error
	var opErr *net.OpError
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) || errors.As(err, &opErr) || (errors.As(err, &netErr) && netErr.Timeout()) {
		return "version check failed (no internet connection)"
	}
	return "version check failed (" + err.Error() + ")"
}

// newer reports whether release tag a is newer than b (both like v1.2.3).
func newer(a, b string) bool {
	pa, oka := parseVersion(a)
	pb, okb := parseVersion(b)
	if !oka || !okb {
		return false
	}
	for i := range pa {
		if pa[i] != pb[i] {
			return pa[i] > pb[i]
		}
	}
	return false
}

func parseVersion(v string) ([3]int, bool) {
	var out [3]int
	parts := strings.SplitN(strings.TrimPrefix(v, "v"), ".", 3)
	if len(parts) != 3 {
		return out, false
	}
	for i, p := range parts {
		p, _, _ = strings.Cut(p, "-")
		n, err := strconv.Atoi(p)
		if err != nil {
			return out, false
		}
		out[i] = n
	}
	return out, true
}

// startUpdateCheck runs the daily release check in the background while the command works.
// The returned func, called when the command is done, prints a notice if a newer release exists.
func startUpdateCheck(cmd string) func() {
	if !updateCheckEnabled() || version == "dev" || cmd == "doctor" || cmd == holdArg || !stderrTTY() {
		return func() {}
	}
	cache := readUpdateCache()
	type result struct {
		latest string
		err    error
	}
	var fresh chan result
	if time.Since(cache.Checked) > checkInterval {
		fresh = make(chan result, 1)
		go func() {
			latest, err := latestRelease(3 * time.Second)
			if err != nil {
				latest = cache.Latest // keep what we knew; try again tomorrow
			}
			writeUpdateCache(updateCache{Checked: time.Now(), Latest: latest})
			fresh <- result{latest, err}
		}()
	}
	return func() {
		latest := cache.Latest
		if fresh != nil {
			select {
			case r := <-fresh:
				latest = r.latest
				if r.err != nil {
					fmt.Fprintln(os.Stderr, styleWarn.Render("! ")+styleMuted.Render(capitalize(checkFailure(r.err))))
				}
			case <-time.After(1500 * time.Millisecond): // don't hold up a quick command
			}
		}
		if newer(latest, version) && latest != updateNoticeShown {
			fmt.Fprintln(os.Stderr, styleWarn.Render("↑ ")+fmt.Sprintf("New version %s available (you have %s) — run: ",
				latest, version)+styleCode.Render("payload doctor"))
		}
	}
}

// updateNoticeShown is the release a command already announced itself (e.g. in the payload view
// footer), so the end-of-command notice doesn't repeat it.
var updateNoticeShown string

// knownNewerRelease returns the newer release found by the last daily check, if any. It reads the
// cache only, so it never waits on the network.
func knownNewerRelease() string {
	if !updateCheckEnabled() || version == "dev" {
		return ""
	}
	if latest := readUpdateCache().Latest; newer(latest, version) {
		return latest
	}
	return ""
}

// stderrTTY reports whether notices on stderr will be seen by a person (not piped or redirected).
var stderrTTY = func() bool {
	return isatty.IsTerminal(os.Stderr.Fd()) || isatty.IsCygwinTerminal(os.Stderr.Fd())
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func updateCheckEnabled() bool {
	if v := os.Getenv("PAYLOAD_NO_UPDATE_CHECK"); v != "" && v != "0" {
		return false
	}
	return updateCheck
}

// --- doctor -----------------------------------------------------------------------------------

func doctor(dir string) {
	fmt.Println(styleBadge.Render("payload doctor"))
	ok := func(label, detail string) {
		fmt.Printf("  %s %-11s %s\n", styleOK.Render("✓"), label, detail)
	}
	bad := func(label, detail string) {
		fmt.Printf("  %s %-11s %s\n", styleWarn.Render("!"), label, detail)
	}

	// Version
	latest, err := latestRelease(10 * time.Second)
	if err == nil {
		writeUpdateCache(updateCache{Checked: time.Now(), Latest: latest})
	}
	switch {
	case err != nil:
		bad("version", fmt.Sprintf("%s — %s", version, checkFailure(err)))
	case version == "dev":
		bad("version", fmt.Sprintf("dev build — latest release is %s", latest))
	case newer(latest, version):
		bad("version", fmt.Sprintf("%s — %s is available", version, styleTitle.Render(latest)))
	default:
		ok("version", version+" (latest)")
	}

	// Binary and PATH
	exe, _ := os.Executable()
	exe, _ = filepath.EvalSymlinks(exe)
	if onPath, err := exec.LookPath("payload"); err != nil {
		bad("binary", exe+" — not on PATH")
	} else if p, _ := filepath.EvalSymlinks(onPath); !strings.EqualFold(filepath.Clean(p), filepath.Clean(exe)) {
		bad("binary", fmt.Sprintf("%s — but \"payload\" on PATH runs %s", exe, onPath))
	} else {
		ok("binary", exe)
	}

	// Storage
	ok("storage", fmt.Sprintf("%s (%d payloads)", dir, len(keys(dir))))

	// Settings
	if _, err := os.Stat(settingsPath()); err != nil {
		ok("settings", "default keys (no "+settingsPath()+")")
	} else if data, _ := os.ReadFile(settingsPath()); !json.Valid(data) {
		bad("settings", settingsPath()+" is not valid JSON — defaults in use")
	} else {
		ok("settings", settingsPath())
	}

	// Update checks
	if updateCheckEnabled() {
		ok("updates", "checked daily")
	} else {
		ok("updates", "daily check turned off")
	}

	// Clipboard (Linux depends on what is installed)
	if runtime.GOOS == "linux" {
		tool := ""
		for _, t := range []string{"wl-copy", "xclip", "xsel"} {
			if _, err := exec.LookPath(t); err == nil {
				tool = t
				break
			}
		}
		switch {
		case tool != "":
			ok("clipboard", tool)
		case os.Getenv("DISPLAY") != "":
			ok("clipboard", "built-in (no xclip/wl-copy needed)")
		default:
			bad("clipboard", "none — no graphical session (DISPLAY not set)")
		}
	}

	if err != nil || !(version == "dev" || newer(latest, version)) {
		return // offline or up to date
	}
	fmt.Println()
	question := fmt.Sprintf("Update payload to %s?", latest)
	if version == "dev" {
		// A local build may have changes the release lacks, so say what will happen.
		question = fmt.Sprintf("Replace this dev build with release %s?", latest)
	}
	if !confirm(question, "Downloads the release from GitHub and replaces "+exe, "Update") {
		info("Not updated.")
		return
	}
	if err := selfUpdate(exe, latest); err != nil {
		fail("update failed: %v", err)
	}
	success("Updated to %s", styleTitle.Render(latest))
}

// selfUpdate downloads release tag for this OS/arch, checks it runs, and swaps it in for exe.
func selfUpdate(exe, tag string) error {
	asset := fmt.Sprintf("payload_%s_%s", runtime.GOOS, runtime.GOARCH)
	if runtime.GOOS == "windows" {
		asset += ".exe"
	}
	info("Downloading %s %s...", asset, tag)
	resp, err := (&http.Client{Timeout: 2 * time.Minute}).Get(repoURL + "/releases/download/" + tag + "/" + asset)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download: %s", resp.Status)
	}

	tmp := exe + ".new"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	hash := sha256.New()
	_, err = io.Copy(io.MultiWriter(f, hash), resp.Body)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmp)
		return err
	}

	// Compare with the SHA256SUMS the release workflow published next to the binaries.
	if err := verifyChecksum(tag, asset, hex.EncodeToString(hash.Sum(nil))); err != nil {
		os.Remove(tmp)
		return err
	}

	// Make sure the new binary actually runs here (e.g. Windows Smart App Control may block it).
	if out, err := exec.Command(tmp, "version").Output(); err != nil || strings.TrimSpace(string(out)) != tag {
		os.Remove(tmp)
		if err == nil {
			err = errors.New("it reported version " + strings.TrimSpace(string(out)))
		}
		return fmt.Errorf("the downloaded binary did not run (%v) — see \"If Windows blocks payload\" in the README", err)
	}

	// Windows cannot overwrite a running .exe but can rename it; the .old file is removed next run.
	if runtime.GOOS == "windows" {
		old := exe + ".old"
		os.Remove(old)
		if err := os.Rename(exe, old); err != nil {
			os.Remove(tmp)
			return err
		}
		if err := os.Rename(tmp, exe); err != nil {
			os.Rename(old, exe)
			return err
		}
	} else if err := os.Rename(tmp, exe); err != nil {
		os.Remove(tmp)
		return err
	}

	// Let the new version refresh the AI README (settings are kept).
	setupCmd := exec.Command(exe, "setup")
	setupCmd.Stdout, setupCmd.Stderr = io.Discard, os.Stderr
	_ = setupCmd.Run()
	return nil
}

// verifyChecksum checks a downloaded asset's SHA-256 against the release's SHA256SUMS.
// Releases published before checksums existed have no SHA256SUMS; those are noted and allowed.
func verifyChecksum(tag, asset, actual string) error {
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Get(repoURL + "/releases/download/" + tag + "/SHA256SUMS")
	if err != nil {
		return fmt.Errorf("could not fetch SHA256SUMS: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		info("Release %s has no SHA256SUMS — skipping checksum verification.", tag)
		return nil
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("could not fetch SHA256SUMS: %s", resp.Status)
	}
	sums, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	expected := ""
	for _, line := range strings.Split(string(sums), "\n") {
		if f := strings.Fields(line); len(f) == 2 && strings.TrimPrefix(f[1], "*") == asset {
			expected = strings.ToLower(f[0])
			break
		}
	}
	if expected != actual {
		if expected == "" {
			expected = "<not listed>"
		}
		return fmt.Errorf("checksum mismatch for %s — expected %s, downloaded %s; nothing was changed",
			asset, expected, actual)
	}
	info("Checksum verified (sha256 %s)", actual)
	return nil
}

// cleanupOldBinary removes the copy a Windows self-update left behind.
func cleanupOldBinary() {
	if runtime.GOOS != "windows" {
		return
	}
	if exe, err := os.Executable(); err == nil {
		os.Remove(exe + ".old")
	}
}
