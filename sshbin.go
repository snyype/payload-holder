package main

import (
	"archive/zip"
	"bytes"
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

// The bundled ssh: on Windows each release ships Microsoft's OpenSSH for Windows client
// (github.com/PowerShell/Win32-OpenSSH) as ssh_windows_<arch>.zip — ssh.exe, libcrypto.dll and
// their license files — installed to <bin>/ssh/ next to the payload binary. Like the bundled curl
// it is not on PATH; only "payload ssh" uses it. macOS and Linux use the ssh on PATH.

// bundledSSHDir is where the bundled ssh lives, or "" where none is bundled.
func bundledSSHDir() string {
	if p := bundledCurlPath(); p != "" {
		return filepath.Join(filepath.Dir(filepath.Dir(p)), "ssh")
	}
	return ""
}

func sshAsset() string {
	return fmt.Sprintf("ssh_windows_%s.zip", runtime.GOARCH)
}

// findSSH returns the ssh to run and where it came from: bundled, Windows' own, or PATH.
func findSSH() (path, source string, err error) {
	if d := bundledSSHDir(); d != "" {
		if p := filepath.Join(d, "ssh.exe"); fileExists(p) {
			return p, "bundled", nil
		}
		if p := filepath.Join(os.Getenv("SystemRoot"), "System32", "OpenSSH", "ssh.exe"); fileExists(p) {
			return p, "Windows", nil
		}
	}
	p, err := exec.LookPath("ssh")
	if err != nil {
		hint := ""
		if bundledSSHDir() != "" {
			hint = " — run: payload ssh install"
		}
		return "", "", fmt.Errorf("ssh is not installed or not on PATH%s", hint)
	}
	return p, "PATH", nil
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// sshVersion is "ssh -V", e.g. "OpenSSH_for_Windows_10.4p1" (ssh prints it on stderr).
func sshVersion(path string) string {
	out, _ := exec.Command(path, "-V").CombinedOutput()
	v, _, _ := strings.Cut(strings.TrimSpace(string(out)), ",")
	return v
}

func sshPathCmd() {
	path, source, err := findSSH()
	if err != nil {
		fail("%v", err)
	}
	if !stdoutTTY {
		fmt.Println(path)
		return
	}
	fmt.Println(path + "  " + styleMuted.Render("("+source+", "+sshVersion(path)+")"))
	if source != "bundled" && bundledSSHDir() != "" {
		info("No bundled ssh in %s — run \"payload ssh install\" to add it.", bundledSSHDir())
	}
}

func sshInstallCmd() {
	if bundledSSHDir() == "" {
		info("payload bundles ssh on Windows only; here it uses the ssh on PATH.")
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
	if err := installBundledSSH(tag); err != nil {
		fail("could not install ssh: %v", err)
	}
}

// installBundledSSH downloads release tag's ssh zip, verifies it against SHA256SUMS, unpacks it,
// checks ssh runs and swaps it in at bundledSSHDir.
func installBundledSSH(tag string) error {
	dir := bundledSSHDir()
	asset := sshAsset()
	info("Downloading %s %s...", asset, tag)
	resp, err := (&http.Client{Timeout: 2 * time.Minute}).Get(repoURL + "/releases/download/" + tag + "/" + asset)
	if err != nil {
		return fmt.Errorf("download: %s", networkProblem(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("release %s has no bundled ssh (added in v2.5.0)", tag)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download: %s", resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return err
	}
	sum := sha256.Sum256(data)
	if err := verifyChecksum(tag, asset, hex.EncodeToString(sum[:])); err != nil {
		return err
	}

	tmp := dir + ".new"
	os.RemoveAll(tmp)
	if err := unzipFlat(data, tmp); err != nil {
		os.RemoveAll(tmp)
		return err
	}
	if v := sshVersion(filepath.Join(tmp, "ssh.exe")); !strings.Contains(v, "OpenSSH") {
		os.RemoveAll(tmp)
		return fmt.Errorf("the downloaded ssh did not run (%q)", v)
	}
	os.RemoveAll(dir + ".old")
	if fileExists(dir) {
		if err := os.Rename(dir, dir+".old"); err != nil {
			os.RemoveAll(tmp)
			return fmt.Errorf("could not replace %s (is an ssh session open?): %v", dir, err)
		}
	}
	if err := os.Rename(tmp, dir); err != nil {
		os.Rename(dir+".old", dir)
		return err
	}
	os.RemoveAll(dir + ".old")
	exe := filepath.Join(dir, "ssh.exe")
	success("Installed %s %s", exe, styleMuted.Render("("+sshVersion(exe)+")"))
	return nil
}

// unzipFlat writes the zip's files (no folders, plain names only) into dir.
func unzipFlat(data []byte, dir string) error {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for _, f := range zr.File {
		name := filepath.Base(f.Name)
		if f.FileInfo().IsDir() || name != f.Name || strings.ContainsAny(name, `/\:`) {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		out, err := os.OpenFile(filepath.Join(dir, name), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
		if err == nil {
			_, err = io.Copy(out, io.LimitReader(rc, 64<<20))
			if cerr := out.Close(); err == nil {
				err = cerr
			}
		}
		rc.Close()
		if err != nil {
			return err
		}
	}
	return nil
}
