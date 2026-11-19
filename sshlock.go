package main

import (
	"crypto/hmac"
	"crypto/pbkdf2"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/charmbracelet/huh"
)

// Master password for payload ssh and the env vault. When one is set, every payload ssh and
// payload env command — and anything else that reads a value from the env vault, such as filling
// in {{name}} placeholders — asks for it unless payload was unlocked in the last 10 hours on this
// computer; payload ssh lock locks it again.
//
// vault/ssh-lock.json keeps a salted PBKDF2-SHA256 hash, never the password. An unlock is a record
// in .cache/ssh-unlocked holding its expiry and an HMAC over it keyed by that hash (so changing the
// password ends it), sealed with this computer's secret store (DPAPI on Windows). Neither file is
// synced. It keeps payload ssh closed to someone at an unlocked computer; it does not encrypt the
// logins or the env vault.

const (
	sshUnlockFor = 10 * time.Hour
	sshLockIter  = 600_000
)

type sshLock struct {
	Salt string `json:"salt"` // hex
	Hash string `json:"hash"` // hex PBKDF2-SHA256(password, salt, iter, 32)
	Iter int    `json:"iter"`
}

func sshLockPath() string    { return filepath.Join(storeDir(), "vault", "ssh-lock.json") }
func sshSessionPath() string { return filepath.Join(storeDir(), ".cache", "ssh-unlocked") }

// loadSSHLock returns the master password record, or nil when none is set.
func loadSSHLock() (*sshLock, error) {
	data, err := os.ReadFile(sshLockPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var l sshLock
	if err := json.Unmarshal(data, &l); err != nil || l.Salt == "" || l.Hash == "" || l.Iter <= 0 {
		return nil, fmt.Errorf("%s is damaged — delete it to remove the master password", sshLockPath())
	}
	return &l, nil
}

func (l *sshLock) key(password string) []byte {
	salt, _ := hex.DecodeString(l.Salt)
	k, _ := pbkdf2.Key(sha256.New, password, salt, l.Iter, 32)
	return k
}

// check reports whether password is the master password.
func (l *sshLock) check(password string) bool {
	want, err := hex.DecodeString(l.Hash)
	return err == nil && hmac.Equal(l.key(password), want)
}

// setSSHMasterPassword stores a new master password and unlocks payload ssh for 10 hours.
func setSSHMasterPassword(password string) error {
	l := &sshLock{Salt: hex.EncodeToString(randomBytes(16)), Iter: sshLockIter}
	l.Hash = hex.EncodeToString(l.key(password))
	data, _ := json.MarshalIndent(l, "", "  ")
	if err := os.MkdirAll(filepath.Dir(sshLockPath()), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(sshLockPath(), append(data, '\n'), 0o600); err != nil {
		return err
	}
	return startSSHSession(l)
}

func removeSSHMasterPassword() error {
	lockSSH()
	if err := os.Remove(sshLockPath()); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (l *sshLock) sessionMAC(until int64) string {
	k, _ := hex.DecodeString(l.Hash)
	m := hmac.New(sha256.New, k)
	m.Write([]byte("payload-ssh-unlocked:" + strconv.FormatInt(until, 10)))
	return hex.EncodeToString(m.Sum(nil))
}

type sshSession struct {
	Until int64  `json:"until"`
	MAC   string `json:"mac"`
}

// startSSHSession unlocks payload ssh for sshUnlockFor.
func startSSHSession(l *sshLock) error {
	until := time.Now().Add(sshUnlockFor).Unix()
	data, _ := json.Marshal(sshSession{Until: until, MAC: l.sessionMAC(until)})
	sealed, err := sealSecret(data)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(sshSessionPath()), 0o755); err != nil {
		return err
	}
	return os.WriteFile(sshSessionPath(), sealed, 0o600)
}

// sshUnlockedUntil is when the current unlock ends; ok is false when payload ssh is locked.
func sshUnlockedUntil(l *sshLock) (until time.Time, ok bool) {
	sealed, err := os.ReadFile(sshSessionPath())
	if err != nil {
		return time.Time{}, false
	}
	data, err := openSecret(sealed)
	if err != nil {
		return time.Time{}, false
	}
	var s sshSession
	if json.Unmarshal(data, &s) != nil || !hmac.Equal([]byte(s.MAC), []byte(l.sessionMAC(s.Until))) {
		return time.Time{}, false
	}
	until = time.Unix(s.Until, 0)
	if !time.Now().Before(until) || until.After(time.Now().Add(sshUnlockFor)) {
		return time.Time{}, false
	}
	return until, true
}

// lockSSH ends the current unlock.
func lockSSH() {
	_ = os.Remove(sshSessionPath())
	unlocked = false
}

// errLocked is what reading the env vault returns while payload is locked.
var errLocked = errors.New("payload is locked — run payload ssh unlock")

// unlocked is set once this process has found payload unlocked (or no master password set), so
// the check runs once per command and an unlock that expires mid-command doesn't interrupt it.
var unlocked bool

// isLocked reports, without asking, whether a master password is set and payload is locked.
func isLocked() bool {
	if unlocked {
		return false
	}
	l, err := loadSSHLock()
	if err != nil {
		return true // damaged lock file: requireUnlock reports it
	}
	if l == nil {
		unlocked = true
		return false
	}
	if _, ok := sshUnlockedUntil(l); ok {
		unlocked = true
		return false
	}
	return true
}

// requireUnlock lets a command that needs payload ssh or the env vault run: at once when no
// master password is set or the unlock hasn't expired, otherwise after asking for the master
// password (or failing, without a terminal).
func requireUnlock() {
	if !isLocked() {
		return
	}
	l, err := loadSSHLock()
	if err != nil {
		fail("%v", err)
	}
	if !interactive {
		fail("payload is locked — run payload ssh unlock in a terminal first")
	}
	if !askSSHMasterPassword(l, "🔒 payload is locked", "Master password — unlocks payload ssh and the env vault for 10 hours.") {
		fail("payload is still locked")
	}
	if err := startSSHSession(l); err != nil {
		fail("could not unlock: %v", err)
	}
	unlocked = true
	until, _ := sshUnlockedUntil(l)
	fmt.Fprintln(os.Stderr, styleOK.Render("✓ Unlocked")+styleMuted.Render(" until "+until.Format("15:04 Mon")+" · payload ssh lock locks it again"))
}

// askSSHMasterPassword asks until the master password is right; false if the form is left.
func askSSHMasterPassword(l *sshLock, title, description string) bool {
	var pw, ok string
	err := huh.NewForm(huh.NewGroup(
		huh.NewInput().Title(title).Description(description).EchoMode(huh.EchoModePassword).Value(&pw).
			Validate(func(v string) error {
				if v != "" && v == ok {
					return nil // already checked (huh validates again when the field loses focus)
				}
				if !l.check(v) {
					return errors.New("wrong master password")
				}
				ok = v
				return nil
			}),
	)).WithTheme(theme()).Run()
	return err == nil
}

// sshSettingsCmd is payload ssh settings: set, change or remove the master password.
func sshSettingsCmd() {
	if !interactive {
		fail("payload ssh settings needs a terminal")
	}
	l, err := loadSSHLock()
	if err != nil {
		fail("%v", err)
	}
	fmt.Println(styleSSHBadge.Render("ssh settings"))
	if l == nil {
		fmt.Println(styleMuted.Render("  No master password: payload ssh and the env vault open without asking."))
		var set bool
		if huh.NewConfirm().Title("Set a master password for payload ssh and the env vault?").
			Description("Every payload ssh and payload env command (and filling in {{name}} values) will ask for it, then stay unlocked for 10 hours (payload ssh lock locks it sooner).").
			Affirmative("Set one").Negative("Cancel").Value(&set).WithTheme(theme()).Run() != nil || !set {
			fmt.Println(styleMuted.Render("Nothing changed."))
			return
		}
		pw, ok := newSSHMasterPassword()
		if !ok {
			fmt.Println(styleMuted.Render("Nothing changed."))
			return
		}
		if err := setSSHMasterPassword(pw); err != nil {
			fail("could not save the master password: %v", err)
		}
		fmt.Println(styleOK.Render("✓ Master password set") + styleMuted.Render(" · unlocked for 10 hours · payload ssh lock locks it now"))
		return
	}

	status := "locked"
	if until, ok := sshUnlockedUntil(l); ok {
		status = "unlocked until " + until.Format("15:04 Mon")
	}
	fmt.Println(styleMuted.Render("  Master password is set · payload is " + status + "."))
	choice := "change"
	if huh.NewSelect[string]().Title("Master password").
		Options(huh.NewOption("Change the master password", "change"),
			huh.NewOption("Remove the master password", "remove"),
			huh.NewOption("Lock payload now", "lock"),
			huh.NewOption("Cancel", "")).
		Height(selectHeight(4, "")).Value(&choice).WithTheme(theme()).Run() != nil || choice == "" {
		fmt.Println(styleMuted.Render("Nothing changed."))
		return
	}
	if choice == "lock" {
		lockSSH()
		fmt.Println(styleOK.Render("✓ Locked") + styleMuted.Render(" · the next payload ssh or env command asks for the master password"))
		return
	}
	if !askSSHMasterPassword(l, "Current master password", "Needed to "+choice+" it.") {
		fmt.Println(styleMuted.Render("Nothing changed."))
		return
	}
	if choice == "remove" {
		if err := removeSSHMasterPassword(); err != nil {
			fail("could not remove the master password: %v", err)
		}
		fmt.Println(styleOK.Render("✓ Master password removed") + styleMuted.Render(" · payload ssh and the env vault open without asking"))
		return
	}
	pw, ok := newSSHMasterPassword()
	if !ok {
		fmt.Println(styleMuted.Render("Nothing changed."))
		return
	}
	if err := setSSHMasterPassword(pw); err != nil {
		fail("could not save the master password: %v", err)
	}
	fmt.Println(styleOK.Render("✓ Master password changed") + styleMuted.Render(" · unlocked for 10 hours"))
}

// newSSHMasterPassword asks for a new master password twice.
func newSSHMasterPassword() (string, bool) {
	var pw, again string
	err := huh.NewForm(huh.NewGroup(
		huh.NewInput().Title("New master password").Description("At least 6 characters. If you forget it, delete "+sshLockPath()+".").
			EchoMode(huh.EchoModePassword).Value(&pw).Validate(func(v string) error {
			if len([]rune(v)) < 6 {
				return errors.New("use at least 6 characters")
			}
			return nil
		}),
		huh.NewInput().Title("Type it again").EchoMode(huh.EchoModePassword).Value(&again).
			Validate(func(v string) error {
				if v != pw {
					return errors.New("doesn't match")
				}
				return nil
			}),
	)).WithTheme(theme()).Run()
	return pw, err == nil
}

// sshLockCmd is payload ssh lock.
func sshLockCmd() {
	l, err := loadSSHLock()
	if err != nil {
		fail("%v", err)
	}
	if l == nil {
		fmt.Println(styleWarn.Render("! No master password is set") + styleMuted.Render(" — set one with payload ssh settings"))
		return
	}
	lockSSH()
	fmt.Println(styleOK.Render("✓ payload locked") + styleMuted.Render(" · the next payload ssh or env command asks for the master password"))
}

// sshUnlockCmd is payload ssh unlock.
func sshUnlockCmd() {
	l, err := loadSSHLock()
	if err != nil {
		fail("%v", err)
	}
	if l == nil {
		fmt.Println(styleMuted.Render("No master password is set — payload ssh and the env vault are always open (payload ssh settings sets one)."))
		return
	}
	if until, ok := sshUnlockedUntil(l); ok {
		fmt.Println(styleOK.Render("✓ Already unlocked") + styleMuted.Render(" until "+until.Format("15:04 Mon")))
		return
	}
	requireUnlock()
}
