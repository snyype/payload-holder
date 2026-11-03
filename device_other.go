//go:build !windows

package main

import (
	"os"
	"os/exec"
	"regexp"
	"runtime"
)

// machineID is the OS machine ID: IOPlatformUUID on macOS, /etc/machine-id (systemd/dbus) on Linux.
func machineID() string {
	if runtime.GOOS == "darwin" {
		out, err := exec.Command("ioreg", "-rd1", "-c", "IOPlatformExpertDevice").Output()
		if err != nil {
			return ""
		}
		if m := regexp.MustCompile(`"IOPlatformUUID" = "([^"]+)"`).FindSubmatch(out); m != nil {
			return string(m[1])
		}
		return ""
	}
	for _, p := range []string{"/etc/machine-id", "/var/lib/dbus/machine-id"} {
		if b, err := os.ReadFile(p); err == nil && len(b) > 0 {
			return string(b)
		}
	}
	return ""
}
