package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/atotto/clipboard"
)

// payload ssh: saved ssh logins in folders. Folders ("clients", e.g. folder1 → subfolder1) nest; a login
// (shown as onboarding.ssh) is an ssh command plus the env vault names of its passwords. Opening a
// login copies its password to the clipboard and runs ssh — nothing is typed for you. Stored in
// vault/ssh.json (no passwords: those stay in the env vault).

type sshNode struct {
	ID     string `json:"id"`
	Num    int    `json:"num,omitempty"`    // ssh logins: 1, 2, 3… in creation order (folders have none) — payload ssh <num>
	Parent string `json:"parent,omitempty"` // "" for a top-level folder
	Type   string `json:"type"`             // "folder" or "ssh"
	Name   string `json:"name"`
	Icon   string `json:"icon,omitempty"` // top-level folders: an emoji…
	Logo   string `json:"logo,omitempty"` // …or an image, vault/ssh-logos/<id>.png (see sshlogo.go)

	Command     string `json:"command,omitempty"`      // ssh logins: e.g. ssh root@10.0.0.5
	Jump        string `json:"jump,omitempty"`         // jump host(s) for -J, e.g. admin@1.2.3.4
	PasswordEnv string `json:"password_env,omitempty"` // env vault name; "" means the bound ssh_<id>

	// Jumpable logins can be picked as the jump host of other logins. JumpVia is such a login's ID:
	// its host (and port) become this login's -J, and its password the jump password — looked up
	// each time, so changing the jump login updates every login that goes through it.
	Jumpable bool   `json:"jumpable,omitempty"`
	JumpVia  string `json:"jump_via,omitempty"`
}

// envName is the env vault entry holding the login's password(s): bound to the login's ID
// (ssh_<id>) unless the file names another one.
func (n *sshNode) envName() string {
	if n.PasswordEnv != "" {
		return n.PasswordEnv
	}
	return "ssh_" + n.ID
}

// args is the login's ssh arguments, with the jump host as -J (options go before the host).
func (n *sshNode) args() ([]string, error) { return n.argsDepth(0) }

func (n *sshNode) argsDepth(depth int) ([]string, error) {
	args, err := sshArgs(n.Command)
	if err != nil {
		return nil, err
	}
	jump := n.Jump
	if n.JumpVia != "" {
		if jump, err = jumpSpec(n.JumpVia, depth); err != nil {
			return nil, err
		}
	}
	if jump != "" {
		if _, j := sshTarget(args); j == "" {
			args = append([]string{"-J", jump}, args...)
		}
	}
	return args, nil
}

// jumpLogin is the login another one jumps through (nil if it was deleted).
func jumpLogin(id string) *sshNode {
	s, err := loadSSH()
	if err != nil {
		return nil
	}
	if j := s.get(id); j != nil && j.Type == sshLogin {
		return j
	}
	return nil
}

// jumpSpec is the -J value for jumping through the login with this ID: its user@host, with :port
// if it uses -p, after its own jump hosts if it has any.
func jumpSpec(id string, depth int) (string, error) {
	if depth > 4 {
		return "", errors.New("its jump hosts go round in a loop — edit it (e) to pick another")
	}
	j := jumpLogin(id)
	if j == nil {
		return "", errors.New("the login it jumps through was deleted — edit it (e) to pick another")
	}
	args, err := j.argsDepth(depth + 1)
	if err != nil {
		return "", err
	}
	target, chain := sshTarget(args)
	if target == "" {
		return "", errors.New("the login it jumps through has no host")
	}
	if p := sshPort(args); p != "" {
		target += ":" + p
	}
	if chain != "" {
		target = chain + "," + target
	}
	return target, nil
}

// sshPort is the -p value in ssh arguments, if any.
func sshPort(args []string) string {
	for i, a := range args {
		switch {
		case a == "--":
			return ""
		case a == "-p" && i+1 < len(args):
			return args[i+1]
		case strings.HasPrefix(a, "-p") && len(a) > 2:
			return a[2:]
		}
	}
	return ""
}

// splitJump takes a -J value out of a command, so it is stored once in the Jump field.
func splitJump(command string) (rest, jump string) {
	words, err := splitCommand(command)
	if err != nil {
		return command, ""
	}
	var kept []string
	for i := 0; i < len(words); i++ {
		w := words[i]
		switch {
		case w == "-J" && i+1 < len(words):
			jump = words[i+1]
			i++
		case strings.HasPrefix(w, "-J") && len(w) > 2:
			jump = w[2:]
		default:
			kept = append(kept, w)
		}
	}
	if jump == "" {
		return command, ""
	}
	return quoteWords(kept), jump
}

// loginPasswords resolves a login's env vault entry. With a jump host the entry is a group holding
// both passwords ({"server": "…", "jump": "…"} → name.server, name.jump); a plain value is used for
// the server and the jump host alike. Names are "" when there's no entry.
func loginPasswords(n *sshNode, env map[string]string, hasJump bool) (serverName, jumpName string) {
	name := n.envName()
	if n.JumpVia != "" {
		// The jump password is the jump login's own (server) password.
		serverName = name
		if _, ok := env[name]; !ok {
			if _, ok := env[name+".server"]; ok {
				serverName = name + ".server"
			}
		}
		if hasJump {
			if j := jumpLogin(n.JumpVia); j != nil && j.ID != n.ID {
				jumpName, _ = loginPasswords(j, env, false)
			}
		}
		return serverName, jumpName
	}
	if _, ok := env[name]; ok {
		if hasJump {
			return name, name
		}
		return name, ""
	}
	serverName = name + ".server"
	if hasJump {
		jumpName = name + ".jump"
	}
	return serverName, jumpName
}

const (
	sshFolder = "folder"
	sshLogin  = "ssh"
)

type sshStore struct {
	Nodes []sshNode `json:"nodes"`
}

// sshIcons are the folder icons to pick from: wide emoji only, so table columns stay aligned.
var sshIcons = []string{"📁", "🏦", "🏢", "🏭", "🏥", "💳", "🔐", "🌐", "🚀", "🧪", "📦", "💾", "🐳", "🔥", "⭐", "🔧"}

func sshPath() string {
	return filepath.Join(storeDir(), "vault", "ssh.json")
}

func loadSSH() (*sshStore, error) {
	s := &sshStore{}
	data, err := os.ReadFile(sshPath())
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, s); err != nil {
		return nil, fmt.Errorf("%s: %v", sshPath(), err)
	}
	if s.numberLogins() {
		_ = s.save() // logins saved before numbers existed get theirs once, in file order
	}
	return s, nil
}

// numberLogins gives every ssh login a unique number: kept once given, new ones (or a clash, e.g.
// after an import) get the next after the highest. Folders get none. It reports whether it changed
// anything.
func (s *sshStore) numberLogins() bool {
	top := 0
	for _, n := range s.Nodes {
		if n.Type == sshLogin {
			top = max(top, n.Num)
		}
	}
	changed, seen := false, map[int]bool{}
	for i := range s.Nodes {
		n := &s.Nodes[i]
		switch {
		case n.Type != sshLogin:
			if n.Num != 0 {
				n.Num, changed = 0, true
			}
		case n.Num <= 0 || seen[n.Num]:
			top++
			n.Num, changed = top, true
			seen[n.Num] = true
		default:
			seen[n.Num] = true
		}
	}
	return changed
}

// byNum finds a login by its number.
func (s *sshStore) byNum(num int) *sshNode {
	for i := range s.Nodes {
		if s.Nodes[i].Type == sshLogin && s.Nodes[i].Num == num {
			return &s.Nodes[i]
		}
	}
	return nil
}

// findLogin resolves what was typed after payload ssh: a folder/login path, or a login's number.
func (s *sshStore) findLogin(arg string) *sshNode {
	if n := s.find(arg); n != nil {
		return n
	}
	if num, err := strconv.Atoi(strings.TrimPrefix(arg, "#")); err == nil {
		return s.byNum(num)
	}
	return nil
}

func mustLoadSSH() *sshStore {
	s, err := loadSSH()
	if err != nil {
		fail("cannot read ssh logins: %v", err)
	}
	return s
}

func (s *sshStore) save() error {
	s.numberLogins()
	if err := os.MkdirAll(filepath.Dir(sshPath()), 0o755); err != nil {
		return err
	}
	data, _ := json.MarshalIndent(s, "", "  ")
	return os.WriteFile(sshPath(), append(data, '\n'), 0o644)
}

func (s *sshStore) get(id string) *sshNode {
	for i := range s.Nodes {
		if s.Nodes[i].ID == id {
			return &s.Nodes[i]
		}
	}
	return nil
}

// children lists a folder's contents ("" = top level): folders first, then logins, by name.
func (s *sshStore) children(parent string) []sshNode {
	var out []sshNode
	for _, n := range s.Nodes {
		if n.Parent == parent {
			out = append(out, n)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Type != out[j].Type {
			return out[i].Type == sshFolder
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out
}

// path is a node's location, e.g. folder1/subfolder1/onboarding.
func (s *sshStore) path(id string) string {
	var parts []string
	for n := s.get(id); n != nil; n = s.get(n.Parent) {
		parts = append([]string{n.Name}, parts...)
		if len(parts) > 64 {
			break // a broken file with a loop
		}
	}
	return strings.Join(parts, "/")
}

// find resolves a path like folder1/subfolder1/onboarding (a trailing .ssh is allowed).
func (s *sshStore) find(path string) *sshNode {
	parent := ""
	var node *sshNode
	for _, part := range strings.Split(strings.Trim(path, "/"), "/") {
		node = nil
		for i := range s.Nodes {
			n := &s.Nodes[i]
			if n.Parent == parent && (strings.EqualFold(n.Name, part) || (n.Type == sshLogin && strings.EqualFold(n.Name+".ssh", part))) {
				node = n
				break
			}
		}
		if node == nil {
			return nil
		}
		parent = node.ID
	}
	return node
}

// folders lists every folder with its path, for the "parent" picker.
func (s *sshStore) folders() []sshNode {
	var out []sshNode
	for _, n := range s.Nodes {
		if n.Type == sshFolder {
			out = append(out, n)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return strings.ToLower(s.path(out[i].ID)) < strings.ToLower(s.path(out[j].ID)) })
	return out
}

// descendants counts what deleting id would remove (itself included).
func (s *sshStore) descendants(id string) []string {
	ids := []string{id}
	for i := 0; i < len(ids); i++ {
		for _, n := range s.Nodes {
			if n.Parent == ids[i] {
				ids = append(ids, n.ID)
			}
		}
	}
	return ids
}

func (s *sshStore) remove(id string) {
	gone := map[string]bool{}
	for _, d := range s.descendants(id) {
		gone[d] = true
		if n := s.get(d); n != nil && n.Logo != "" {
			_ = os.Remove(sshLogoPath(n.Logo))
		}
	}
	kept := s.Nodes[:0]
	for _, n := range s.Nodes {
		if !gone[n.ID] {
			kept = append(kept, n)
		}
	}
	s.Nodes = kept
}

// nameTaken reports whether parent already has another entry called name.
func (s *sshStore) nameTaken(parent, name, except string) bool {
	for _, n := range s.Nodes {
		if n.Parent == parent && n.ID != except && strings.EqualFold(n.Name, name) {
			return true
		}
	}
	return false
}

func sshNameError(name string) error {
	if name == "" {
		return errors.New("enter a name")
	}
	if strings.ContainsAny(name, `/\`) || name == "." || name == ".." {
		return errors.New(`no / or \ in names`)
	}
	if len([]rune(name)) > 40 {
		return errors.New("40 characters at most")
	}
	return nil
}

func newSSHID() string { return randomHex(6) }

// summary describes a folder's contents, e.g. "2 subfolders · 1 login".
func (s *sshStore) summary(id string) string {
	folders, logins := 0, 0
	for _, n := range s.Nodes {
		if n.Parent == id {
			if n.Type == sshFolder {
				folders++
			} else {
				logins++
			}
		}
	}
	var parts []string
	if folders > 0 {
		parts = append(parts, plural(folders, "subfolder"))
	}
	if logins > 0 {
		parts = append(parts, plural(logins, "login"))
	}
	if len(parts) == 0 {
		return "empty"
	}
	return strings.Join(parts, " · ")
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}

// --- ssh commands -------------------------------------------------------------------------------

// sshArgs splits a saved command into ssh's arguments (a leading "ssh" is dropped).
func sshArgs(command string) ([]string, error) {
	words, err := splitCommand(command)
	if err != nil {
		return nil, err
	}
	if len(words) > 0 {
		base := strings.ToLower(filepath.Base(words[0]))
		if base == "ssh" || base == "ssh.exe" {
			words = words[1:]
		}
	}
	if len(words) == 0 {
		return nil, errors.New("no host — e.g. ssh root@10.0.0.5")
	}
	return words, nil
}

// sshTarget finds the destination (user@host) and the -J jump hosts in ssh arguments.
func sshTarget(args []string) (target, jump string) {
	takesValue := "BbcDEeFIiJLlmOoPpQRSWw"
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			if target == "" && i+1 < len(args) {
				target = args[i+1]
			}
			return target, jump
		case strings.HasPrefix(a, "-J"):
			if a == "-J" && i+1 < len(args) {
				jump = args[i+1]
				i++
			} else {
				jump = a[2:]
			}
		case strings.HasPrefix(a, "-") && len(a) == 2 && strings.ContainsRune(takesValue, rune(a[1])):
			i++ // option with a separate value
		case strings.HasPrefix(a, "-"):
		case target == "":
			target = a // OpenSSH also takes options after the host: keep reading them
		default:
			return target, jump // the remote command starts here
		}
	}
	return target, jump
}

// checkSSHCommand validates a command for the editor: it must split and name a host.
func checkSSHCommand(command string) error {
	args, err := sshArgs(command)
	if err != nil {
		return err
	}
	if t, _ := sshTarget(args); t == "" {
		return errors.New("no host — e.g. ssh root@10.0.0.5")
	}
	return nil
}

func sshStep(format string, a ...any) {
	fmt.Fprintln(os.Stderr, styleClients.Render("● ")+fmt.Sprintf(format, a...))
}

// sshConnect runs ssh in this terminal. At each password prompt (the jump host's, then the
// server's) payload copies the matching password from the env vault to the clipboard and shows the
// prompt for you to paste into — see sshaskpass.go.
func sshConnect(s *sshStore, n *sshNode, extra []string) {
	args, err := n.args()
	if err != nil {
		fail("%s: %v", s.path(n.ID), err)
	}
	args = append(args, extra...)
	bin, source, err := findSSH()
	if err != nil {
		fail("%v", err)
	}
	target, jump := sshTarget(args)
	sshStep("Opening %s", styleTitle.Render(s.path(n.ID)))

	env, _ := loadEnv()
	serverEnv, jumpEnv := loginPasswords(n, env, jump != "")
	check := func(name, what string) string {
		if name == "" {
			return ""
		}
		v, ok := env[name]
		switch {
		case !ok:
			sshStep("%s %s isn't saved — type it when ssh asks (or add it with e in payload ssh table)", styleWarn.Render("!"), what)
		default:
			sshStep("%s saved %s — copied to the clipboard when ssh asks for it", what, styleMuted.Render("(env "+name+")"))
		}
		return v
	}
	jumpPW := ""
	if jump != "" && jumpEnv != serverEnv {
		jumpPW = check(jumpEnv, "Jump host password")
	}
	what := "Server password"
	if jump != "" && jumpEnv == serverEnv {
		what = "Password (server and jump host)"
	}
	pw := check(serverEnv, what)
	sshStep("Running %s %s", styleCode.Render("ssh "+quoteWords(args)), styleMuted.Render("("+source+" ssh)"))
	if target == "" {
		sshStep("%s no host found in the command", styleWarn.Render("!"))
	}

	cmd := exec.Command(bin, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if serverEnv != "" || jumpEnv != "" {
		if self, err := os.Executable(); err == nil {
			var jumpHosts []string
			for _, j := range strings.Split(jump, ",") {
				jumpHosts = append(jumpHosts, hostOf(j))
			}
			cmd.Env = append(os.Environ(),
				"SSH_ASKPASS="+self, "SSH_ASKPASS_REQUIRE=force", askpassEnv+"=1",
				"PAYLOAD_SSH_TARGET="+hostOf(target), "PAYLOAD_SSH_JUMPS="+strings.Join(jumpHosts, ","),
				"PAYLOAD_SSH_PW_ENV="+serverEnv, "PAYLOAD_SSH_JUMP_ENV="+jumpEnv, "PAYLOAD_SSH_LOGIN="+s.path(n.ID))
		}
	}
	err = cmd.Run()
	// Don't leave a password on the clipboard after the session.
	if cur, cerr := clipboard.ReadAll(); cerr == nil && cur != "" && (cur == pw || cur == jumpPW) {
		_ = clipboard.WriteAll("")
		sshStep("Cleared the password from the clipboard")
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		os.Exit(exit.ExitCode())
	}
	if err != nil {
		fail("could not run ssh: %v", err)
	}
}

// --- CLI ----------------------------------------------------------------------------------------

func sshCmd(args []string) {
	sub := ""
	if len(args) > 0 {
		sub = args[0]
	}
	switch sub {
	case "":
		sshPickCmd()
	case "table", "clients":
		sshTableCmd()
	case "new", "add":
		sshNewCmd()
	case "export":
		sshExportCmd(args[1:])
	case "import":
		sshImportCmd(args[1:])
	case "path":
		sshPathCmd()
	case "install":
		sshInstallCmd()
	case "list":
		s := mustLoadSSH()
		var logins []sshNode
		for _, n := range s.Nodes {
			if n.Type == sshLogin {
				logins = append(logins, n)
			}
		}
		sort.Slice(logins, func(i, j int) bool { return logins[i].Num < logins[j].Num })
		for _, n := range logins {
			fmt.Printf("%d\t%s\n", n.Num, s.path(n.ID))
		}
	case "show":
		if len(args) != 2 {
			fail("usage: payload ssh show <folder/login>")
		}
		s := mustLoadSSH()
		n := s.findLogin(args[1])
		if n == nil || n.Type != sshLogin {
			fail("no ssh login %q — run payload ssh list", args[1])
		}
		args, _ := n.args()
		fmt.Println("ssh " + quoteWords(args))
		info("#%d %s", n.Num, s.path(n.ID))
		info("passwords: env vault %s", n.envName())
	default:
		s := mustLoadSSH()
		n := s.findLogin(sub)
		switch {
		case n == nil:
			fail("no ssh login %q — run payload ssh list, or payload ssh table", sub)
		case n.Type == sshFolder:
			fail("%s is a folder — give a login inside it, e.g. %s/<name> (payload ssh list)", sub, strings.Trim(sub, "/"))
		}
		// payload ssh <login> -p copies the login's password instead of connecting. (-p with a value,
		// e.g. -p 2222, is still ssh's port option.)
		if len(args) == 2 && (args[1] == "-p" || args[1] == "--password") {
			msg := copyLoginPassword(*n)
			fmt.Println(msg + styleMuted.Render(fmt.Sprintf("  (#%d %s)", n.Num, s.path(n.ID))))
			if !strings.Contains(msg, "✓") {
				os.Exit(1)
			}
			return
		}
		sshConnect(s, n, args[1:])
	}
}

// jumpUsers lists the logins outside id's subtree that jump through a login inside it.
func (s *sshStore) jumpUsers(id string) []string {
	gone := map[string]bool{}
	for _, d := range s.descendants(id) {
		gone[d] = true
	}
	var out []string
	for _, n := range s.Nodes {
		if n.Type == sshLogin && n.JumpVia != "" && gone[n.JumpVia] && !gone[n.ID] {
			out = append(out, n.ID)
		}
	}
	return out
}
