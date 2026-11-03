package main

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// deviceID identifies this computer to other payload clients. It is derived from the operating
// system's own machine ID (see machineID in device_*.go), hashed so the raw ID never leaves the
// computer, which keeps it the same across reinstalls and a deleted payload folder. Without a
// machine ID it falls back to a random ID kept in the user's config folder.
var deviceID = sync.OnceValue(func() string {
	if id := strings.TrimSpace(machineID()); id != "" {
		return formatDeviceID(sha256.Sum256([]byte("payload-device:" + strings.ToLower(id))))
	}
	path := deviceIDFallbackPath()
	if b, err := os.ReadFile(path); err == nil && len(strings.TrimSpace(string(b))) > 0 {
		return strings.TrimSpace(string(b))
	}
	id := formatDeviceID(sha256.Sum256(randomBytes(32)))
	if os.MkdirAll(filepath.Dir(path), 0o700) == nil {
		_ = os.WriteFile(path, []byte(id+"\n"), 0o600)
	}
	return id
})

// formatDeviceID is "pd-" and 16 hex characters, e.g. pd-3f9a0c41d27be610.
func formatDeviceID(sum [32]byte) string {
	return "pd-" + hex.EncodeToString(sum[:8])
}

func deviceIDFallbackPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = os.TempDir()
	}
	return filepath.Join(dir, "payload", "device-id")
}

// validDeviceID accepts the IDs deviceID makes (so a peer can't smuggle odd text into settings).
func validDeviceID(id string) bool {
	if len(id) != 19 || !strings.HasPrefix(id, "pd-") {
		return false
	}
	_, err := hex.DecodeString(id[3:])
	return err == nil
}
