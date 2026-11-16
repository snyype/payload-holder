package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

func TestSSHMasterPassword(t *testing.T) {
	t.Setenv("PAYLOAD_STORE", t.TempDir())
	if l, err := loadSSHLock(); l != nil || err != nil {
		t.Fatalf("no lock expected, got %v %v", l, err)
	}
	if err := setSSHMasterPassword("hunter22"); err != nil {
		t.Fatal(err)
	}
	l, err := loadSSHLock()
	if err != nil || l == nil {
		t.Fatalf("lock not saved: %v", err)
	}
	if data, _ := os.ReadFile(sshLockPath()); strings.Contains(string(data), "hunter22") {
		t.Fatal("the password itself must not be stored")
	}
	if !l.check("hunter22") || l.check("hunter23") || l.check("") {
		t.Fatal("check")
	}

	// Setting it unlocks for 10 hours.
	until, ok := sshUnlockedUntil(l)
	if !ok || time.Until(until) < sshUnlockFor-time.Minute || time.Until(until) > sshUnlockFor {
		t.Fatalf("unlocked until %v (%v)", until, ok)
	}

	// payload ssh lock ends it.
	lockSSH()
	if _, ok := sshUnlockedUntil(l); ok {
		t.Fatal("still unlocked after lock")
	}

	// A record whose time was changed doesn't count.
	if err := startSSHSession(l); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(72 * time.Hour).Unix()
	data, _ := json.Marshal(sshSession{Until: later, MAC: l.sessionMAC(time.Now().Add(time.Hour).Unix())})
	sealed, _ := sealSecret(data)
	_ = os.WriteFile(sshSessionPath(), sealed, 0o600)
	if _, ok := sshUnlockedUntil(l); ok {
		t.Fatal("tampered unlock accepted")
	}

	// Changing the password ends an unlock made with the old one.
	if err := startSSHSession(l); err != nil {
		t.Fatal(err)
	}
	old := *l
	if err := setSSHMasterPassword("another-one"); err != nil {
		t.Fatal(err)
	}
	if _, ok := sshUnlockedUntil(&old); ok {
		t.Fatal("old password's unlock should not count")
	}

	if err := removeSSHMasterPassword(); err != nil {
		t.Fatal(err)
	}
	if l, _ := loadSSHLock(); l != nil {
		t.Fatal("master password not removed")
	}
}
