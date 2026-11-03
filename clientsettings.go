package main

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
)

// Clipboard settings, opened from payload clients with "s": every client payload knows — listening
// now, or set up earlier — and whether its text is copied or held for approval. Toggling hold
// saves it and tells the client (now if listening, otherwise the next time payload sees it).

type settingsRow struct {
	id, name, ip string // ip is "" when the client isn't listening now
	hold         bool
}

func settingsRows(online []clientRow) []settingsRow {
	prefs := loadClientPrefs()
	seen := map[string]bool{}
	var rows []settingsRow
	for _, r := range online {
		if r.ID == "" || r.self || seen[r.ID] {
			continue // old versions have no device ID; your own text isn't filtered
		}
		seen[r.ID] = true
		rows = append(rows, settingsRow{id: r.ID, name: r.Name, ip: r.IP, hold: prefs[r.ID].Hold})
	}
	for id, p := range prefs {
		if !seen[id] && validDeviceID(id) {
			rows = append(rows, settingsRow{id: id, name: firstNonEmpty(p.Name, id), hold: p.Hold})
		}
	}
	sort.SliceStable(rows, func(i, j int) bool { return strings.ToLower(rows[i].name) < strings.ToLower(rows[j].name) })
	return rows
}

func clientSettingsCmd(online []clientRow) {
	m := clientSettingsModel{rows: settingsRows(online)}
	for _, r := range online {
		if r.ID == "" && !r.self {
			m.oldVersions = append(m.oldVersions, r.Name)
		}
	}
	if _, err := tea.NewProgram(m).Run(); err != nil {
		fail("%v", err)
	}
}

type clientSettingsModel struct {
	rows        []settingsRow
	cursor      int
	status      string
	busy        bool
	oldVersions []string // listening clients too old to have a device ID
}

// holdDoneMsg reports a saved toggle and whether the client heard about it yet.
type holdDoneMsg struct {
	i         int
	hold      bool
	err       error
	delivered bool
}

func (m clientSettingsModel) Init() tea.Cmd { return nil }

func (m clientSettingsModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case holdDoneMsg:
		m.busy = false
		r := &m.rows[msg.i]
		if msg.err != nil {
			m.status = styleErr.Render("✗ could not save: " + msg.err.Error())
			return m, nil
		}
		r.hold = msg.hold
		told := "they'll be told the next time they listen"
		if msg.delivered {
			told = "they've been told"
		}
		if msg.hold {
			m.status = styleWarn.Render("⏸ "+r.name+"'s text is now held for approval") + styleMuted.Render(" · "+told)
		} else {
			m.status = styleOK.Render("✓ "+r.name+"'s text goes onto your clipboard again") + styleMuted.Render(" · "+told)
		}
		return m, nil
	case tea.KeyMsg:
		if msg.String() == "ctrl+c" || pressed(msg, "table", "quit") {
			return m, tea.Quit
		}
		if m.busy || len(m.rows) == 0 {
			return m, nil
		}
		m.status = ""
		switch {
		case pressed(msg, "table", "up"):
			if m.cursor > 0 {
				m.cursor--
			}
		case pressed(msg, "table", "down"):
			if m.cursor < len(m.rows)-1 {
				m.cursor++
			}
		case pressed(msg, "client_settings", "hold"):
			m.busy = true
			i, r := m.cursor, m.rows[m.cursor]
			m.status = styleMuted.Render("Saving…")
			return m, func() tea.Msg {
				hold := !r.hold
				pref := clientPref{}
				kind := noticeRelease
				if hold {
					pref, kind = clientPref{Name: r.name, Hold: true}, noticeHold
				}
				if err := saveClientPref(r.id, pref); err != nil {
					return holdDoneMsg{i: i, err: err}
				}
				return holdDoneMsg{i: i, hold: hold, delivered: notifyClient(r.id, r.ip, kind)}
			}
		}
	}
	return m, nil
}

func (m clientSettingsModel) View() string {
	head := styleClientsBadge.Render("clipboard settings") + "  " +
		styleMuted.Render("you are "+clientName()+" · device "+deviceID())
	l := func(action string) string { return keyLabel("table", action) }
	var b strings.Builder
	b.WriteString(head + "\n")
	if len(m.rows) == 0 {
		b.WriteString(styleMuted.Render("  No clients yet — they appear here once they're listening on the network.") + "\n")
	} else {
		t := table.New().
			Border(lipgloss.RoundedBorder()).
			BorderStyle(lipgloss.NewStyle().Foreground(clientPurple)).
			Headers("#", "NAME", "DEVICE", "NOW", "THEIR TEXT").
			StyleFunc(func(row, col int) lipgloss.Style {
				s := lipgloss.NewStyle().Padding(0, 1)
				switch {
				case row == table.HeaderRow:
					return s.Bold(true).Foreground(clientPurple)
				case row == m.cursor:
					return s.Bold(true).Foreground(lipgloss.Color("#1E1035")).Background(lipgloss.Color("#C4B5FD"))
				case col == 4 && m.rows[row].hold:
					return s.Foreground(warning).Bold(true)
				case col == 0 || col == 2 || col == 4:
					return s.Foreground(muted)
				}
				return s
			})
		for i, r := range m.rows {
			now := "offline"
			if r.ip != "" {
				now = "listening · " + r.ip
			}
			text := "copied to your clipboard"
			if r.hold {
				text = "on hold — shown in listen, not copied"
			}
			t.Row(strconv.Itoa(i+1), r.name, r.id, now, text)
		}
		b.WriteString(t.Render() + "\n")
	}
	if len(m.oldVersions) > 0 {
		b.WriteString(styleMuted.Render("  Not shown (update payload to filter them): "+strings.Join(m.oldVersions, ", ")) + "\n")
	}
	b.WriteString(styleMuted.Render(fmt.Sprintf("  %s/%s select · %s hold / allow their text · %s back",
		l("up"), l("down"), keyLabel("client_settings", "hold"), l("quit"))) + "\n")
	b.WriteString(m.status + "\n")
	return b.String()
}
