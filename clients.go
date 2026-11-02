package main

import (
	"fmt"
	"net"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
)

// payload clients: the computers running "payload clipboard listen", in a purple table. Pick one
// and the editor opens; the save key sends the text to that computer only.

var (
	clientPurple      = lipgloss.AdaptiveColor{Light: "#6D28D9", Dark: "#C4B5FD"}
	styleClientsBadge = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#1E1035")).Background(lipgloss.Color("#C4B5FD")).Padding(0, 1)
	styleClients      = lipgloss.NewStyle().Foreground(clientPurple).Bold(true)
)

type clientRow struct {
	clipPeer
	self bool
}

// findClients searches the network and marks this computer's own listener.
func findClients() ([]clientRow, error) {
	peers, err := clipDiscover()
	if err != nil {
		return nil, err
	}
	local := map[string]bool{}
	for _, ip := range localIPs() {
		local[ip] = true
	}
	rows := make([]clientRow, len(peers))
	for i, p := range peers {
		rows[i] = clientRow{clipPeer: p, self: local[p.IP] || net.ParseIP(p.IP).IsLoopback()}
	}
	return rows, nil
}

func clientsCmd() {
	if interactive && savedClientName() == "" {
		if !askClientName() {
			return
		}
	}
	rows, err := findClients()
	if err != nil {
		fail("could not search the network: %v", err)
	}
	if !interactive {
		fmt.Println(clientsHeader(rows, 0))
		if len(rows) == 0 {
			info("No one is listening. Run \"payload clipboard listen\" on the other computers.")
			return
		}
		fmt.Println(renderClientsPage(rows, 0, -1))
		return
	}

	m := clientsModel{rows: rows}
	for {
		res, err := tea.NewProgram(m).Run()
		if err != nil {
			fail("%v", err)
		}
		m = res.(clientsModel)
		action, target := m.action, m.target
		m.action, m.status = "", ""
		switch action {
		case "send":
			text, ok := editTextVerbTUI("send to "+target.Name, styleClientsBadge, "",
				"Type or paste the text to send to "+target.Name+" ("+target.IP+")", "send",
				func(s string) string { return styleMuted.Render(humanSize(len(s))) })
			if !ok {
				m.status = styleMuted.Render("Nothing sent.")
				break
			}
			if err := clipDeliver(target.IP, clientName(), text); err != nil {
				m.status = styleErr.Render("✗ could not send to " + target.Name + ": " + err.Error())
			} else {
				m.status = styleOK.Render("✓ Sent "+humanSize(len(text))+" to "+target.Name) +
					styleMuted.Render(" ("+target.IP+") · it's on their clipboard")
			}
		case "refresh":
		default:
			return // quit
		}
		if rows, err := findClients(); err == nil {
			m.rows = rows
		}
		if len(m.rows) > 0 {
			m.page = min(m.page, pageCount(len(m.rows))-1)
			m.cursor = min(m.cursor, m.onPage()-1)
		} else {
			m.page, m.cursor = 0, 0
		}
	}
}

// askClientName is the first-run screen: the name others see, saved to settings.json.
func askClientName() bool {
	name := hostName()
	err := huh.NewForm(huh.NewGroup(huh.NewInput().
		Title("What's your name?").
		Description("Other people see it in their payload clients table and when you send them text.\n" +
			"Saved in " + settingsPath() + " · change it later with: payload settings set-name <name>").
		Validate(func(s string) error {
			if strings.TrimSpace(s) == "" {
				return fmt.Errorf("enter a name")
			}
			if len([]rune(strings.TrimSpace(s))) > 40 {
				return fmt.Errorf("40 characters at most")
			}
			return nil
		}).
		Value(&name))).WithTheme(theme()).WithShowHelp(true).Run()
	if err != nil {
		return false
	}
	saved, err := setClientName(name)
	if err != nil {
		fail("could not save your name: %v", err)
	}
	success("Saved — others will see you as %s", styleTitle.Render(saved))
	return true
}

func clientsHeader(rows []clientRow, page int) string {
	s := styleClientsBadge.Render("clients") + "  " + styleMuted.Render(fmt.Sprintf("%d listening", len(rows)))
	if len(rows) > perPage {
		s += styleMuted.Render(fmt.Sprintf(" · page %d of %d", page+1, pageCount(len(rows))))
	}
	return s + styleMuted.Render(" · you are "+clientName())
}

func renderClientsPage(rows []clientRow, page, selected int) string {
	start := page * perPage
	end := min(start+perPage, len(rows))
	t := table.New().
		Border(lipgloss.RoundedBorder()).
		BorderStyle(lipgloss.NewStyle().Foreground(clientPurple)).
		Headers("#", "NAME", "IP").
		StyleFunc(func(row, col int) lipgloss.Style {
			s := lipgloss.NewStyle().Padding(0, 1)
			switch {
			case row == table.HeaderRow:
				return s.Bold(true).Foreground(clientPurple)
			case row == selected:
				s = s.Bold(true).Foreground(lipgloss.Color("#1E1035")).Background(lipgloss.Color("#C4B5FD"))
			case col == 0:
				s = s.Foreground(muted)
			case col == 2:
				s = s.Foreground(clientPurple)
			}
			if col == 0 {
				s = s.Align(lipgloss.Right)
			}
			return s
		})
	for i := start; i < end; i++ {
		r := rows[i]
		name := r.Name
		if r.self {
			name += " (this computer)"
		}
		t.Row(strconv.Itoa(i+1), name, r.IP)
	}
	return t.Render()
}

type clientsModel struct {
	rows   []clientRow
	page   int
	cursor int
	status string

	action string // send, refresh, or "" to quit
	target clipPeer
}

func (m clientsModel) Init() tea.Cmd { return nil }

func (m clientsModel) onPage() int {
	return min(perPage, len(m.rows)-m.page*perPage)
}

func (m clientsModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	m.status = ""
	if k.String() == "ctrl+c" || pressed(k, "table", "quit") {
		return m, tea.Quit
	}
	if pressed(k, "clients_table", "refresh") {
		m.action = "refresh"
		return m, tea.Quit
	}
	if len(m.rows) == 0 {
		return m, nil
	}
	pages := pageCount(len(m.rows))
	switch {
	case pressed(k, "clients_table", "send"):
		m.action, m.target = "send", m.rows[m.page*perPage+m.cursor].clipPeer
		return m, tea.Quit
	case pressed(k, "table", "up"):
		if m.cursor > 0 {
			m.cursor--
		} else if m.page > 0 {
			m.page--
			m.cursor = m.onPage() - 1
		}
	case pressed(k, "table", "down"):
		if m.cursor < m.onPage()-1 {
			m.cursor++
		} else if m.page < pages-1 {
			m.page++
			m.cursor = 0
		}
	case pressed(k, "table", "next_page"):
		if m.page < pages-1 {
			m.page++
			m.cursor = min(m.cursor, m.onPage()-1)
		}
	case pressed(k, "table", "prev_page"):
		if m.page > 0 {
			m.page--
		}
	}
	return m, nil
}

func (m clientsModel) View() string {
	if m.action != "" {
		if m.action == "refresh" {
			return styleMuted.Render("Looking for listeners on the network…") + "\n"
		}
		return ""
	}
	c := func(action string) string { return keyLabel("clients_table", action) }
	l := func(action string) string { return keyLabel("table", action) }
	if len(m.rows) == 0 {
		return clientsHeader(m.rows, 0) + "\n" +
			styleWarn.Render("! ") + "No one is listening. Run " + styleCode.Render("payload clipboard listen") + " on the other computers.\n" +
			styleMuted.Render(fmt.Sprintf("  %s search again · %s quit", c("refresh"), l("quit"))) + "\n" + m.status + "\n"
	}
	help := styleMuted.Render(fmt.Sprintf("%s/%s select · %s send text · %s search again · %s quit",
		l("up"), l("down"), c("send"), c("refresh"), l("quit")))
	return clientsHeader(m.rows, m.page) + "\n" +
		renderClientsPage(m.rows, m.page, m.cursor) + "\n" +
		"  " + help + "\n" +
		m.status + "\n"
}
