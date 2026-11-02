package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// The bundled curl: on Windows each release ships the official curl.se build as
// curl_windows_<arch>.exe (listed in SHA256SUMS), installed to <bin>/curl/curl.exe next to the
// payload binary. It sits in its own folder so it never shadows the system curl on PATH; only
// "payload curl" uses it. macOS and Linux always have curl, so they use the one on PATH.

// bundledCurlPath is where the bundled curl lives, or "" where none is bundled.
func bundledCurlPath() string {
	if runtime.GOOS != "windows" {
		return ""
	}
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	if p, err := filepath.EvalSymlinks(exe); err == nil {
		exe = p
	}
	return filepath.Join(filepath.Dir(exe), "curl", "curl.exe")
}

func curlAsset() string {
	return fmt.Sprintf("curl_windows_%s.exe", runtime.GOARCH)
}

// findCurl returns the curl to run and where it came from: the bundled one, else the one on PATH.
func findCurl() (path, source string, err error) {
	if p := bundledCurlPath(); p != "" {
		if _, err := os.Stat(p); err == nil {
			return p, "bundled", nil
		}
	}
	p, err := exec.LookPath("curl")
	if err != nil {
		hint := ""
		if bundledCurlPath() != "" {
			hint = " — run: payload curl install"
		}
		return "", "", fmt.Errorf("curl is not installed or not on PATH%s", hint)
	}
	return p, "PATH", nil
}

// curlVersion is the first line of "curl -V", e.g. "curl 8.23.0 (x86_64-w64-mingw32) ...", cut short.
func curlVersion(path string) string {
	out, err := exec.Command(path, "-V").Output()
	if err != nil {
		return ""
	}
	f := strings.Fields(strings.SplitN(string(out), "\n", 2)[0])
	if len(f) < 2 {
		return ""
	}
	return f[0] + " " + f[1]
}

// curlPathCmd prints the curl "payload curl" runs (just the path when piped).
func curlPathCmd() {
	path, source, err := findCurl()
	if err != nil {
		fail("%v", err)
	}
	if !stdoutTTY {
		fmt.Println(path)
		return
	}
	detail := source
	if v := curlVersion(path); v != "" {
		detail += ", " + v
	}
	fmt.Println(path + "  " + styleMuted.Render("("+detail+")"))
	if source == "PATH" && bundledCurlPath() != "" {
		info("No bundled curl in %s — run \"payload curl install\" to add it.", filepath.Dir(bundledCurlPath()))
	}
}

// curlInstallCmd downloads the bundled curl of this release (the latest one for a dev build).
func curlInstallCmd() {
	if bundledCurlPath() == "" {
		info("payload bundles curl on Windows only; here it uses the curl on PATH.")
		return
	}
	tag := version
	if tag == "dev" {
		latest, err := latestRelease(10 * time.Second)
		if err != nil {
			fail("cannot find the latest release: %s", checkFailure(err))
		}
		tag = latest
	}
	if err := installBundledCurl(tag); err != nil {
		fail("could not install curl: %v", err)
	}
}

// installBundledCurl downloads release tag's curl, verifies it against SHA256SUMS, checks it runs
// and puts it at bundledCurlPath.
func installBundledCurl(tag string) error {
	target := bundledCurlPath()
	asset := curlAsset()
	info("Downloading %s %s...", asset, tag)
	resp, err := (&http.Client{Timeout: 2 * time.Minute}).Get(repoURL + "/releases/download/" + tag + "/" + asset)
	if err != nil {
		return fmt.Errorf("download: %s", networkProblem(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("release %s has no bundled curl (added in v1.8.0)", tag)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download: %s", resp.Status)
	}

	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	tmp := target + ".new"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	hash := sha256.New()
	_, err = io.Copy(io.MultiWriter(f, hash), resp.Body)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = verifyChecksum(tag, asset, hex.EncodeToString(hash.Sum(nil)))
	}
	if err == nil {
		if out, rerr := exec.Command(tmp, "-V").Output(); rerr != nil || !strings.HasPrefix(string(out), "curl ") {
			err = fmt.Errorf("the downloaded curl did not run (%v)", rerr)
		}
	}
	if err != nil {
		os.Remove(tmp)
		return err
	}
	os.Remove(target + ".old")
	if _, serr := os.Stat(target); serr == nil {
		if err := os.Rename(target, target+".old"); err != nil { // works even while it runs
			os.Remove(tmp)
			return err
		}
	}
	if err := os.Rename(tmp, target); err != nil {
		os.Rename(target+".old", target)
		return err
	}
	os.Remove(target + ".old")
	success("Installed %s %s", target, styleMuted.Render("("+curlVersion(target)+")"))
	return nil
}
