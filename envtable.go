package main

import (
	"fmt"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
)

// payload env table: the env vault as a red, paged table with every value masked until revealed.
// It moves, copies and deletes with the "table" key bindings; "env_table" adds reveal / reveal_all.

// envMask is fixed-length so a hidden value doesn't give away its length.
const envMask = "••••••••"

var (
	envRed        = lipgloss.AdaptiveColor{Light: "#B91C1C", Dark: "#F87171"}
	styleEnvBadge = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#2A0707")).Background(lipgloss.Color("#F87171")).Padding(0, 1)
	styleEnvRed   = lipgloss.NewStyle().Foreground(envRed).Bold(true)
)

type envRow struct{ name, value string }

func loadEnvRows() []envRow {
	vals := mustLoadEnv()
	rows := make([]envRow, 0, len(vals))
	for _, n := range envNames(vals) {
		rows = append(rows, envRow{n, vals[n]})
	}
	return rows
}

// renderEnvPage draws one page; shown says which names are revealed, selected is the index within the page.
func renderEnvPage(rows []envRow, page, selected int, shown map[string]bool) string {
	start := page * perPage
	end := min(start+perPage, len(rows))

	t := table.New().
		Border(lipgloss.RoundedBorder()).
		BorderStyle(lipgloss.NewStyle().Foreground(envRed)).
		Headers("#", "NAME", "VALUE").
		StyleFunc(func(row, col int) lipgloss.Style {
			s := lipgloss.NewStyle().Padding(0, 1)
			switch {
			case row == table.HeaderRow:
				return s.Bold(true).Foreground(envRed)
			case row == selected:
				s = s.Bold(true).Foreground(lipgloss.Color("#2A0707")).Background(lipgloss.Color("#F87171"))
			case col == 0:
				s = s.Foreground(muted)
			case col == 2:
				s = s.Foreground(lipgloss.AdaptiveColor{Light: "#374151", Dark: "#C9D1D9"})
			}
			if col == 0 {
				s = s.Align(lipgloss.Right)
			}
			return s
		})
	for i := start; i < end; i++ {
		r := rows[i]
		v := envMask
		if shown[r.name] {
			v = previewValue(r.value, 40)
		}
		t.Row(strconv.Itoa(i+1), r.name, v)
	}
	return t.Render()
}

// previewValue puts a value on one line and cuts it to max runes.
func previewValue(v string, max int) string {
	v = strings.Join(strings.Fields(v), " ")
	if r := []rune(v); len(r) > max {
		v = string(r[:max-1]) + "…"
	}
	return v
}

func envTableHeader(rows []envRow, page int) string {
	return styleEnvBadge.Render("env vault") + "  " + styleMuted.Render(
		fmt.Sprintf("%d set · page %d of %d", len(rows), page+1, pageCount(len(rows))))
}

func envTableCmd(args []string) {
	rows := loadEnvRows()
	if len(rows) == 0 {
		info("No env values yet. Run:  payload env set <name> <value>")
		return
	}
	page := 0
	if len(args) > 0 {
		n, err := strconv.Atoi(args[0])
		if err != nil || n < 1 || n > pageCount(len(rows)) {
			fail("page must be between 1 and %d", pageCount(len(rows)))
		}
		page = n - 1
	}

	if !interactive {
		fmt.Println(envTableHeader(rows, page))
		fmt.Println(renderEnvPage(rows, page, -1, nil))
		if page+1 < pageCount(len(rows)) {
			info("Next page:  payload env table %d", page+2)
		}
		return
	}
	if _, err := tea.NewProgram(envTableModel{rows: rows, page: page, shown: map[string]bool{}}).Run(); err != nil {
		fail("%v", err)
	}
}

type envTableModel struct {
	rows   []envRow
	page   int
	cursor int             // index within the current page
	shown  map[string]bool // revealed names; forgotten when the table closes
	status string

	confirmDrop string // name awaiting a y/N answer before it is deleted
}

func (m envTableModel) Init() tea.Cmd { return nil }

func (m envTableModel) onPage() int {
	return min(perPage, len(m.rows)-m.page*perPage)
}

func (m envTableModel) current() envRow {
	return m.rows[m.page*perPage+m.cursor]
}

func (m envTableModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	pages := pageCount(len(m.rows))
	m.status = ""
	if k.String() == "ctrl+c" {
		return m, tea.Quit
	}
	if m.confirmDrop != "" {
		return m.answerDrop(k)
	}
	switch {
	case pressed(k, "table", "quit"):
		return m, tea.Quit
	case pressed(k, "env_table", "reveal"):
		n := m.current().name
		m.shown[n] = !m.shown[n]
	case pressed(k, "env_table", "reveal_all"):
		all := true
		for _, r := range m.rows {
			all = all && m.shown[r.name]
		}
		for _, r := range m.rows {
			m.shown[r.name] = !all
		}
	case pressed(k, "table", "delete"):
		m.confirmDrop = m.current().name
	case pressed(k, "table", "copy"):
		r := m.current()
		if err := copyText(r.value); err != nil {
			m.status = styleErr.Render("✗ could not copy " + r.name + ": " + err.Error())
		} else {
			m.status = styleOK.Render("✓ Copied " + r.name + " to the clipboard")
		}
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
	case pressed(k, "table", "first"):
		m.page, m.cursor = 0, 0
	case pressed(k, "table", "last"):
		m.page = pages - 1
		m.cursor = m.onPage() - 1
	}
	return m, nil
}

// answerDrop deletes the name awaiting confirmation on a confirm_delete key; any other key cancels.
func (m envTableModel) answerDrop(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	name := m.confirmDrop
	m.confirmDrop = ""
	if !pressed(k, "table", "confirm_delete") {
		m.status = styleMuted.Render("Not deleted.")
		return m, nil
	}
	vals, err := loadEnv()
	if err == nil {
		delete(vals, name)
		err = saveEnv(vals)
	}
	if err != nil {
		m.status = styleErr.Render("✗ could not delete " + name + ": " + err.Error())
		return m, nil
	}
	i := m.page*perPage + m.cursor
	m.rows = append(m.rows[:i:i], m.rows[i+1:]...)
	delete(m.shown, name)
	m.status = styleOK.Render("✓ Deleted " + name)
	if len(m.rows) == 0 {
		m.status += styleMuted.Render(" · no env values left")
		return m, tea.Quit
	}
	m.page = min(m.page, pageCount(len(m.rows))-1)
	m.cursor = min(m.cursor, m.onPage()-1)
	return m, nil
}

func (m envTableModel) View() string {
	if len(m.rows) == 0 {
		return m.status + "\n"
	}
	if m.confirmDrop != "" {
		m.status = styleWarn.Render("Delete "+m.confirmDrop+"? ") + styleMuted.Render(keyLabel("table", "confirm_delete")+" to delete · any other key cancels")
	}
	pages := pageCount(len(m.rows))
	dots := make([]string, pages)
	for i := range dots {
		if i == m.page {
			dots[i] = styleEnvRed.Render("●")
		} else {
			dots[i] = styleMuted.Render("○")
		}
	}
	pager := strings.Join(dots, " ")
	if pages > 12 {
		pager = styleEnvRed.Render(fmt.Sprintf("%d/%d", m.page+1, pages))
	}
	l := func(action string) string { return keyLabel("table", action) }
	help := styleMuted.Render(fmt.Sprintf("%s/%s page · %s/%s select · %s 👁 show/hide · %s all · %s copy · %s delete · %s quit",
		l("prev_page"), l("next_page"), l("up"), l("down"), keyLabel("env_table", "reveal"), keyLabel("env_table", "reveal_all"),
		l("copy"), l("delete"), l("quit")))
	return envTableHeader(m.rows, m.page) + "\n" +
		renderEnvPage(m.rows, m.page, m.cursor, m.shown) + "\n" +
		"  " + pager + "   " + help + "\n" +
		m.status + "\n"
}
