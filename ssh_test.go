package main

import (
	"archive/zip"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestSSHCommandParsing(t *testing.T) {
	for _, c := range []struct{ cmd, target, jump string }{
		{"ssh root@10.0.0.5", "root@10.0.0.5", ""},
		{"ssh -p 2222 root@10.0.0.5", "root@10.0.0.5", ""},
		{"ssh root@10.0.0.5 -J admin@1.2.3.4", "root@10.0.0.5", "admin@1.2.3.4"},
		{"ssh -J admin@1.2.3.4,b@h2 -i key root@db", "root@db", "admin@1.2.3.4,b@h2"},
		{"ssh -Jadmin@1.2.3.4 root@db", "root@db", "admin@1.2.3.4"},
		{"root@db", "root@db", ""},
	} {
		args, err := sshArgs(c.cmd)
		if err != nil {
			t.Fatalf("%q: %v", c.cmd, err)
		}
		if tg, j := sshTarget(args); tg != c.target || j != c.jump {
			t.Errorf("%q → target %q jump %q, want %q %q", c.cmd, tg, j, c.target, c.jump)
		}
	}
	if checkSSHCommand("ssh -p 22") == nil || checkSSHCommand("ssh") == nil {
		t.Error("a command without a host was accepted")
	}
	for in, want := range map[string]string{"root@10.0.0.5": "10.0.0.5", "a@h:2222": "h", "[::1]:22": "::1", "host": "host", "ssh://u@h": "h"} {
		if got := hostOf(in); got != want {
			t.Errorf("hostOf(%q) = %q, want %q", in, got, want)
		}
	}
	rest, jump := splitJump("ssh root@db -J admin@1.2.3.4 -p 22")
	if jump != "admin@1.2.3.4" || rest != "ssh root@db -p 22" {
		t.Errorf("splitJump → %q %q", rest, jump)
	}
	n := sshNode{ID: "abc123", Type: sshLogin, Command: "ssh root@db", Jump: "admin@1.2.3.4"}
	if args, _ := n.args(); !reflect.DeepEqual(args, []string{"-J", "admin@1.2.3.4", "root@db"}) {
		t.Errorf("args with jump = %q", args)
	}
	if n.envName() != "ssh_abc123" {
		t.Errorf("bound env name = %q", n.envName())
	}
}

func TestAskpassChoose(t *testing.T) {
	jumps := []string{"1.2.3.4"}
	for _, c := range []struct{ prompt, want string }{
		{"admin@1.2.3.4's password: ", "ssh_x.jump"},
		{"root@10.0.0.5's password: ", "ssh_x.server"},
		{"(admin@1.2.3.4) Password: ", "ssh_x.jump"},
		{"Password: ", "ssh_x.server"},
		{"Are you sure you want to continue connecting (yes/no/[fingerprint])? ", ""},
	} {
		if got := askpassChoose(c.prompt, "10.0.0.5", jumps, "ssh_x.server", "ssh_x.jump"); got != c.want {
			t.Errorf("%q → %q, want %q", c.prompt, got, c.want)
		}
	}
	// One password for both: every password prompt gets it.
	if got := askpassChoose("admin@1.2.3.4's password: ", "10.0.0.5", jumps, "ssh_x", "ssh_x"); got != "ssh_x" {
		t.Errorf("shared password → %q", got)
	}
}

func TestEnvGroups(t *testing.T) {
	t.Setenv("PAYLOAD_STORE", t.TempDir())
	writeFile(t, envPath(), `{"localUrl":"http://x","ssh_a1b2c3":{"server":"s1","jump":"j1"}}`)
	vals, groups, err := loadEnvGroups()
	if err != nil {
		t.Fatal(err)
	}
	if vals["localUrl"] != "http://x" || vals["ssh_a1b2c3.server"] != "s1" || vals["ssh_a1b2c3.jump"] != "j1" || !groups["ssh_a1b2c3"] {
		t.Fatalf("flattened %v groups %v", vals, groups)
	}
	if out, missing := expandEnv([]byte(`{{ssh_a1b2c3.jump}} {{localUrl}}`)); string(out) != "j1 http://x" || len(missing) > 0 {
		t.Errorf("placeholders → %q %v", out, missing)
	}
	vals["ssh_a1b2c3.server"] = "s2"
	vals["other.thing"] = "flat" // no group "other": stays a plain name with a dot
	if err := saveEnv(vals); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(envPath())
	if !strings.Contains(string(raw), `"ssh_a1b2c3": {`) || !strings.Contains(string(raw), `"other.thing": "flat"`) {
		t.Errorf("saved vault:\n%s", raw)
	}
	dropEnvEntries([]string{"ssh_a1b2c3"})
	vals, _ = loadEnv()
	if _, ok := vals["ssh_a1b2c3.jump"]; ok || vals["localUrl"] != "http://x" {
		t.Errorf("after dropping the group: %v", vals)
	}
}

func TestLoginPasswordsBinding(t *testing.T) {
	t.Setenv("PAYLOAD_STORE", t.TempDir())
	n := &sshNode{ID: "a1b2c3", Type: sshLogin, Command: "ssh root@db", Jump: "admin@1.2.3.4"}
	if err := saveLoginPasswords(n, "server-pw", "jump-pw"); err != nil {
		t.Fatal(err)
	}
	env, _ := loadEnv()
	server, jump := loginPasswords(n, env, true)
	if env[server] != "server-pw" || env[jump] != "jump-pw" {
		t.Fatalf("server %q=%q jump %q=%q", server, env[server], jump, env[jump])
	}
	// Editing with empty fields keeps both.
	if err := saveLoginPasswords(n, "", ""); err != nil {
		t.Fatal(err)
	}
	if env, _ = loadEnv(); env["ssh_a1b2c3.server"] != "server-pw" || env["ssh_a1b2c3.jump"] != "jump-pw" {
		t.Errorf("passwords lost on an empty edit: %v", env)
	}
	// A new jump login with no jump password uses the server password for both.
	m := &sshNode{ID: "d4e5f6", Type: sshLogin, Command: "ssh root@db", Jump: "j@h"}
	_ = saveLoginPasswords(m, "same", "")
	if env, _ = loadEnv(); env["ssh_d4e5f6.jump"] != "same" {
		t.Errorf("jump password not defaulted: %v", env)
	}
	// Dropping the jump host turns the group back into one value.
	n.Jump = ""
	_ = saveLoginPasswords(n, "", "")
	env, _ = loadEnv()
	if env["ssh_a1b2c3"] != "server-pw" || env["ssh_a1b2c3.jump"] != "" {
		t.Errorf("after removing the jump host: %v", env)
	}
	if s, j := loginPasswords(n, env, false); s != "ssh_a1b2c3" || j != "" {
		t.Errorf("plain entry → %q %q", s, j)
	}
}

func testLogo(t *testing.T, path string) {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 80, 40))
	for y := 0; y < 40; y++ {
		for x := 0; x < 80; x++ {
			c := color.NRGBA{52, 211, 153, 255}
			if x < 40 {
				c = color.NRGBA{220, 38, 38, 255}
			}
			img.SetNRGBA(x, y, c)
		}
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}

func TestSSHStoreAndLogos(t *testing.T) {
	t.Setenv("PAYLOAD_STORE", t.TempDir())
	src := filepath.Join(t.TempDir(), "logo.png")
	testLogo(t, src)
	if checkLogoFile(src) != nil || checkLogoFile(filepath.Join(t.TempDir(), "missing.png")) == nil {
		t.Fatal("checkLogoFile")
	}
	file, err := importLogo(src, "a1b2c3")
	if err != nil || file != "a1b2c3.png" || !validLogoFile(file) {
		t.Fatalf("importLogo → %q %v", file, err)
	}
	f, _ := os.Open(sshLogoPath(file))
	cfg, _ := png.DecodeConfig(f)
	f.Close()
	if cfg.Width != logoStoreSize || cfg.Height != logoStoreSize/2 {
		t.Errorf("stored logo is %dx%d, want %dx%d (shape kept)", cfg.Width, cfg.Height, logoStoreSize, logoStoreSize/2)
	}
	lines := logoLines(file, 6, 3)
	if len(lines) != 3 || !strings.Contains(strings.Join(lines, ""), "▀") {
		t.Fatalf("logo lines %q", lines)
	}
	for _, bad := range []string{"../x.png", "a1b2c3.jpg", "zz.png", "a1b2c3"} {
		if validLogoFile(bad) {
			t.Errorf("validLogoFile(%q)", bad)
		}
	}

	s := &sshStore{Nodes: []sshNode{
		{ID: "a1b2c3", Type: sshFolder, Name: "GIBL", Logo: file},
		{ID: "b1b2b3", Type: sshFolder, Name: "Acopening", Parent: "a1b2c3"},
		{ID: "c1c2c3", Type: sshLogin, Name: "onboarding", Parent: "b1b2b3", Command: "ssh root@db"},
		{ID: "d1d2d3", Type: sshFolder, Name: "MBL", Icon: "🏦"},
	}}
	if got := s.path("c1c2c3"); got != "GIBL/Acopening/onboarding" {
		t.Errorf("path = %q", got)
	}
	if n := s.find("gibl/acopening/onboarding.ssh"); n == nil || n.ID != "c1c2c3" {
		t.Error("find by path failed")
	}
	if got := s.summary("a1b2c3"); got != "1 subfolder" {
		t.Errorf("summary = %q", got)
	}
	if kids := s.children(""); len(kids) != 2 || kids[0].Name != "GIBL" {
		t.Errorf("top level = %v", kids)
	}
	s.remove("a1b2c3")
	if len(s.Nodes) != 1 || s.Nodes[0].Name != "MBL" {
		t.Errorf("after removing GIBL: %v", s.Nodes)
	}
	if fileExists(sshLogoPath(file)) {
		t.Error("deleted client's logo file was kept")
	}
}

func TestSSHExportImport(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PAYLOAD_STORE", home)
	t.Setenv(passphraseEnv, "correct horse battery")
	src := filepath.Join(t.TempDir(), "logo.png")
	testLogo(t, src)
	logo, _ := importLogo(src, "a1b2c3")
	s := &sshStore{Nodes: []sshNode{
		{ID: "a1b2c3", Type: sshFolder, Name: "GIBL", Logo: logo},
		{ID: "c1c2c3", Type: sshLogin, Name: "onboarding", Parent: "a1b2c3", Command: "ssh root@db", Jump: "j@h"},
	}}
	if err := s.save(); err != nil {
		t.Fatal(err)
	}
	if err := saveLoginPasswords(&s.Nodes[1], "srv", "jmp"); err != nil {
		t.Fatal(err)
	}
	zipPath := filepath.Join(t.TempDir(), "out.zip")
	sshExportCmd([]string{zipPath})

	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, f := range zr.File {
		names[f.Name] = true
		rc, _ := f.Open()
		buf := make([]byte, 1<<16)
		n, _ := rc.Read(buf)
		rc.Close()
		if strings.Contains(string(buf[:n]), "srv") || strings.Contains(string(buf[:n]), "jmp") {
			t.Errorf("%s holds a password in plain text", f.Name)
		}
	}
	zr.Close()
	for _, want := range []string{"manifest.json", "ssh.json", "passwords.enc", "logos/" + logo} {
		if !names[want] {
			t.Errorf("zip lacks %s (has %v)", want, names)
		}
	}

	// Another computer: empty, plus an unrelated "GIBL" that clashes by name only.
	other := t.TempDir()
	t.Setenv("PAYLOAD_STORE", other)
	clash := &sshStore{Nodes: []sshNode{{ID: "ffffff", Type: sshFolder, Name: "GIBL", Icon: "🏢"}}}
	_ = clash.save()
	sshImportCmd([]string{zipPath, "-y"})
	got := mustLoadSSH()
	if n := got.get("c1c2c3"); n == nil || n.Command != "ssh root@db" || n.Jump != "j@h" {
		t.Fatalf("imported login: %+v", n)
	}
	if n := got.get("a1b2c3"); n == nil || n.Name != "GIBL-2" || n.Logo != logo || !fileExists(sshLogoPath(logo)) {
		t.Errorf("imported folder: %+v (logo file %v)", n, fileExists(sshLogoPath(logo)))
	}
	env, _ := loadEnv()
	if env["ssh_c1c2c3.server"] != "srv" || env["ssh_c1c2c3.jump"] != "jmp" {
		t.Errorf("imported passwords: %v", env)
	}

	// The wrong passphrase decrypts nothing.
	data, _ := os.ReadFile(zipPath)
	zr2, _ := zip.NewReader(strings.NewReader(string(data)), int64(len(data)))
	for _, f := range zr2.File {
		if f.Name == "passwords.enc" {
			rc, _ := f.Open()
			raw := make([]byte, 1<<16)
			n, _ := rc.Read(raw)
			rc.Close()
			var box sealedBox
			if err := json.Unmarshal(raw[:n], &box); err != nil {
				t.Fatal(err)
			}
			if _, err := openWithPassphrase(box, "wrong"); err == nil {
				t.Error("wrong passphrase accepted")
			}
		}
	}

	if checkImportedNodes([]sshNode{{ID: "zz", Type: sshFolder, Name: "x"}}) == nil ||
		checkImportedNodes([]sshNode{{ID: "a1b2c3", Type: sshLogin, Name: "x", Parent: "999999"}}) == nil ||
		checkImportedNodes([]sshNode{{ID: "a1b2c3", Type: sshFolder, Name: "x", Logo: "../evil.png"}}) == nil {
		t.Error("a hostile export was accepted")
	}
}
