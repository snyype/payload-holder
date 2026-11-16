package main

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
)

// payload ssh table: folders (clients such as folder1 → subfolder1) and ssh logins in a table.
// enter opens a folder or connects to a login, backspace goes up, n adds a folder, s adds a login,
// e edits, p copies a login's password, c copies its command, d deletes.

var (
	sshCyan       = lipgloss.AdaptiveColor{Light: "#0E7490", Dark: "#67E8F9"}
	styleSSHBadge = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#042F2E")).Background(lipgloss.Color("#67E8F9")).Padding(0, 1)
)

const sshLoginIcon = "💻"

func sshTableCmd() {
	s := mustLoadSSH()
	if !interactive {
		printSSHTree(s, "", "")
		return
	}
	m := sshTableModel{}
	for {
		m.store = mustLoadSSH()
		m.rows = m.store.children(m.folder)
		m.cursor = min(m.cursor, max(0, len(m.rows)-1))
		res, err := tea.NewProgram(m).Run()
		if err != nil {
			fail("%v", err)
		}
		m = res.(sshTableModel)
		action, target := m.action, m.target
		m.action, m.target, m.status = "", "", ""
		switch action {
		case "connect":
			s := mustLoadSSH()
			if n := s.get(target); n != nil {
				sshConnect(s, n, nil)
			}
			return
		case "new_folder":
			m.status = sshFolderForm(m.store, nil, m.folder)
		case "new_login":
			m.status = sshLoginForm(m.store, nil, m.folder)
		case "edit":
			if n := m.store.get(target); n != nil {
				if n.Type == sshFolder {
					m.status = sshFolderForm(m.store, n, n.Parent)
				} else {
					m.status = sshLoginForm(m.store, n, n.Parent)
				}
			}
		default:
			return // quit
		}
	}
}

// printSSHTree lists everything as an indented tree (piped output).
func printSSHTree(s *sshStore, parent, indent string) {
	for _, n := range s.children(parent) {
		if n.Type == sshFolder {
			icon := firstNonEmpty(n.Icon, "📁")
			if n.Logo != "" {
				icon = "🖼"
			}
			fmt.Printf("%s%s %s/  (%s)\n", indent, icon, n.Name, s.summary(n.ID))
			printSSHTree(s, n.ID, indent+"  ")
		} else {
			fmt.Printf("%s%s %s.ssh  %s\n", indent, sshLoginIcon, n.Name, n.Command)
		}
	}
}

type sshTableModel struct {
	store  *sshStore
	folder string // current folder ("" = top level)
	rows   []sshNode
	cursor int
	status string

	action, target string
	confirmDrop    string
}

func (m sshTableModel) Init() tea.Cmd { return nil }

func (m sshTableModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	if k.String() == "ctrl+c" {
		return m, tea.Quit
	}
	if m.confirmDrop != "" {
		id := m.confirmDrop
		m.confirmDrop = ""
		if !pressed(k, "table", "confirm_delete") {
			m.status = styleMuted.Render("Not deleted.")
			return m, nil
		}
		name := m.store.path(id)
		var envs []string
		for _, d := range m.store.descendants(id) {
			if n := m.store.get(d); n != nil && n.Type == sshLogin && n.PasswordEnv == "" {
				envs = append(envs, n.envName())
			}
		}
		m.store.remove(id)
		dropEnvEntries(envs)
		if err := m.store.save(); err != nil {
			m.status = styleErr.Render("✗ could not delete: " + err.Error())
			return m, nil
		}
		m.rows = m.store.children(m.folder)
		m.cursor = min(m.cursor, max(0, len(m.rows)-1))
		m.status = styleOK.Render("✓ Deleted " + name)
		return m, nil
	}
	m.status = ""
	switch {
	case pressed(k, "ssh_table", "back") || (k.String() == "esc" && m.folder != ""):
		if m.folder != "" {
			from := m.folder
			if f := m.store.get(m.folder); f != nil {
				m.folder = f.Parent
			} else {
				m.folder = ""
			}
			m.rows = m.store.children(m.folder)
			m.cursor = 0
			for i, r := range m.rows {
				if r.ID == from {
					m.cursor = i
				}
			}
		}
	case pressed(k, "table", "quit"):
		return m, tea.Quit
	case pressed(k, "ssh_table", "new_folder"):
		m.action = "new_folder"
		return m, tea.Quit
	case pressed(k, "ssh_table", "new_login"):
		m.action = "new_login"
		return m, tea.Quit
	case len(m.rows) == 0:
	case pressed(k, "ssh_table", "open"):
		r := m.rows[m.cursor]
		if r.Type == sshFolder {
			m.folder, m.rows, m.cursor = r.ID, m.store.children(r.ID), 0
			return m, nil
		}
		m.action, m.target = "connect", r.ID
		return m, tea.Quit
	case pressed(k, "ssh_table", "edit"):
		m.action, m.target = "edit", m.rows[m.cursor].ID
		return m, tea.Quit
	case pressed(k, "table", "delete"):
		m.confirmDrop = m.rows[m.cursor].ID
	case pressed(k, "ssh_table", "copy_password"):
		m.status = copyLoginPassword(m.rows[m.cursor])
	case pressed(k, "ssh_table", "toggle_jumpable"):
		m.status = m.toggleJumpable()
	case pressed(k, "table", "copy"):
		r := m.rows[m.cursor]
		if r.Type == sshLogin {
			if err := copyText(r.Command); err != nil {
				m.status = styleErr.Render("✗ " + err.Error())
			} else {
				m.status = styleOK.Render("✓ Copied the command: " + r.Command)
			}
		}
	case pressed(k, "table", "up"):
		if m.cursor > 0 {
			m.cursor--
		}
	case pressed(k, "table", "down"):
		if m.cursor < len(m.rows)-1 {
			m.cursor++
		}
	case pressed(k, "table", "first"):
		m.cursor = 0
	case pressed(k, "table", "last"):
		m.cursor = len(m.rows) - 1
	}
	return m, nil
}

// copyLoginPassword copies a login's server password (from the env vault) and says which it was.
func copyLoginPassword(n sshNode) string {
	if n.Type != sshLogin {
		return ""
	}
	env, _ := loadEnv()
	args, _ := n.args()
	_, jump := sshTarget(args)
	name, _ := loginPasswords(&n, env, jump != "")
	v, ok := env[name]
	if !ok {
		return styleWarn.Render("! no password saved for " + n.Name + " — e to edit it")
	}
	if err := copyText(v); err != nil {
		return styleErr.Render("✗ " + err.Error())
	}
	return styleOK.Render("✓ Copied {{" + name + "}} to the clipboard")
}

func (m sshTableModel) breadcrumb() string {
	if m.folder == "" {
		return "clients"
	}
	parts := strings.Split(m.store.path(m.folder), "/")
	return "clients / " + strings.Join(parts, " / ")
}

func (m sshTableModel) View() string {
	if m.action != "" {
		return ""
	}
	var b strings.Builder
	b.WriteString(styleSSHBadge.Render("ssh") + "  " + styleTitle.Render(m.breadcrumb()) + "\n")
	if len(m.rows) == 0 {
		if m.folder == "" {
			b.WriteString(styleMuted.Render("  No clients yet. Press n to add one (e.g. folder1), then s inside it to add an ssh login.") + "\n")
		} else {
			b.WriteString(styleMuted.Render("  Empty folder. n adds a subfolder, s adds an ssh login here.") + "\n")
		}
	} else {
		// One line per entry: marker, icon (a logo is drawn 2 cells × 1 line, like an emoji), the
		// name, then what's inside a folder or where a login goes. Names line up in one column.
		env, _ := loadEnv()
		hl := lipgloss.NewStyle().Foreground(sshCyan).Bold(true)
		names := make([]string, len(m.rows))
		nameW, numW := 0, 1
		for i, n := range m.rows {
			names[i] = n.Name
			if n.Type == sshFolder {
				names[i] += "/"
			} else {
				names[i] += ".ssh"
				numW = max(numW, len(strconv.Itoa(n.Num)))
			}
			nameW = max(nameW, lipgloss.Width(names[i]))
		}
		b.WriteString("\n")
		for i, n := range m.rows {
			marker, name := "  ", names[i]
			if i == m.cursor {
				marker, name = hl.Render("▌ "), hl.Render(name)
			}
			pad := strings.Repeat(" ", nameW-lipgloss.Width(names[i]))
			num, icon, details := strings.Repeat(" ", numW), sshLoginIcon, ""
			if n.Type == sshFolder {
				icon, details = folderIcon(n), m.store.summary(n.ID)
			} else {
				num, details = fmt.Sprintf("%*d", numW, n.Num), loginDetails(n, env)
			}
			b.WriteString(marker + styleMuted.Render(num) + "  " + icon + " " + name + pad + "   " + styleMuted.Render(details) + "\n")
		}
		b.WriteString("\n")
	}
	l := func(a string) string { return keyLabel("table", a) }
	c := func(a string) string { return keyLabel("ssh_table", a) }
	help := fmt.Sprintf("%s open/connect · %s back · %s new folder · %s new ssh login · %s edit · %s copy password · %s jumpable · %s delete · %s quit",
		c("open"), c("back"), c("new_folder"), c("new_login"), c("edit"), c("copy_password"), c("toggle_jumpable"), l("delete"), l("quit"))
	b.WriteString("  " + styleMuted.Render(help) + "\n")
	if m.confirmDrop != "" {
		what := m.store.path(m.confirmDrop)
		if n := len(m.store.descendants(m.confirmDrop)) - 1; n > 0 {
			what += fmt.Sprintf(" and the %d entries inside it", n)
		}
		if users := m.store.jumpUsers(m.confirmDrop); len(users) > 0 {
			what += fmt.Sprintf(" (%s jump through it and will need another jump host)", plural(len(users), "login"))
		}
		b.WriteString(styleWarn.Render("Delete "+what+"? ") + styleMuted.Render(l("confirm_delete")+" to delete · any other key cancels") + "\n")
	} else {
		b.WriteString(m.status + "\n")
	}
	return b.String()
}

// loginDetails is the table's description of a login: where it goes and which passwords it uses.
func loginDetails(n sshNode, env map[string]string) string {
	args, err := n.args()
	if err != nil {
		return "! " + err.Error()
	}
	target, jump := sshTarget(args)
	s := target
	if jump != "" {
		s += " via " + jump
		if j := jumpLogin(n.JumpVia); n.JumpVia != "" && j != nil {
			s += fmt.Sprintf(" (#%d)", j.Num)
		}
	}
	if n.Jumpable {
		s += " · jumpable"
	}
	serverName, jumpName := loginPasswords(&n, env, jump != "")
	mark := func(name string) string {
		if _, ok := env[name]; ok {
			return "✓"
		}
		return "none"
	}
	if jump != "" && jumpName != serverName {
		return s + fmt.Sprintf(" · password %s · jump password %s", mark(serverName), mark(jumpName))
	}
	return s + " · password " + mark(serverName)
}

// --- forms --------------------------------------------------------------------------------------

// sshFolderForm adds a folder (edit == nil) or edits one, and returns the status line.
func sshFolderForm(s *sshStore, edit *sshNode, parent string) string {
	const useImage = "image"
	name, icon, top, logoSrc := "", "📁", parent == "", ""
	if edit != nil {
		name, icon, top = edit.Name, firstNonEmpty(edit.Icon, "📁"), edit.Parent == ""
		if edit.Logo != "" {
			icon = useImage
		}
	}
	keepLogo := edit != nil && edit.Logo != ""
	folders := s.folders()
	var parentOpts []huh.Option[string]
	for _, f := range folders {
		if edit != nil && containsID(s.descendants(edit.ID), f.ID) {
			continue // can't move a folder into itself
		}
		parentOpts = append(parentOpts, huh.NewOption("📁 "+s.path(f.ID), f.ID))
	}
	if parent == "" && len(parentOpts) > 0 {
		parent = parentOpts[0].Value
	}
	iconOpts := []huh.Option[string]{huh.NewOption("🖼  Image file (PNG or JPEG logo)", useImage)}
	for _, ic := range sshIcons {
		iconOpts = append(iconOpts, huh.NewOption(ic, ic))
	}
	iconOpts, parentOpts = chosenFirst(iconOpts, icon), chosenFirst(parentOpts, parent)
	title := "New client folder"
	if edit != nil {
		title = "Edit " + s.path(edit.ID)
	}
	groups := []*huh.Group{
		huh.NewGroup(
			huh.NewInput().Title(title).Description("Folder name, e.g. folder1 or subfolder1").Value(&name).
				Validate(func(v string) error { return sshNameError(strings.TrimSpace(v)) }),
			huh.NewConfirm().Title("Mark as parent (top-level client)?").
				Description("A parent shows on the first page of payload ssh table, with its icon.").
				Affirmative("Parent").Negative("Inside another folder").Value(&top),
		),
		huh.NewGroup(
			huh.NewSelect[string]().Title("Icon").Description("Shown next to the client in payload ssh table").
				Options(iconOpts...).Height(selectHeight(len(iconOpts), "Shown next to the client in payload ssh table")).Value(&icon),
		).WithHideFunc(func() bool { return !top }),
		huh.NewGroup(
			huh.NewInput().Title("Logo image").
				DescriptionFunc(func() string {
					d := "Path of a PNG or JPEG, e.g. D:\\logos\\logo.png — payload keeps a small copy"
					if keepLogo {
						d += " · leave empty to keep the current logo"
					}
					return d
				}, &icon).
				Value(&logoSrc).Validate(func(v string) error {
				if strings.TrimSpace(v) == "" && keepLogo {
					return nil
				}
				return checkLogoFile(v)
			}),
		).WithHideFunc(func() bool { return !top || icon != useImage }),
	}
	if len(parentOpts) > 0 {
		groups = append(groups, huh.NewGroup(
			huh.NewSelect[string]().Title("Parent client").Description("The folder this one goes inside").
				Options(parentOpts...).Height(selectHeight(len(parentOpts), "The folder this one goes inside")).Value(&parent),
		).WithHideFunc(func() bool { return top }))
	}
	if err := huh.NewForm(groups...).WithTheme(theme()).WithShowHelp(true).Run(); err != nil {
		return styleMuted.Render("Nothing saved.")
	}
	name = strings.TrimSpace(name)
	if top || len(parentOpts) == 0 {
		parent = ""
	}
	except := ""
	if edit != nil {
		except = edit.ID
	}
	if s.nameTaken(parent, name, except) {
		return styleErr.Render("✗ " + name + " already exists there")
	}
	node := edit
	if node == nil {
		s.Nodes = append(s.Nodes, sshNode{ID: newSSHID(), Type: sshFolder})
		node = &s.Nodes[len(s.Nodes)-1]
	}
	node.Name, node.Parent, node.Icon = name, parent, ""
	switch {
	case top && icon == useImage && strings.TrimSpace(logoSrc) != "":
		file, err := importLogo(logoSrc, node.ID)
		if err != nil {
			return styleErr.Render("✗ could not use the logo: " + err.Error())
		}
		node.Logo = file
	case top && icon == useImage:
		// keep the current logo
	default:
		if node.Logo != "" {
			_ = os.Remove(sshLogoPath(node.Logo))
			node.Logo = ""
		}
		if top {
			node.Icon = icon
		}
	}
	if err := s.save(); err != nil {
		return styleErr.Render("✗ could not save: " + err.Error())
	}
	return styleOK.Render("✓ Saved " + s.path(node.ID))
}

func firstTopLevel(s *sshStore, id string) string {
	for n := s.get(id); n != nil; n = s.get(n.Parent) {
		if n.Parent == "" {
			return n.ID
		}
	}
	return id
}

func containsID(ids []string, id string) bool {
	for _, x := range ids {
		if x == id {
			return true
		}
	}
	return false
}

// sshLoginForm adds an ssh login (edit == nil) or edits one. It asks for the password, and — when
// "through a jump host" is ticked — the jump host and its password; both go into the env vault
// entry bound to the login's ID (ssh_<id>). Returns the status line.
func sshLoginForm(s *sshStore, edit *sshNode, parent string) string {
	folders := s.folders()
	if len(folders) == 0 {
		return styleWarn.Render("! Add a client folder first (n), then add ssh logins inside it.")
	}
	if parent == "" {
		parent = folders[0].ID
	}
	var name, command, jumpHost, pw, jumpPW, via string
	jumpable := false
	if edit != nil {
		name, command, jumpHost, via, jumpable = edit.Name, edit.Command, edit.Jump, edit.JumpVia, edit.Jumpable
	}
	useJump := jumpHost != "" || via != ""
	var folderOpts []huh.Option[string]
	for _, f := range folders {
		folderOpts = append(folderOpts, huh.NewOption(s.path(f.ID), f.ID))
	}
	// Logins marked jumpable can be picked as this one's jump host; "" means typing one in.
	var hops []sshNode
	for _, n := range s.Nodes {
		if n.Type == sshLogin && (n.Jumpable || n.ID == via) && (edit == nil || n.ID != edit.ID) {
			hops = append(hops, n)
		}
	}
	sort.Slice(hops, func(i, j int) bool { return hops[i].Num < hops[j].Num })
	var viaOpts []huh.Option[string]
	for _, n := range hops {
		label := fmt.Sprintf("#%d %s", n.Num, s.path(n.ID))
		if args, err := n.args(); err == nil {
			if t, _ := sshTarget(args); t != "" {
				label += "  (" + t + ")"
			}
		}
		viaOpts = append(viaOpts, huh.NewOption(label, n.ID))
	}
	viaOpts = append(viaOpts, huh.NewOption("Other host — type it in", ""))
	// A new login without a typed jump host starts on the first jumpable login.
	if via == "" && jumpHost == "" && len(hops) > 0 {
		via = hops[0].ID
	}
	viaOpts = chosenFirst(viaOpts, via)
	folderOpts = chosenFirst(folderOpts, parent)
	viaHelp := "Logins marked jumpable: their host, port and password are used, and stay in step if they change."
	if len(hops) == 0 {
		viaHelp = "No logins are marked jumpable yet — answer Yes to \"Can other logins jump through this one?\" " +
			"on one, or press j on it in payload ssh table. Until then, type the jump host."
	}
	title, keep := "New ssh login", ""
	if edit != nil {
		title, keep = "Edit "+s.path(edit.ID)+".ssh", " Leave empty to keep the saved one."
	}
	err := huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().Title("Client folder").Options(folderOpts...).Height(selectHeight(len(folderOpts), "")).Value(&parent),
			huh.NewInput().Title(title).Description("Login name, e.g. onboarding or onboarding-db (shown as onboarding.ssh)").
				Value(&name).Validate(func(v string) error { return sshNameError(strings.TrimSpace(v)) }),
			huh.NewInput().Title("ssh command").Description("e.g. ssh root@10.0.0.5  (or -p 2222 root@10.0.0.5)").
				Value(&command).Validate(checkSSHCommand),
			huh.NewInput().Title("Password").Description("Saved in the env vault, bound to this login."+keep).
				EchoMode(huh.EchoModePassword).Value(&pw),
		),
		huh.NewGroup(
			huh.NewConfirm().Title("Through a jump host (-J)?").Affirmative("Yes").Negative("No").Value(&useJump),
			huh.NewConfirm().Title("Can other logins jump through this one?").
				Description("Marks it jumpable: other logins can then pick it under \"Jump through\".").
				Affirmative("Yes").Negative("No").Value(&jumpable),
		),
		huh.NewGroup(
			huh.NewSelect[string]().Title("Jump through").
				Description(viaHelp).
				Options(viaOpts...).Height(selectHeight(len(viaOpts), viaHelp)).Value(&via),
		).WithHideFunc(func() bool { return !useJump }),
		huh.NewGroup(
			huh.NewInput().Title("Jump host").Description("e.g. admin@1.2.3.4 (several: a@h1,b@h2)").Value(&jumpHost).
				Validate(func(v string) error {
					if v = strings.TrimSpace(v); v == "" || strings.ContainsAny(v, " \t") {
						return fmt.Errorf("enter the jump host, e.g. admin@1.2.3.4")
					}
					return nil
				}),
			huh.NewInput().Title("Jump host password").
				Description("Saved in the same env entry. Leave empty if it's the same as the password above."+keep).
				EchoMode(huh.EchoModePassword).Value(&jumpPW),
		).WithHideFunc(func() bool { return !useJump || via != "" }),
	).WithTheme(theme()).WithShowHelp(true).Run()
	if err != nil {
		return styleMuted.Render("Nothing saved.")
	}

	name = strings.TrimSpace(name)
	command, fromCommand := splitJump(strings.TrimSpace(command)) // a pasted -J moves to the jump field
	switch {
	case !useJump:
		jumpHost, jumpPW, via = "", "", ""
		if fromCommand != "" {
			jumpHost = fromCommand
		}
	case via != "":
		jumpHost, jumpPW = "", "" // the jump login supplies both
	case strings.TrimSpace(jumpHost) == "":
		jumpHost = fromCommand
	}
	jumpHost = strings.TrimSpace(jumpHost)
	except := ""
	if edit != nil {
		except = edit.ID
	}
	if s.nameTaken(parent, name, except) {
		return styleErr.Render("✗ " + name + " already exists in " + s.path(parent))
	}
	node := edit
	if node == nil {
		s.Nodes = append(s.Nodes, sshNode{ID: newSSHID(), Type: sshLogin})
		node = &s.Nodes[len(s.Nodes)-1]
	}
	node.Parent, node.Name, node.Command, node.Jump = parent, name, command, jumpHost
	node.JumpVia, node.Jumpable = via, jumpable
	if err := saveLoginPasswords(node, pw, jumpPW); err != nil {
		return styleErr.Render("✗ could not save the password: " + err.Error())
	}
	if err := s.save(); err != nil {
		return styleErr.Render("✗ could not save: " + err.Error())
	}
	return styleOK.Render("✓ Saved " + s.path(node.ID) + ".ssh" + styleMuted.Render(" · passwords in the env vault as "+node.envName()))
}

// saveLoginPasswords stores a login's passwords in its env entry: a plain value without a jump host,
// or a group {"server", "jump"} with one. Empty passwords keep what's saved; an empty jump password on
// a new entry means the same as the server's.
func saveLoginPasswords(n *sshNode, pw, jumpPW string) error {
	vals, err := loadEnv()
	if err != nil {
		return err
	}
	name := n.envName()
	plain, hasPlain := vals[name]
	server, hasServer := vals[name+".server"]
	jump, hasJump := vals[name+".jump"]
	if !hasServer && hasPlain {
		server, hasServer = plain, true
	}
	if pw != "" {
		server, hasServer = pw, true
	}
	if jumpPW != "" {
		jump, hasJump = jumpPW, true
	}
	delete(vals, name)
	delete(vals, name+".server")
	delete(vals, name+".jump")
	switch {
	case n.Jump != "" && hasServer:
		if !hasJump {
			jump = server
		}
		vals[name+".server"], vals[name+".jump"] = server, jump
		return saveEnv(vals, name)
	case n.Jump != "" && hasJump:
		vals[name+".jump"] = jump
		return saveEnv(vals, name)
	case hasServer: // no jump host: a single value (a former group becomes plain)
		vals[name] = server
		return saveEnv(vals)
	}
	return saveEnv(vals)
}

// dropEnvEntries removes env vault entries (plain values or whole groups), e.g. a deleted login's.
func dropEnvEntries(names []string) {
	if len(names) == 0 {
		return
	}
	vals, err := loadEnv()
	if err != nil {
		return
	}
	for _, name := range names {
		delete(vals, name)
		for _, k := range envGroupKeys(vals, name) {
			delete(vals, k)
		}
	}
	_ = saveEnv(vals)
}

// folderIcon is what the table shows for a folder: its logo (2 cells × 1 line, the size of an emoji
// so every row is one line), its emoji, or a plain folder.
func folderIcon(n sshNode) string {
	if n.Logo != "" {
		if lines := logoLines(n.Logo, 2, 1); lines != nil {
			return lines[0]
		}
	}
	return firstNonEmpty(n.Icon, "📁")
}

// toggleJumpable marks the selected login jumpable (others can pick it as their jump host) or not.
func (m *sshTableModel) toggleJumpable() string {
	r := m.rows[m.cursor]
	n := m.store.get(r.ID)
	if n == nil || n.Type != sshLogin {
		return styleMuted.Render("Only ssh logins can be jump hosts.")
	}
	n.Jumpable = !n.Jumpable
	if err := m.store.save(); err != nil {
		n.Jumpable = !n.Jumpable
		return styleErr.Render("✗ could not save: " + err.Error())
	}
	m.rows[m.cursor] = *n
	if n.Jumpable {
		return styleOK.Render("✓ " + n.Name + ".ssh is jumpable — other logins can pick it under Jump through")
	}
	msg := "✓ " + n.Name + ".ssh is no longer jumpable"
	if users := m.store.jumpUsers(n.ID); len(users) > 0 {
		msg += styleMuted.Render(" (the " + plural(len(users), "login") + " already using it keep it)")
	}
	return styleOK.Render(msg)
}

// chosenFirst moves the option holding value to the top. huh scrolls a select so its chosen option
// is the first line shown, hiding every option above it until ↑ is pressed; with the chosen one
// first, the whole list shows.
func chosenFirst[T comparable](opts []huh.Option[T], value T) []huh.Option[T] {
	for i, o := range opts {
		if o.Value == value && i > 0 {
			out := append([]huh.Option[T]{o}, opts[:i]...)
			return append(out, opts[i+1:]...)
		}
	}
	return opts
}

// selectHeight is the height to give a huh select: its title, description and every option (up to
// a cap, after which it scrolls). Without an explicit height huh sizes the list before it is shown
// and scrolls it to the bottom, so only the last option is visible until ↑ is pressed.
func selectHeight(options int, description string) int {
	h := 1 + options // title + options
	if description != "" {
		h += 1 + len([]rune(description))/76 // wrapped at a typical form width
	}
	return min(h, 18)
}
