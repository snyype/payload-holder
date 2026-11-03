package main

import (
	"archive/zip"
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/pbkdf2"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image/png"
	"io"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/huh"
)

// payload ssh export / import: every folder (with its icon) and login in one .zip, with the same
// IDs, so it can be imported on another computer. Passwords are included only encrypted:
// AES-256-GCM with a key from the passphrase (PBKDF2-SHA256, 600,000 rounds).
//
//	manifest.json   what's inside
//	ssh.json        folders and logins (the same format as vault/ssh.json)
//	passwords.enc   optional: the logins' env vault entries, encrypted

const (
	sshExportFormat = "payload-ssh-export"
	pbkdf2Rounds    = 600_000
	passphraseEnv   = "PAYLOAD_SSH_PASSPHRASE" // for scripts; otherwise payload asks
)

type sshManifest struct {
	Format     string    `json:"format"`
	Version    int       `json:"version"`
	ExportedBy string    `json:"exported_by"`
	Created    time.Time `json:"created"`
	Folders    int       `json:"folders"`
	Logins     int       `json:"logins"`
	Passwords  bool      `json:"passwords"`
}

type sealedBox struct {
	KDF    string `json:"kdf"`
	Rounds int    `json:"rounds"`
	Salt   string `json:"salt"`
	Nonce  string `json:"nonce"`
	Data   string `json:"data"`
}

// exportedEnv is one env vault entry: a plain value or a group (server/jump).
type exportedEnv struct {
	Value string            `json:"value,omitempty"`
	Group map[string]string `json:"group,omitempty"`
}

func passphraseKey(pass string, salt []byte) ([]byte, error) {
	return pbkdf2.Key(sha256.New, pass, salt, pbkdf2Rounds, 32)
}

func sealWithPassphrase(plain []byte, pass string) (sealedBox, error) {
	salt, nonce := randomBytes(16), randomBytes(12)
	key, err := passphraseKey(pass, salt)
	if err != nil {
		return sealedBox{}, err
	}
	block, _ := aes.NewCipher(key)
	gcm, _ := cipher.NewGCM(block)
	return sealedBox{KDF: "pbkdf2-sha256", Rounds: pbkdf2Rounds,
		Salt: base64.StdEncoding.EncodeToString(salt), Nonce: base64.StdEncoding.EncodeToString(nonce),
		Data: base64.StdEncoding.EncodeToString(gcm.Seal(nil, nonce, plain, nil))}, nil
}

func openWithPassphrase(b sealedBox, pass string) ([]byte, error) {
	if b.KDF != "pbkdf2-sha256" || b.Rounds < 1 {
		return nil, fmt.Errorf("unknown encryption %q", b.KDF)
	}
	salt, err1 := base64.StdEncoding.DecodeString(b.Salt)
	nonce, err2 := base64.StdEncoding.DecodeString(b.Nonce)
	data, err3 := base64.StdEncoding.DecodeString(b.Data)
	if err := errors.Join(err1, err2, err3); err != nil || len(nonce) != 12 {
		return nil, errors.New("passwords.enc is damaged")
	}
	key, err := pbkdf2.Key(sha256.New, pass, salt, b.Rounds, 32)
	if err != nil {
		return nil, err
	}
	block, _ := aes.NewCipher(key)
	gcm, _ := cipher.NewGCM(block)
	plain, err := gcm.Open(nil, nonce, data, nil)
	if err != nil {
		return nil, errors.New("wrong passphrase")
	}
	return plain, nil
}

// askPassphrase gets the passphrase from PAYLOAD_SSH_PASSPHRASE or asks for it (twice when confirm).
func askPassphrase(title string, confirm bool) (string, error) {
	if v := os.Getenv(passphraseEnv); v != "" {
		return v, nil
	}
	if !interactive {
		return "", fmt.Errorf("set %s, or run this in a terminal", passphraseEnv)
	}
	var p1, p2 string
	fields := []huh.Field{huh.NewInput().Title(title).EchoMode(huh.EchoModePassword).Value(&p1).
		Validate(func(v string) error {
			if confirm && len(v) < 8 {
				return errors.New("use at least 8 characters")
			}
			return nil
		})}
	if confirm {
		fields = append(fields, huh.NewInput().Title("Repeat the passphrase").EchoMode(huh.EchoModePassword).Value(&p2).
			Validate(func(v string) error {
				if v != p1 {
					return errors.New("the passphrases don't match")
				}
				return nil
			}))
	}
	if err := huh.NewForm(huh.NewGroup(fields...)).WithTheme(theme()).Run(); err != nil {
		return "", errors.New("cancelled")
	}
	return p1, nil
}

// --- export -------------------------------------------------------------------------------------

func sshExportCmd(args []string) {
	file, withPasswords := "", true
	for _, a := range args {
		switch {
		case a == "--no-passwords":
			withPasswords = false
		case strings.HasPrefix(a, "-"):
			fail("usage: payload ssh export [file.zip] [--no-passwords]")
		default:
			file = a
		}
	}
	if file == "" {
		file = "payload-ssh-" + time.Now().Format("2006-01-02") + ".zip"
	}
	if !strings.HasSuffix(strings.ToLower(file), ".zip") {
		file += ".zip"
	}
	s := mustLoadSSH()
	m := sshManifest{Format: sshExportFormat, Version: 1, ExportedBy: "payload " + version, Created: time.Now().UTC()}
	for _, n := range s.Nodes {
		if n.Type == sshFolder {
			m.Folders++
		} else {
			m.Logins++
		}
	}
	if len(s.Nodes) == 0 {
		fail("nothing to export — add clients with payload ssh clients")
	}

	// The logins' env vault entries.
	entries := map[string]exportedEnv{}
	if withPasswords {
		env, groups, err := loadEnvGroups()
		if err != nil {
			fail("cannot read env vault: %v", err)
		}
		for i := range s.Nodes {
			n := &s.Nodes[i]
			if n.Type != sshLogin {
				continue
			}
			name := n.envName()
			if v, ok := env[name]; ok {
				entries[name] = exportedEnv{Value: v}
			} else if groups[name] {
				g := map[string]string{}
				for _, k := range envGroupKeys(env, name) {
					g[strings.TrimPrefix(k, name+".")] = env[k]
				}
				entries[name] = exportedEnv{Group: g}
			}
		}
	}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	put := func(name string, data []byte) {
		w, err := zw.Create(name)
		if err == nil {
			_, err = w.Write(data)
		}
		if err != nil {
			fail("cannot write the zip: %v", err)
		}
	}
	if len(entries) > 0 {
		pass, err := askPassphrase(fmt.Sprintf("Passphrase to encrypt %d login password(s)", len(entries)), true)
		if err != nil {
			fail("passwords not exported: %v — or leave them out: payload ssh export --no-passwords", err)
		}
		plain, _ := json.Marshal(entries)
		box, err := sealWithPassphrase(plain, pass)
		if err != nil {
			fail("%v", err)
		}
		data, _ := json.MarshalIndent(box, "", "  ")
		put("passwords.enc", data)
		m.Passwords = true
	}
	manifest, _ := json.MarshalIndent(m, "", "  ")
	put("manifest.json", manifest)
	nodes, _ := json.MarshalIndent(s, "", "  ")
	put("ssh.json", nodes)
	for _, n := range s.Nodes {
		if n.Logo != "" {
			if data, err := os.ReadFile(sshLogoPath(n.Logo)); err == nil {
				put("logos/"+n.Logo, data)
			}
		}
	}
	if err := zw.Close(); err != nil {
		fail("%v", err)
	}
	if err := os.WriteFile(file, buf.Bytes(), 0o600); err != nil {
		fail("cannot write %s: %v", file, err)
	}
	what := fmt.Sprintf("%s, %s", plural(m.Folders, "folder"), plural(m.Logins, "login"))
	if m.Passwords {
		what += fmt.Sprintf(", %s encrypted", plural(len(entries), "password entry"))
	} else {
		what += ", no passwords"
	}
	success("Exported %s to %s", what, styleTitle.Render(file))
	info("On the other computer: payload ssh import %s", file)
}

// --- import -------------------------------------------------------------------------------------

func sshImportCmd(args []string) {
	file, yes := "", false
	for _, a := range args {
		switch a {
		case "-y", "--yes":
			yes = true
		default:
			file = a
		}
	}
	if file == "" {
		fail("usage: payload ssh import <file.zip> [-y]")
	}
	data, err := os.ReadFile(file)
	if err != nil {
		fail("cannot read %s: %v", file, err)
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		fail("%s is not a zip: %v", file, err)
	}
	read := func(name string) []byte {
		for _, f := range zr.File {
			if f.Name == name {
				rc, err := f.Open()
				if err != nil {
					fail("%s: %v", name, err)
				}
				defer rc.Close()
				b, err := io.ReadAll(io.LimitReader(rc, 32<<20))
				if err != nil {
					fail("%s: %v", name, err)
				}
				return b
			}
		}
		return nil
	}
	var m sshManifest
	if json.Unmarshal(read("manifest.json"), &m) != nil || m.Format != sshExportFormat {
		fail("%s is not a payload ssh export", file)
	}
	var in sshStore
	if err := json.Unmarshal(read("ssh.json"), &in); err != nil {
		fail("ssh.json in %s: %v", file, err)
	}
	if err := checkImportedNodes(in.Nodes); err != nil {
		fail("%s: %v", file, err)
	}

	s := mustLoadSSH()
	var added, updated []string
	byID := map[string]int{}
	for i, n := range s.Nodes {
		byID[n.ID] = i
	}
	for _, n := range in.Nodes {
		if i, ok := byID[n.ID]; ok {
			s.Nodes[i] = n
			updated = append(updated, n.ID)
		} else {
			s.Nodes = append(s.Nodes, n)
			byID[n.ID] = len(s.Nodes) - 1
			added = append(added, n.ID)
		}
	}
	// An imported entry whose name is already used by another one in the same folder gets a suffix.
	for _, id := range added {
		n := &s.Nodes[byID[id]]
		for base, k := n.Name, 2; s.nameTaken(n.Parent, n.Name, n.ID); k++ {
			n.Name = fmt.Sprintf("%s-%d", base, k)
		}
	}

	fmt.Println(styleSSHBadge.Render("ssh import") + "  " + styleMuted.Render(fmt.Sprintf("%s · exported %s by %s",
		file, m.Created.Local().Format("2 Jan 2006 15:04"), m.ExportedBy)))
	show := func(mark string, style interface{ Render(...string) string }, ids []string) {
		for _, id := range ids {
			n := s.get(id)
			label := s.path(id)
			if n.Type == sshLogin {
				label += ".ssh"
			} else {
				label = firstNonEmpty(s.get(firstTopLevel(s, id)).Icon, "📁") + " " + label
			}
			fmt.Println("  " + style.Render(mark) + " " + label)
		}
	}
	show("+", styleOK, added)
	show("~", styleWarn, updated)
	if m.Passwords {
		info("  Includes encrypted passwords for the logins.")
	}
	if !yes {
		if !interactive {
			fail("import changes your ssh logins — run it again with -y to confirm")
		}
		if !confirm(fmt.Sprintf("Import %d new and update %d existing?", len(added), len(updated)),
			"Folders and logins with the same ID are replaced by the imported ones", "Import") {
			info("Nothing imported.")
			return
		}
	}

	pwNote := ""
	if m.Passwords {
		pwNote = importPasswords(read("passwords.enc"))
	}
	for _, n := range in.Nodes {
		if n.Logo == "" {
			continue
		}
		data := read("logos/" + n.Logo)
		if _, err := png.Decode(bytes.NewReader(data)); err != nil {
			warn("Logo of %s skipped: not a valid image", n.Name)
			s.get(n.ID).Logo = ""
			continue
		}
		if err := os.MkdirAll(sshLogoDir(), 0o755); err == nil {
			err = os.WriteFile(sshLogoPath(n.Logo), data, 0o644)
		}
	}
	if err := s.save(); err != nil {
		fail("could not save: %v", err)
	}
	success("Imported %d new, %d updated%s", len(added), len(updated), pwNote)
}

// importPasswords decrypts passwords.enc and writes its entries into the env vault.
func importPasswords(data []byte) string {
	var box sealedBox
	if json.Unmarshal(data, &box) != nil {
		warn("passwords.enc is damaged — passwords not imported")
		return ""
	}
	var plain []byte
	for attempt := 0; ; attempt++ {
		pass, err := askPassphrase("Passphrase for the exported passwords", false)
		if err != nil {
			warn("Passwords not imported: %v", err)
			return ""
		}
		if plain, err = openWithPassphrase(box, pass); err == nil {
			break
		}
		if !interactive || os.Getenv(passphraseEnv) != "" || attempt >= 2 {
			warn("Passwords not imported: %v", err)
			return ""
		}
		warn("%v — try again", err)
	}
	var entries map[string]exportedEnv
	if err := json.Unmarshal(plain, &entries); err != nil {
		warn("Passwords not imported: %v", err)
		return ""
	}
	vals, err := loadEnv()
	if err != nil {
		warn("Passwords not imported: %v", err)
		return ""
	}
	var groups []string
	for name, e := range entries {
		if !validEnvName(name) || strings.Contains(name, ".") {
			continue
		}
		delete(vals, name)
		for _, k := range envGroupKeys(vals, name) {
			delete(vals, k)
		}
		if e.Group != nil {
			for k, v := range e.Group {
				if validEnvName(k) && !strings.Contains(k, ".") {
					vals[name+"."+k] = v
				}
			}
			groups = append(groups, name)
		} else {
			vals[name] = e.Value
		}
	}
	if err := saveEnv(vals, groups...); err != nil {
		warn("Passwords not imported: %v", err)
		return ""
	}
	return fmt.Sprintf(" · %s into the env vault", plural(len(entries), "password entry"))
}

// checkImportedNodes rejects files payload didn't write: odd IDs, types, names, icons or a parent
// that isn't a folder in the file.
func checkImportedNodes(nodes []sshNode) error {
	folders := map[string]bool{}
	seen := map[string]bool{}
	for _, n := range nodes {
		if _, err := hex.DecodeString(n.ID); err != nil || len(n.ID) < 6 || len(n.ID) > 32 || seen[n.ID] {
			return fmt.Errorf("bad or repeated id %q", n.ID)
		}
		seen[n.ID] = true
		if n.Type == sshFolder {
			folders[n.ID] = true
		} else if n.Type != sshLogin {
			return fmt.Errorf("unknown entry type %q", n.Type)
		}
		if err := sshNameError(n.Name); err != nil {
			return fmt.Errorf("entry %q: %v", n.Name, err)
		}
		if n.PasswordEnv != "" && (!validEnvName(n.PasswordEnv) || strings.Contains(n.PasswordEnv, ".")) {
			return fmt.Errorf("entry %q: bad env name %q", n.Name, n.PasswordEnv)
		}
		if len([]rune(n.Icon)) > 4 {
			return fmt.Errorf("entry %q: bad icon", n.Name)
		}
		if n.Logo != "" && !validLogoFile(n.Logo) {
			return fmt.Errorf("entry %q: bad logo name %q", n.Name, n.Logo)
		}
	}
	for _, n := range nodes {
		if n.Parent != "" && !folders[n.Parent] {
			return fmt.Errorf("entry %q is in a folder that isn't in the file", n.Name)
		}
	}
	return nil
}
